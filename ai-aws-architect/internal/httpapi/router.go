package httpapi

import (
	"net/http"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cloud-ai/ai-aws-architect/internal/auth"
	"github.com/cloud-ai/ai-aws-architect/internal/awsconnect"
	"github.com/cloud-ai/ai-aws-architect/internal/catalog"
	appconfig "github.com/cloud-ai/ai-aws-architect/internal/config"
	"github.com/cloud-ai/ai-aws-architect/internal/deployment"
	"github.com/cloud-ai/ai-aws-architect/internal/httpapi/handlers"
	"github.com/cloud-ai/ai-aws-architect/internal/httpapi/httperr"
	"github.com/cloud-ai/ai-aws-architect/internal/httpapi/middleware"
	"github.com/cloud-ai/ai-aws-architect/internal/reasoning"
	"github.com/cloud-ai/ai-aws-architect/internal/runner"
	"github.com/cloud-ai/ai-aws-architect/internal/store"
)

type Deps struct {
	Config  *appconfig.Config
	Pool    *pgxpool.Pool
	Auth    *auth.Service
	Chats   *store.ChatStore
	Agent   *reasoning.Service
	Catalog *catalog.Catalog
	AWS     *awsconnect.Service
	Deploy  *deployment.Service
	Runner  runner.Runner
}

// NewRouter wires every route. The URL shape mirrors the data model: config
// versions live under the chat they belong to, because they are never shared
// across chats.
func NewRouter(d Deps) *gin.Engine {
	gin.SetMode(ginMode(d.Config.GinMode))

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(cors.New(cors.Config{
		AllowOrigins:     d.Config.CORSOrigins,
		AllowMethods:     []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete, http.MethodOptions},
		AllowHeaders:     []string{"Authorization", "Content-Type", middleware.HeaderRequestID},
		ExposeHeaders:    []string{middleware.HeaderRequestID},
		AllowCredentials: false,
		MaxAge:           12 * time.Hour,
	}))
	// No file uploads on this API; keep multipart buffering minimal.
	r.MaxMultipartMemory = 1 << 20

	health := handlers.NewHealthHandler(d.Pool)
	authH := handlers.NewAuthHandler(d.Auth)
	chatH := handlers.NewChatHandler(d.Chats, d.Agent)
	configH := handlers.NewConfigHandler(d.Chats, d.Agent)
	catalogH := handlers.NewCatalogHandler(d.Catalog, d.Runner, d.Agent)
	connH := handlers.NewConnectionHandler(d.AWS)
	depH := handlers.NewDeploymentHandler(d.Deploy)

	r.GET("/healthz", health.Live)
	r.GET("/readyz", health.Ready)

	v1 := r.Group("/v1")

	// Public
	authGroup := v1.Group("/auth")
	{
		authGroup.POST("/signup", authH.Signup)
		authGroup.POST("/login", authH.Login)
		authGroup.POST("/refresh", authH.Refresh)
		authGroup.POST("/logout", authH.Logout)
	}

	// Authenticated
	secured := v1.Group("")
	secured.Use(middleware.RequireAuth(d.Auth))
	{
		secured.GET("/auth/me", authH.Me)
		secured.GET("/catalog", catalogH.List)

		// AWS account connection. Global to the user, not per chat: the
		// connection is an account-level fact, and surfacing it only inside a
		// chat would imply each chat needs its own.
		secured.GET("/aws/connection", connH.Status)
		secured.POST("/aws/connection", connH.Start)
		secured.POST("/aws/connection/verify", connH.Verify)
		secured.DELETE("/aws/connection", connH.Disconnect)

		secured.GET("/chats", chatH.List)
		secured.POST("/chats", chatH.Create)
		secured.GET("/chats/:chatID", chatH.Get)
		secured.PATCH("/chats/:chatID", chatH.Update)
		secured.DELETE("/chats/:chatID", chatH.Delete)

		secured.GET("/chats/:chatID/messages", chatH.ListMessages)
		secured.POST("/chats/:chatID/messages", chatH.SendMessage)

		secured.GET("/chats/:chatID/config", configH.Current)
		secured.GET("/chats/:chatID/config/diff", configH.Diff)
		secured.GET("/chats/:chatID/config/versions", configH.ListVersions)
		// Hand-edited configuration. Same validation pipeline, same
		// append-only history, same system message as a model proposal.
		secured.POST("/chats/:chatID/config/versions", configH.CreateManual)
		secured.GET("/chats/:chatID/config/versions/:version", configH.GetVersion)
		secured.POST("/chats/:chatID/config/versions/:version/revert", configH.Revert)

		// Deployment. Plan and approve are separate endpoints on purpose: the
		// boundary between reasoning and execution is enforced by routing, not
		// by a flag anything upstream could set.
		secured.POST("/chats/:chatID/config/versions/:version/plan", depH.Plan)
		secured.GET("/chats/:chatID/deployments", depH.ListForChat)
		secured.GET("/deployments/live", depH.Live)
		secured.GET("/deployments/:deploymentID", depH.Get)
		secured.POST("/deployments/:deploymentID/approve", depH.Approve)
		secured.POST("/deployments/:deploymentID/destroy", depH.Destroy)
		secured.POST("/deployments/:deploymentID/cancel", depH.Cancel)
		secured.POST("/deployments/:deploymentID/resolve", depH.Resolve)
	}

	r.NoRoute(func(c *gin.Context) {
		httperr.Write(c, http.StatusNotFound, "not_found", "no such endpoint")
	})
	return r
}

func ginMode(mode string) string {
	switch mode {
	case gin.ReleaseMode, gin.TestMode, gin.DebugMode:
		return mode
	default:
		return gin.ReleaseMode
	}
}
