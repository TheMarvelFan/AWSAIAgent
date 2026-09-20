// Package config loads runtime configuration from the environment.
// No config file, no flags: one source of truth that works identically on a
// Windows dev box, in docker compose, and on App Runner / ECS.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr    string
	GinMode     string
	LogLevel    string
	LogFormat   string
	CORSOrigins []string

	DatabaseURL   string
	DBMaxConns    int32
	DBMinConns    int32
	RunMigrations bool

	JWTSecret       []byte
	JWTIssuer       string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration

	LLMProvider        string
	AWSRegion          string
	AWSProfile         string
	BedrockModelID     string
	BedrockMaxTokens   int32
	BedrockTemperature float32

	AgentMaxHistory int
	AgentTimeout    time.Duration

	// AWSServiceAccountID / AWSServiceRoleName identify the single identity on
	// our side that assumes customer roles. Everything in the cross-account
	// design rests on this principal, so it is configured explicitly and
	// verified against STS at startup rather than inferred.
	AWSServiceAccountID string
	AWSServiceRoleName  string
	ConnectTemplateURL  string
	DefaultRegion       string
	// StackRegion is only where the CloudFormation connect stack RECORD lives.
	// The stack creates one IAM role, and since IAM is global, the role works in
	// every region regardless. However, the customer must have this region enabled
	// to open the link at all, and newer AWS accounts are pinned to a single
	// home region chosen at signup. us-east-1 can never be disabled, which is
	// the only reason it is the default.
	//
	// Separate from DefaultRegion on purpose: that one decides where the
	// customer's resources actually live, and is a real choice.
	StackRegion        string
	ConnectionCheckTTL time.Duration

	// Runner: "terraform" once the real one exists, "stub" until then.
	RunnerProvider string
	// TeardownAfter is how long applied resources live before an automatic
	// destroy job fires. Zero disables it.
	TeardownAfter time.Duration
	MaxRunTime    time.Duration
	WorkerEnabled bool
	WorkerPoll    time.Duration
	// JobStaleAfter is how long a silent running job waits before being
	// declared abandoned. Must exceed the longest plausible run, or a slow
	// CloudFront distribution gets reclaimed from a perfectly healthy worker.
	JobStaleAfter time.Duration

	TerraformBinary string
	// TFWorkRoot must survive between plan and apply: apply runs the SAVED plan
	// file, which is what binds an approval to an exact set of changes.
	TFWorkRoot    string
	TFStateBucket string
	TFStateRegion string
	// TFWorkspaceTTL is how long an unreferenced workspace survives. Each holds
	// a full copy of the AWS provider, so this is a disk-space control.
	TFWorkspaceTTL time.Duration
}

const (
	RunnerStub      = "stub"
	RunnerTerraform = "terraform"
)

const (
	ProviderBedrock = "bedrock"
	ProviderStub    = "stub"
)

