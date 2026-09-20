package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/cloud-ai/ai-aws-architect/internal/configdiff"
	"github.com/cloud-ai/ai-aws-architect/internal/domain"
	"github.com/cloud-ai/ai-aws-architect/internal/httpapi/httperr"
	"github.com/cloud-ai/ai-aws-architect/internal/httpapi/middleware"
	"github.com/cloud-ai/ai-aws-architect/internal/reasoning"
	"github.com/cloud-ai/ai-aws-architect/internal/store"
)

type ConfigHandler struct {
	chats *store.ChatStore
	// reasoning owns the validation pipeline - catalog, region and budget - so
	// a hand-edited config passes the same gates a model proposal does.
	reasoning *reasoning.Service
}

func NewConfigHandler(chats *store.ChatStore, reasoningSvc *reasoning.Service) *ConfigHandler {
	return &ConfigHandler{chats: chats, reasoning: reasoningSvc}
}

type manualEditRequest struct {
	// Document is a complete architecture configuration, not a patch. The
	// server re-validates and normalizes it: region is stamped, parameter
	// defaults are filled and estimated_cost is recomputed, so what gets stored
	// is never the raw submission.
	Document json.RawMessage `json:"document" binding:"required"`
	// BasedOnVersion is the version the editor was showing. A mismatch returns
	// 409 stale_config rather than silently overwriting someone else's change.
	BasedOnVersion int `json:"based_on_version" binding:"required,min=1"`
	// Note is narrated into the transcript as a system message.
	Note string `json:"note" binding:"max=500"`
}

// CreateManual appends a user-authored configuration version.
//
// The counterpart to sending a message: same append-only history, same
// validation, same system message narrating the change into the transcript.
// The only differences are that no model is involved and that validation
// errors are returned to the caller rather than discarded - the user is
// present and can fix them.
func (h *ConfigHandler) CreateManual(c *gin.Context) {
	chatID, err := chatIDParam(c)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	var req manualEditRequest
	if !bindJSON(c, &req) {
		return
	}

	result, err := h.reasoning.ApplyManualEdit(c.Request.Context(),
		middleware.MustUserID(c), chatID, req.Document, req.BasedOnVersion, req.Note)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

// Current returns the running config for the right-hand panel. 204 when the
// chat has no proposal yet, so the panel can render its empty state without
// treating it as an error.
func (h *ConfigHandler) Current(c *gin.Context) {
	chatID, err := chatIDParam(c)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	if _, err := h.chats.GetChat(c.Request.Context(), middleware.MustUserID(c), chatID); err != nil {
		httperr.WriteError(c, err)
		return
	}

	cv, err := h.chats.CurrentConfig(c.Request.Context(), chatID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.Status(http.StatusNoContent)
			return
		}
		httperr.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, cv)
}

// ListVersions returns metadata only, newest first. The documents themselves
// are fetched per version, so opening the history list stays cheap.
func (h *ConfigHandler) ListVersions(c *gin.Context) {
	chatID, err := chatIDParam(c)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	chat, err := h.chats.GetChat(c.Request.Context(), middleware.MustUserID(c), chatID)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}

	versions, err := h.chats.ListConfigVersions(c.Request.Context(), chatID, chat.CurrentConfigVersion)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	if versions == nil {
		versions = []domain.ConfigVersionMeta{}
	}
	c.JSON(http.StatusOK, gin.H{
		"versions":        versions,
		"current_version": chat.CurrentConfigVersion,
	})
}

func (h *ConfigHandler) GetVersion(c *gin.Context) {
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
	if _, err := h.chats.GetChat(c.Request.Context(), middleware.MustUserID(c), chatID); err != nil {
		httperr.WriteError(c, err)
		return
	}

	cv, err := h.chats.GetConfigVersion(c.Request.Context(), chatID, version)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, cv)
}

// Diff compares any two versions of the same chat.
//
//	GET /config/diff?from=2&to=5&format=both
//
// `from` defaults to the version before `to`; `to` defaults to the running
// config, so ?from=3 alone answers "what changed since v3".
func (h *ConfigHandler) Diff(c *gin.Context) {
	chatID, err := chatIDParam(c)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	chat, err := h.chats.GetChat(c.Request.Context(), middleware.MustUserID(c), chatID)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	if chat.CurrentConfigVersion == nil {
		httperr.WriteError(c, domain.ErrNotFound)
		return
	}

	to := intQuery(c, "to", *chat.CurrentConfigVersion, 1, 1<<30)
	from := intQuery(c, "from", to-1, 0, 1<<30)
	if from >= to && from != 0 {
		httperr.WriteValidation(c, "from", "must be less than 'to'")
		return
	}

	format := c.DefaultQuery("format", "both")
	switch format {
	case "json", "text", "both":
	default:
		httperr.WriteValidation(c, "format", "must be one of json, text, both")
		return
	}

	toCV, err := h.chats.GetConfigVersion(c.Request.Context(), chatID, to)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}

	// from=0 means "compare against nothing", i.e. show v1 as all additions.
	var fromDoc json.RawMessage
	var fromVersion *int
	if from > 0 {
		fromCV, err := h.chats.GetConfigVersion(c.Request.Context(), chatID, from)
		if err != nil {
			httperr.WriteError(c, err)
			return
		}
		fromDoc = fromCV.Document
		v := fromCV.Version
		fromVersion = &v
	}

	result := configdiff.Result{From: fromVersion, To: &toCV.Version}

	if format == "json" || format == "both" {
		changes, err := configdiff.Diff(fromDoc, toCV.Document)
		if err != nil {
			httperr.WriteError(c, err)
			return
		}
		result.Changes = changes
		result.Stats = configdiff.Summarize(changes)
	}
	if format == "text" || format == "both" {
		unified, err := configdiff.Unified(fromDoc, toCV.Document, intQuery(c, "context", 3, 0, 20))
		if err != nil && !errors.Is(err, configdiff.ErrTooLargeForText) {
			httperr.WriteError(c, err)
			return
		}
		result.Unified = unified
	}
	if result.Changes == nil {
		result.Changes = []configdiff.Change{}
	}
	c.JSON(http.StatusOK, result)
}

// Revert appends a new version carrying the target version's document. History
// is never rewritten, so a revert is itself revertible.
func (h *ConfigHandler) Revert(c *gin.Context) {
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

	note := fmt.Sprintf("Reverted the running configuration to version %d.", version)
	cv, msg, err := h.chats.Revert(c.Request.Context(), middleware.MustUserID(c), chatID, version, note)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"config_version": cv,
		"message":        msg,
	})
}
