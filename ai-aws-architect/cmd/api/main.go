package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/cloud-ai/ai-aws-architect/internal/auth"
	"github.com/cloud-ai/ai-aws-architect/internal/awsconnect"
	"github.com/cloud-ai/ai-aws-architect/internal/bedrock"
	"github.com/cloud-ai/ai-aws-architect/internal/catalog"
	"github.com/cloud-ai/ai-aws-architect/internal/config"
	"github.com/cloud-ai/ai-aws-architect/internal/deployment"
	"github.com/cloud-ai/ai-aws-architect/internal/httpapi"
	"github.com/cloud-ai/ai-aws-architect/internal/reasoning"
	"github.com/cloud-ai/ai-aws-architect/internal/runner"
	tfrunner "github.com/cloud-ai/ai-aws-architect/internal/runner/terraform"
	"github.com/cloud-ai/ai-aws-architect/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("startup failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	slog.SetDefault(newLogger(cfg))

	// Signal-aware context: Ctrl+C during startup aborts cleanly instead of
	// leaving a half-migrated database.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := store.Connect(ctx, cfg.DatabaseURL, store.PoolOptions{
		MaxConns: cfg.DBMaxConns,
		MinConns: cfg.DBMinConns,
	})
	if err != nil {
		return err
	}
	defer pool.Close()
	slog.Info("connected to postgres")

	if cfg.RunMigrations {
		if err := store.Migrate(ctx, pool); err != nil {
			return err
		}
	}

	cat, err := catalog.Load()
	if err != nil {
		return err
	}
	slog.Info("catalog loaded", "templates", len(cat.All()))

	users := store.NewUserStore(pool)
	tokens := store.NewTokenStore(pool)
	chats := store.NewChatStore(pool)
	connections := store.NewConnectionStore(pool)
	deployments := store.NewDeploymentStore(pool)
	jobs := store.NewJobStore(pool)

	authSvc := auth.NewService(
		users, tokens,
		auth.NewTokenIssuer(cfg.JWTSecret, cfg.JWTIssuer, cfg.AccessTokenTTL),
		cfg.RefreshTokenTTL,
	)

	stsClient, err := awsconnect.NewSTS(ctx, cfg.AWSRegion, cfg.AWSProfile)
	if err != nil {
		return err
	}
	awsSvc := awsconnect.NewService(connections, stsClient, awsconnect.Options{
		ServiceAccountID: cfg.AWSServiceAccountID,
		ServiceRoleName:  cfg.AWSServiceRoleName,
		TemplateURL:      cfg.ConnectTemplateURL,
		DefaultRegion:    cfg.DefaultRegion,
		CheckFreshness:   cfg.ConnectionCheckTTL,
	})
	verifyServiceIdentity(ctx, stsClient, cfg)

	llm, err := newLLM(ctx, cfg, cat)
	if err != nil {
		return err
	}
	if cfg.LLMProvider == config.ProviderStub {
		// Loud on purpose. Demoing keyword matching to judges by accident
		// would be the worst possible outcome.
		slog.Warn("STUB reasoning engine active - proposals are canned keyword matches, not model output. Set LLM_PROVIDER=bedrock for real reasoning.")
	} else {
		slog.Info("reasoning engine ready", "provider", cfg.LLMProvider, "model", llm.ModelID())
	}

	agentSvc := reasoning.NewService(chats, cat, llm, awsSvc, reasoning.Options{
		MaxHistory: cfg.AgentMaxHistory,
		Timeout:    cfg.AgentTimeout,
	})

	var runr runner.Runner
	switch cfg.RunnerProvider {
	case config.RunnerStub:
		runr = runner.NewStubRunner()
		slog.Warn("STUB infrastructure runner active - plans and applies are simulated and NOTHING is created in AWS. Set RUNNER_PROVIDER=terraform for real provisioning.")
	case config.RunnerTerraform:
		tr, err := tfrunner.New(tfrunner.Options{
			Binary:      cfg.TerraformBinary,
			WorkRoot:    cfg.TFWorkRoot,
			StateBucket: cfg.TFStateBucket,
			StateRegion: cfg.TFStateRegion,
		})
		if err != nil {
			return err
		}
		runr = tr
		warnMissingModules(cat, tr)
		slog.Info("terraform runner ready", "state_bucket", cfg.TFStateBucket, "work_root", cfg.TFWorkRoot)
	default:
		return fmt.Errorf("unknown RUNNER_PROVIDER %q", cfg.RunnerProvider)
	}

	deploySvc := deployment.NewService(deployments, jobs, chats, awsSvc, runr, deployment.Options{
		TeardownAfter: cfg.TeardownAfter,
		MaxRunTime:    cfg.MaxRunTime,
	})

	if cfg.WorkerEnabled {
		worker := deployment.NewWorker(deploySvc, jobs, deployments, deployment.WorkerOptions{
			PollInterval: cfg.WorkerPoll,
			StaleAfter:   cfg.JobStaleAfter,
			WorkspaceTTL: cfg.TFWorkspaceTTL,
		})
		// Shares the signal context, so Ctrl+C stops the worker. In-flight
		// Terraform is NOT killed mid-run; the run context carries its own
		// timeout, and a worker that dies leaves its job to be reclaimed as
		// unknown rather than silently lost.
		go worker.Run(ctx)
	} else {
		slog.Warn("deployment worker disabled - plan and apply jobs will queue but never run")
	}

	router := httpapi.NewRouter(httpapi.Deps{
		Config: cfg, Pool: pool, Auth: authSvc,
		Chats: chats, Agent: agentSvc, Catalog: cat, AWS: awsSvc,
		Deploy: deploySvc,
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// Generous: a single turn waits on the model. Must stay above
		// AGENT_TIMEOUT or the write is cut off mid-response.
		WriteTimeout: cfg.AgentTimeout + 30*time.Second,
		IdleTimeout:  120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("http server listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutdown signal received")
	}

	// Drain in-flight requests. A turn already talking to Bedrock gets to
	// finish and commit rather than leaving a chat without its reply.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	slog.Info("shutdown complete")
	return nil
}