// Load reads the environment and validates it. It fails loudly at startup
// rather than at the first request.
func Load() (*Config, error) {
	cfg := &Config{
		HTTPAddr:    env("HTTP_ADDR", ":8080"),
		GinMode:     env("GIN_MODE", "debug"),
		LogLevel:    env("LOG_LEVEL", "info"),
		LogFormat:   env("LOG_FORMAT", "text"),
		CORSOrigins: splitAndTrim(env("CORS_ALLOWED_ORIGINS", "http://localhost:5173")),

		DatabaseURL:   os.Getenv("DATABASE_URL"),
		RunMigrations: envBool("RUN_MIGRATIONS", true),

		JWTSecret: []byte(os.Getenv("JWT_SECRET")),
		JWTIssuer: env("JWT_ISSUER", "ai-aws-architect"),

		AWSServiceAccountID: os.Getenv("AWS_SERVICE_ACCOUNT_ID"),
		AWSServiceRoleName:  env("AWS_SERVICE_ROLE_NAME", "ai-aws-architect-provisioner"),
		ConnectTemplateURL:  os.Getenv("AWS_CONNECT_TEMPLATE_URL"),
		DefaultRegion:       env("AWS_DEFAULT_PROVISION_REGION", "ap-south-1"),
		StackRegion:         env("AWS_CONNECT_STACK_REGION", "us-east-1"),

		RunnerProvider:  strings.ToLower(env("RUNNER_PROVIDER", RunnerStub)),
		TerraformBinary: env("TERRAFORM_BINARY", "terraform"),
		TFWorkRoot:      env("TF_WORK_ROOT", "/var/tmp/ai-aws-architect"),
		TFStateBucket:   os.Getenv("TF_STATE_BUCKET"),
		TFStateRegion:   env("TF_STATE_REGION", "ap-south-1"),
		WorkerEnabled:   envBool("WORKER_ENABLED", true),

		LLMProvider:    strings.ToLower(env("LLM_PROVIDER", ProviderStub)),
		AWSRegion:      env("AWS_REGION", "ap-south-1"),
		AWSProfile:     os.Getenv("AWS_PROFILE"),
		BedrockModelID: env("BEDROCK_MODEL_ID", "apac.anthropic.claude-sonnet-4-5-20250929-v1:0"),
	}

	var err error
	if cfg.DBMaxConns, err = envInt32("DB_MAX_CONNS", 10); err != nil {
		return nil, err
	}
	if cfg.DBMinConns, err = envInt32("DB_MIN_CONNS", 1); err != nil {
		return nil, err
	}
	if cfg.AccessTokenTTL, err = envDuration("ACCESS_TOKEN_TTL", 15*time.Minute); err != nil {
		return nil, err
	}
	if cfg.RefreshTokenTTL, err = envDuration("REFRESH_TOKEN_TTL", 30*24*time.Hour); err != nil {
		return nil, err
	}
	if cfg.BedrockMaxTokens, err = envInt32("BEDROCK_MAX_TOKENS", 4096); err != nil {
		return nil, err
	}
	if cfg.BedrockTemperature, err = envFloat32("BEDROCK_TEMPERATURE", 0.2); err != nil {
		return nil, err
	}
	if cfg.AgentTimeout, err = envDuration("AGENT_TIMEOUT", 90*time.Second); err != nil {
		return nil, err
	}
	if cfg.ConnectionCheckTTL, err = envDuration("AWS_CONNECTION_CHECK_TTL", 60*time.Second); err != nil {
		return nil, err
	}
	if cfg.TeardownAfter, err = envDuration("DEPLOY_TEARDOWN_AFTER", 45*time.Minute); err != nil {
		return nil, err
	}
	if cfg.MaxRunTime, err = envDuration("DEPLOY_MAX_RUN_TIME", 20*time.Minute); err != nil {
		return nil, err
	}
	if cfg.WorkerPoll, err = envDuration("WORKER_POLL_INTERVAL", 2*time.Second); err != nil {
		return nil, err
	}
	// 25m, not 15m. validate() refuses to start unless this exceeds
	// DEPLOY_MAX_RUN_TIME, which defaults to 20m - so the old default made a
	// checkout without these variables in .env fail to boot at all.
	if cfg.JobStaleAfter, err = envDuration("JOB_STALE_AFTER", 25*time.Minute); err != nil {
		return nil, err
	}
	if cfg.TFWorkspaceTTL, err = envDuration("TF_WORKSPACE_TTL", 2*time.Hour); err != nil {
		return nil, err
	}
	maxHistory, err := envInt32("AGENT_MAX_HISTORY_MESSAGES", 20)
	if err != nil {
		return nil, err
	}
	cfg.AgentMaxHistory = int(maxHistory)

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if c.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	// 32 bytes is the floor for a usable HMAC-SHA256 key. Refusing a short
	// secret at boot is cheaper than discovering it in a security review.
	if len(c.JWTSecret) < 32 {
		return fmt.Errorf("JWT_SECRET must be at least 32 characters (got %d)", len(c.JWTSecret))
	}
	switch c.LLMProvider {
	case ProviderBedrock, ProviderStub:
	default:
		return fmt.Errorf("LLM_PROVIDER must be %q or %q, got %q", ProviderBedrock, ProviderStub, c.LLMProvider)
	}
	if c.LLMProvider == ProviderBedrock && c.BedrockModelID == "" {
		return fmt.Errorf("BEDROCK_MODEL_ID is required when LLM_PROVIDER=bedrock")
	}
	if c.AgentMaxHistory < 2 {
		return fmt.Errorf("AGENT_MAX_HISTORY_MESSAGES must be >= 2")
	}
	// Not fatal: proposals work fine without a connection, so the API still
	// boots. Only the connect flow needs these.
	if c.AWSServiceAccountID != "" && len(c.AWSServiceAccountID) != 12 {
		return fmt.Errorf("AWS_SERVICE_ACCOUNT_ID must be a 12-digit account ID")
	}
	if c.RunnerProvider == RunnerTerraform && c.TFStateBucket == "" {
		return fmt.Errorf("TF_STATE_BUCKET is required when RUNNER_PROVIDER=terraform")
	}
	switch c.RunnerProvider {
	case RunnerStub, RunnerTerraform:
	default:
		return fmt.Errorf("RUNNER_PROVIDER must be %q or %q, got %q", RunnerStub, RunnerTerraform, c.RunnerProvider)
	}
	if c.JobStaleAfter <= c.MaxRunTime {
		// Otherwise a healthy long-running job gets reclaimed as abandoned and
		// its deployment is marked unknown for no reason.
		return fmt.Errorf("JOB_STALE_AFTER (%s) must be greater than DEPLOY_MAX_RUN_TIME (%s)",
			c.JobStaleAfter, c.MaxRunTime)
	}
	return nil
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envInt32(key string, def int32) (int32, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return int32(n), nil
}

func envFloat32(key string, def float32) (float32, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	f, err := strconv.ParseFloat(v, 32)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return float32(f), nil
}

func envDuration(key string, def time.Duration) (time.Duration, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return d, nil
}

func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
