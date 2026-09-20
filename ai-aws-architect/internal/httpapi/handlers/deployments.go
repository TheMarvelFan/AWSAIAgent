package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/cloud-ai/ai-aws-architect/internal/deployment"
	"github.com/cloud-ai/ai-aws-architect/internal/domain"
	"github.com/cloud-ai/ai-aws-architect/internal/httpapi/httperr"
	"github.com/cloud-ai/ai-aws-architect/internal/httpapi/middleware"
)

type DeploymentHandler struct{ svc *deployment.Service }

func NewDeploymentHandler(svc *deployment.Service) *DeploymentHandler {
	return &DeploymentHandler{svc: svc}
}

type approveRequest struct {
	// PlanHash must be the hash the client was shown. This is the whole point
	// of the approval gate: without it, the plan a human read and the plan that
	// runs are not provably the same artifact.
	PlanHash string `json:"plan_hash" binding:"required"`
}

// Plan starts a deployment for a config version and returns 202 with a job id.
//
// 202, not 201: Terraform takes minutes, and an HTTP request cannot hold that
// open. The client polls the deployment.
func (h *DeploymentHandler) Plan(c *gin.Context) {
	chatID, err := chatIDParam(c)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	version, err := versionParam(c, "version")
	if err != nil {
		httperr.WriteError(c, err)
		return
	}

	dep, job, err := h.svc.Plan(c.Request.Context(), middleware.MustUserID(c), chatID, version)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"deployment": dep, "job": job})
}

// Approve is the human-in-the-loop gate: reasoning and execution are separate
// endpoints so the boundary is enforced by routing, not by a flag some caller
// could set.
func (h *DeploymentHandler) Approve(c *gin.Context) {
	id, err := deploymentIDParam(c)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	var req approveRequest
	if !bindJSON(c, &req) {
		return
	}

	dep, job, err := h.svc.Approve(c.Request.Context(), middleware.MustUserID(c), id, req.PlanHash)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"deployment": dep, "job": job})
}

func (h *DeploymentHandler) Destroy(c *gin.Context) {
	id, err := deploymentIDParam(c)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	dep, job, err := h.svc.Destroy(c.Request.Context(), middleware.MustUserID(c), id)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"deployment": dep, "job": job})
}

type resolveRequest struct {
	// Acknowledged is required and must be true. Not a default, because the
	// caller is taking responsibility for an assertion the system cannot make.
	Acknowledged bool   `json:"acknowledged"`
	Note         string `json:"note" binding:"max=500"`
}

// Resolve closes out a deployment whose contents could not be determined, on
// the strength of a human having checked AWS.
//
// The only exit from 'unknown' that does not involve running a teardown. See
// deployment.Service.Resolve for why it demands an explicit acknowledgement.
func (h *DeploymentHandler) Resolve(c *gin.Context) {
	id, err := deploymentIDParam(c)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	var req resolveRequest
	if !bindJSON(c, &req) {
		return
	}

	dep, err := h.svc.Resolve(c.Request.Context(), middleware.MustUserID(c), id,
		deployment.ResolveParams{Acknowledged: req.Acknowledged, Note: req.Note})
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, dep)
}

func (h *DeploymentHandler) Cancel(c *gin.Context) {
	id, err := deploymentIDParam(c)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	dep, err := h.svc.Cancel(c.Request.Context(), middleware.MustUserID(c), id)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, dep)
}

// Get is the polling endpoint. Includes the job history so a client can show
// retries and per-attempt errors rather than just a spinner.
func (h *DeploymentHandler) Get(c *gin.Context) {
	id, err := deploymentIDParam(c)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	userID := middleware.MustUserID(c)

	dep, err := h.svc.Get(c.Request.Context(), userID, id)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	jobs, err := h.svc.Jobs(c.Request.Context(), userID, id)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deployment": dep, "jobs": jobs})
}

// ListForChat returns deployment history plus the two pointers that must never
// be confused: what the conversation proposes, and what is actually live.
func (h *DeploymentHandler) ListForChat(c *gin.Context) {
	chatID, err := chatIDParam(c)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	userID := middleware.MustUserID(c)

	deps, err := h.svc.ListForChat(c.Request.Context(), userID, chatID, intQuery(c, "limit", 20, 1, 100))
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	deployed, err := h.svc.DeployedVersion(c.Request.Context(), chatID)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"deployments":             deps,
		"deployed_config_version": deployed,
	})
}

// Live lists everything that may still be billing, across all of a user's
// chats. The disconnect flow reads this before removing the connection that
// teardown depends on.
func (h *DeploymentHandler) Live(c *gin.Context) {
	deps, err := h.svc.Live(c.Request.Context(), middleware.MustUserID(c))
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deployments": deps})
}

func deploymentIDParam(c *gin.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param("deploymentID"))
	if err != nil {
		return uuid.Nil, domain.NewValidationError("deploymentID", "must be a UUID")
	}
	return id, nil
}