// verifyServiceIdentity confirms our own AWS identity matches what we hand
// customers in the trust policy. A mismatch means every CloudFormation stack we
// generate would trust the wrong principal, and every assume would fail after
// the customer had already done their part. Warn rather than fail: proposals
// work without any AWS access at all.
func verifyServiceIdentity(ctx context.Context, s *awsconnect.STS, cfg *config.Config) {
	if cfg.AWSServiceAccountID == "" {
		slog.Warn("AWS_SERVICE_ACCOUNT_ID unset - the AWS connect flow is disabled; proposals still work")
		return
	}
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	identity, err := s.ServiceIdentity(checkCtx)
	if err != nil {
		slog.Warn("could not verify our own AWS identity; connect flow may fail", "err", err)
		return
	}
	if identity.AccountID != cfg.AWSServiceAccountID {
		slog.Error("AWS_SERVICE_ACCOUNT_ID does not match the credentials in use - generated trust policies would name the wrong principal",
			"configured", cfg.AWSServiceAccountID, "actual", identity.AccountID)
		return
	}
	slog.Info("aws service identity verified", "account_id", identity.AccountID, "arn", identity.ARN)
}

// warnMissingModules reports catalog templates with no bundled Terraform
// module. Those templates can still be proposed and planned as far as HCL
// generation, where they fail - better to say so at boot than to discover it
// when a judge picks that block.
func warnMissingModules(cat *catalog.Catalog, tr *tfrunner.Runner) {
	have := map[string]bool{}
	for _, m := range tr.Modules() {
		have[m] = true
	}
	var missing []string
	for _, id := range cat.IDs() {
		if !have[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		slog.Warn("catalog templates have no terraform module and cannot be provisioned",
			"templates", missing, "implemented", tr.Modules())
	}
}

func newLLM(ctx context.Context, cfg *config.Config, cat *catalog.Catalog) (reasoning.LLM, error) {
	if cfg.LLMProvider == config.ProviderStub {
		return reasoning.NewStubLLM(cat), nil
	}
	return bedrock.New(ctx, bedrock.Options{
		Region:      cfg.AWSRegion,
		Profile:     cfg.AWSProfile,
		ModelID:     cfg.BedrockModelID,
		MaxTokens:   cfg.BedrockMaxTokens,
		Temperature: cfg.BedrockTemperature,
	})
}

func newLogger(cfg *config.Config) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(cfg.LogLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	opts := &slog.HandlerOptions{Level: level}
	if strings.EqualFold(cfg.LogFormat, "json") {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}
