package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/cloud-ai/ai-aws-architect/internal/domain"
	"github.com/cloud-ai/ai-aws-architect/internal/httpapi/httperr"
	"github.com/cloud-ai/ai-aws-architect/internal/httpapi/middleware"
	"github.com/cloud-ai/ai-aws-architect/internal/reasoning"
	"github.com/cloud-ai/ai-aws-architect/internal/store"
)

type ChatHandler struct {
	chats     *store.ChatStore
	reasoning *reasoning.Service
}

func NewChatHandler(chats *store.ChatStore, agentSvc *reasoning.Service) *ChatHandler {
	return &ChatHandler{chats: chats, reasoning: agentSvc}
}

type createChatRequest struct {
	Title string `json:"title" binding:"max=120"`
	// MonthlyBudgetUSD is a number the user states, not a tier we infer.
	MonthlyBudgetUSD *float64 `json:"monthly_budget_usd" binding:"omitempty,gt=0,lte=1000000"`
}

type updateChatRequest struct {
	Title            *string  `json:"title" binding:"omitempty,max=120"`
	MonthlyBudgetUSD *float64 `json:"monthly_budget_usd" binding:"omitempty,gt=0,lte=1000000"`
	// ClearBudget removes the ceiling. Explicit, so an omitted field can never
	// silently drop a cost constraint the user set earlier.
	ClearBudget bool `json:"clear_budget"`
	// Archived restores or re-archives a chat. A pointer so that a client
	// sending only a title cannot silently un-archive what it is renaming -
	// the same reasoning as ClearBudget.
	//
	// Archiving through here and through DELETE are the same operation; DELETE
	// remains for clients that expect it.
	Archived *bool `json:"archived"`
}

type sendMessageRequest struct {
	Content string `json:"content" binding:"required,max=8000"`
}

// List backs the collapsible history panel on the left.
func (h *ChatHandler) List(c *gin.Context) {
	limit := intQuery(c, "limit", 30, 1, 100)
	offset := intQuery(c, "offset", 0, 0, 100000)

	chats, err := h.chats.ListChats(c.Request.Context(), middleware.MustUserID(c), limit, offset, boolQuery(c, "include_archived"))
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"chats": chats, "limit": limit, "offset": offset})
}

func (h *ChatHandler) Create(c *gin.Context) {
	var req createChatRequest
	if !bindOptionalJSON(c, &req) {
		return
	}
	chat, err := h.chats.CreateChat(c.Request.Context(), middleware.MustUserID(c), req.Title, req.MonthlyBudgetUSD)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	c.JSON(http.StatusCreated, chat)
}

func (h *ChatHandler) Get(c *gin.Context) {
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
	c.JSON(http.StatusOK, chat)
}

// Update changes the title and/or the budget ceiling.
func (h *ChatHandler) Update(c *gin.Context) {
	chatID, err := chatIDParam(c)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	var req updateChatRequest
	if !bindJSON(c, &req) {
		return
	}
	if req.Title == nil && req.MonthlyBudgetUSD == nil && !req.ClearBudget && req.Archived == nil {
		httperr.WriteValidation(c, "body", "nothing to update")
		return
	}

	// An archived chat is frozen apart from being restored. Otherwise,
	// archiving means nothing: a client could keep renaming, re-budgeting and
	// messaging a chat it had told the user was put away.
	if req.Archived == nil || *req.Archived {
		if err := h.requireActive(c, chatID); err != nil {
			httperr.WriteError(c, err)
			return
		}
	}

	chat, err := h.chats.UpdateChat(c.Request.Context(), middleware.MustUserID(c),
		chatID, req.Title, req.MonthlyBudgetUSD, req.ClearBudget, req.Archived)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, chat)
}

// requireActive rejects operations on an archived chat.
//
// Returns ErrNotFound for a chat that is not the caller's, exactly as GetChat
// does, so archived-ness never becomes a way to probe for another user's chats.
func (h *ChatHandler) requireActive(c *gin.Context, chatID uuid.UUID) error {
	chat, err := h.chats.GetChat(c.Request.Context(), middleware.MustUserID(c), chatID)
	if err != nil {
		return err
	}
	if chat.ArchivedAt != nil {
		return fmt.Errorf("%w: this chat is archived; restore it before making changes",
			domain.ErrConflict)
	}
	return nil
}

// Delete is a soft delete. Config history survives, which matters because a
// version is evidence of what was proposed.
func (h *ChatHandler) Delete(c *gin.Context) {
	chatID, err := chatIDParam(c)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	if err := h.chats.ArchiveChat(c.Request.Context(), middleware.MustUserID(c), chatID); err != nil {
		httperr.WriteError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *ChatHandler) ListMessages(c *gin.Context) {
	chatID, err := chatIDParam(c)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	// GetChat first: it is the ownership check.
	if _, err := h.chats.GetChat(c.Request.Context(), middleware.MustUserID(c), chatID); err != nil {
		httperr.WriteError(c, err)
		return
	}

	limit := intQuery(c, "limit", 50, 1, 200)
	beforeSeq := int64Query(c, "before_seq", 0)

	messages, err := h.chats.ListMessages(c.Request.Context(), chatID, limit, beforeSeq)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	var nextBefore int64
	if len(messages) == limit && len(messages) > 0 {
		nextBefore = messages[0].Seq
	}
	c.JSON(http.StatusOK, gin.H{
		"messages":        messages,
		"next_before_seq": nextBefore,
	})
}

// SendMessage is the main endpoint. One call returns the user message, the
// assistant reply, and — when the architecture actually changed — the new
// config version plus the diff that produced it, so the right-hand panel can
// update from a single response.
func (h *ChatHandler) SendMessage(c *gin.Context) {
	chatID, err := chatIDParam(c)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	var req sendMessageRequest
	if !bindJSON(c, &req) {
		return
	}
	if err := h.requireActive(c, chatID); err != nil {
		httperr.WriteError(c, err)
		return
	}

	result, err := h.reasoning.SendMessage(c.Request.Context(), middleware.MustUserID(c), chatID, req.Content)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	c.JSON(http.StatusCreated, result)
}
