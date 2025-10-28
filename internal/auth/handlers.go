package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/noah-isme/backend-toko/internal/common"
)

// Handler exposes HTTP handlers for authentication and account endpoints.
type Handler struct {
	Service           *Service
	AccessCookieName  string
	RefreshCookieName string
	CookieDomain      string
	CookieSecure      bool
	CookieSameSite    http.SameSite
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

type forgotPasswordRequest struct {
	Email string `json:"email"`
}

type resetPasswordRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
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
	result, err := h.Service.Login(r.Context(), req.Email, req.Password, r.UserAgent(), clientIP(r))
	if err != nil {
		h.writeError(w, err)
		return
	}
	h.setAuthCookies(w, result)
	common.JSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"user":                    result.User,
			"access_token":            result.AccessToken,
			"access_token_expires_at": result.AccessExpiry,
		},
	})
}

// Logout handles POST /api/v1/auth/logout.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "auth service not configured", nil)
		return
	}
	refreshToken := h.refreshTokenFromRequest(r)
	if refreshToken != "" {
		_ = h.Service.Logout(r.Context(), refreshToken)
	}
	h.clearAuthCookies(w)
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

// ForgotPassword handles POST /api/v1/auth/password/forgot.
func (h *Handler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "auth service not configured", nil)
		return
	}
	var req forgotPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request payload", nil)
		return
	}
	result, err := h.Service.InitiatePasswordReset(r.Context(), req.Email)
	if err != nil {
		h.writeError(w, err)
		return
	}
	response := map[string]any{"data": map[string]any{"message": "if the email exists, a reset link has been sent"}}
	if result.Token != "" {
		response["meta"] = map[string]any{"reset_token": result.Token, "expires_at": result.ExpiresAt}
	}
	common.JSON(w, http.StatusAccepted, response)
}

// ResetPassword handles POST /api/v1/auth/password/reset.
func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "auth service not configured", nil)
		return
	}
	var req resetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request payload", nil)
		return
	}
	if strings.TrimSpace(req.Token) == "" {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "token is required", nil)
		return
	}
	if err := h.Service.ResetPassword(r.Context(), req.Token, req.Password); err != nil {
		h.writeError(w, err)
		return
	}
	h.clearAuthCookies(w)
	common.JSON(w, http.StatusOK, map[string]any{"data": map[string]any{"message": "password updated"}})
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

func (h *Handler) setAuthCookies(w http.ResponseWriter, result LoginResult) {
	if h.AccessCookieName != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     h.AccessCookieName,
			Value:    result.AccessToken,
			Domain:   h.CookieDomain,
			Path:     "/",
			Expires:  result.AccessExpiry,
			HttpOnly: true,
			Secure:   h.CookieSecure,
			SameSite: h.CookieSameSite,
		})
	}
	if h.RefreshCookieName != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     h.RefreshCookieName,
			Value:    result.RefreshToken,
			Domain:   h.CookieDomain,
			Path:     "/",
			Expires:  result.RefreshExpiry,
			HttpOnly: true,
			Secure:   h.CookieSecure,
			SameSite: h.CookieSameSite,
		})
	}
}

func (h *Handler) clearAuthCookies(w http.ResponseWriter) {
	if h.AccessCookieName != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     h.AccessCookieName,
			Value:    "",
			Domain:   h.CookieDomain,
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   h.CookieSecure,
			SameSite: h.CookieSameSite,
		})
	}
	if h.RefreshCookieName != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     h.RefreshCookieName,
			Value:    "",
			Domain:   h.CookieDomain,
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   h.CookieSecure,
			SameSite: h.CookieSameSite,
		})
	}
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

func clientIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); ip != "" {
		parts := strings.Split(ip, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
		return ip
	}
	if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); ip != "" {
		return ip
	}
	host := strings.TrimSpace(r.RemoteAddr)
	if host == "" {
		return ""
	}
	if colon := strings.LastIndex(host, ":"); colon >= 0 {
		return host[:colon]
	}
	return host
}
