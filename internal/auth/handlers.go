package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/noah-isme/backend-toko/internal/common"
)

const defaultRefreshCookiePath = "/api/v1/auth"

// Handler exposes HTTP handlers for authentication and account endpoints.
type Handler struct {
	Service *Service
	Mailer  common.EmailSender

	RefreshCookieName     string
	RefreshCookieDomain   string
	RefreshCookieSecure   bool
	RefreshCookieSameSite http.SameSite
	RefreshCookiePath     string

	PublicBaseURL string
}

type registerRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type forgotRequest struct {
	Email string `json:"email"`
}

type resetRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"newPassword"`
}

type refreshResponse struct {
	AccessToken string `json:"accessToken"`
}

// Register handles POST /api/v1/auth/register.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "auth service not configured", nil)
		return
	}
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request payload", nil)
		return
	}
	user, err := h.Service.Register(r.Context(), req.Name, req.Email, req.Password)
	if err != nil {
		h.writeError(w, err)
		return
	}
	common.JSON(w, http.StatusCreated, map[string]any{"data": user})
}

// Login handles POST /api/v1/auth/login.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "auth service not configured", nil)
		return
	}
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request payload", nil)
		return
	}
	result, err := h.Service.Login(r.Context(), req.Email, req.Password, r.UserAgent(), common.ClientIP(r))
	if err != nil {
		h.writeError(w, err)
		return
	}
	h.setRefreshCookie(w, result.RefreshToken, result.RefreshExpiry)
	common.JSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"user":                  result.User,
			"accessToken":           result.AccessToken,
			"accessTokenExpiresAt":  result.AccessExpiry,
			"refreshTokenExpiresAt": result.RefreshExpiry,
		},
	})
}

// Refresh handles POST /api/v1/auth/refresh.
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "auth service not configured", nil)
		return
	}
	token := h.refreshTokenFromRequest(r)
	if token == "" {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing refresh cookie", nil)
		return
	}
	result, err := h.Service.Refresh(r.Context(), token)
	if err != nil {
		h.writeError(w, err)
		return
	}
	h.setRefreshCookie(w, result.RefreshToken, result.RefreshExpiry)
	common.JSON(w, http.StatusOK, refreshResponse{AccessToken: result.AccessToken})
}

// Logout handles POST /api/v1/auth/logout.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "auth service not configured", nil)
		return
	}
	if token := h.refreshTokenFromRequest(r); token != "" {
		_ = h.Service.Logout(r.Context(), token)
	}
	h.clearRefreshCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// Me handles GET /api/v1/auth/me.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "auth service not configured", nil)
		return
	}
	userID, ok := common.UserID(r.Context())
	if !ok {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid token", nil)
		return
	}
	user, err := h.Service.Me(r.Context(), userID)
	if err != nil {
		h.writeError(w, err)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": user})
}

// Forgot handles POST /api/v1/auth/password/forgot.
func (h *Handler) Forgot(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "auth service not configured", nil)
		return
	}
	var req forgotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request payload", nil)
		return
	}
	if err := h.Service.Forgot(r.Context(), req.Email, h.PublicBaseURL, h.Mailer); err != nil {
		h.writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Reset handles POST /api/v1/auth/password/reset.
func (h *Handler) Reset(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "auth service not configured", nil)
		return
	}
	var req resetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request payload", nil)
		return
	}
	if err := h.Service.Reset(r.Context(), req.Token, req.NewPassword); err != nil {
		h.writeError(w, err)
		return
	}
	h.clearRefreshCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) writeError(w http.ResponseWriter, err error) {
	var appErr *common.AppError
	if errors.As(err, &appErr) {
		status := appErr.HTTPStatus
		if status == 0 {
			status = http.StatusInternalServerError
		}
		if appErr.Code == "" {
			appErr.Code = "INTERNAL"
		}
		message := appErr.Message
		if message == "" {
			message = "internal error"
		}
		common.JSONError(w, status, appErr.Code, message, appErr.Details)
		return
	}
	common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "internal error", nil)
}

func (h *Handler) setRefreshCookie(w http.ResponseWriter, token string, expires time.Time) {
	if h.RefreshCookieName == "" {
		return
	}
	path := h.RefreshCookiePath
	if strings.TrimSpace(path) == "" {
		path = defaultRefreshCookiePath
	}
	http.SetCookie(w, &http.Cookie{
		Name:     h.RefreshCookieName,
		Value:    token,
		Domain:   h.RefreshCookieDomain,
		Path:     path,
		Expires:  expires,
		HttpOnly: true,
		Secure:   h.RefreshCookieSecure,
		SameSite: h.RefreshCookieSameSite,
	})
}

func (h *Handler) clearRefreshCookie(w http.ResponseWriter) {
	if h.RefreshCookieName == "" {
		return
	}
	path := h.RefreshCookiePath
	if strings.TrimSpace(path) == "" {
		path = defaultRefreshCookiePath
	}
	http.SetCookie(w, &http.Cookie{
		Name:     h.RefreshCookieName,
		Value:    "",
		Domain:   h.RefreshCookieDomain,
		Path:     path,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		Secure:   h.RefreshCookieSecure,
		SameSite: h.RefreshCookieSameSite,
	})
}

func (h *Handler) refreshTokenFromRequest(r *http.Request) string {
	if h.RefreshCookieName == "" {
		return ""
	}
	if cookie, err := r.Cookie(h.RefreshCookieName); err == nil {
		return strings.TrimSpace(cookie.Value)
	}
	return ""
}
