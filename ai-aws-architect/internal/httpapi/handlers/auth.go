package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/cloud-ai/ai-aws-architect/internal/auth"
	"github.com/cloud-ai/ai-aws-architect/internal/domain"
	"github.com/cloud-ai/ai-aws-architect/internal/httpapi/httperr"
	"github.com/cloud-ai/ai-aws-architect/internal/httpapi/middleware"
)

type AuthHandler struct{ svc *auth.Service }

func NewAuthHandler(svc *auth.Service) *AuthHandler { return &AuthHandler{svc: svc} }

type signupRequest struct {
	Email       string `json:"email" binding:"required,email,max=254"`
	Password    string `json:"password" binding:"required,min=10,max=128"`
	DisplayName string `json:"display_name" binding:"max=80"`
}

type loginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type userDTO struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

type sessionDTO struct {
	User         userDTO `json:"user"`
	AccessToken  string  `json:"access_token"`
	TokenType    string  `json:"token_type"`
	ExpiresAt    string  `json:"expires_at"`
	RefreshToken string  `json:"refresh_token"`
}

func (h *AuthHandler) Signup(c *gin.Context) {
	var req signupRequest
	if !bindJSON(c, &req) {
		return
	}
	session, err := h.svc.Signup(c.Request.Context(), req.Email, req.Password, req.DisplayName, clientInfo(c))
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toSessionDTO(session))
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if !bindJSON(c, &req) {
		return
	}
	session, err := h.svc.Login(c.Request.Context(), req.Email, req.Password, clientInfo(c))
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, toSessionDTO(session))
}

// Refresh rotates the refresh token: the old one stops working the moment this
// succeeds, and the client must store the new one.
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req refreshRequest
	if !bindJSON(c, &req) {
		return
	}
	session, err := h.svc.Refresh(c.Request.Context(), req.RefreshToken, clientInfo(c))
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, toSessionDTO(session))
}

func (h *AuthHandler) Logout(c *gin.Context) {
	var req refreshRequest
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.Logout(c.Request.Context(), req.RefreshToken); err != nil {
		httperr.WriteError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *AuthHandler) Me(c *gin.Context) {
	user, err := h.svc.UserByID(c.Request.Context(), middleware.MustUserID(c))
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, toUserDTO(user))
}

func toSessionDTO(s *auth.Session) sessionDTO {
	return sessionDTO{
		User:         toUserDTO(s.User),
		AccessToken:  s.AccessToken,
		TokenType:    "Bearer",
		ExpiresAt:    s.ExpiresAt.UTC().Format(timeFormat),
		RefreshToken: s.RefreshToken,
	}
}

func toUserDTO(u *domain.User) userDTO {
	return userDTO{
		ID:          u.ID.String(),
		Email:       u.Email,
		DisplayName: u.DisplayName,
		CreatedAt:   u.CreatedAt.UTC().Format(timeFormat),
	}
}

func clientInfo(c *gin.Context) auth.ClientInfo {
	return auth.ClientInfo{UserAgent: c.Request.UserAgent(), IP: c.ClientIP()}
}
