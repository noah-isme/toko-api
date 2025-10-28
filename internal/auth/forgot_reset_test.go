package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/google/uuid"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

func TestForgotResetFlow(t *testing.T) {
	queries := newFakeQueries()
	mailer := &common.InMemoryEmail{}
	userID := uuid.New()
	pgID, _ := pgUUIDFromString(userID.String())
	hash, err := argon2id.CreateHash("hunter2!!", argon2id.DefaultParams)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	now := time.Now()
	user := dbgen.User{
		ID:           pgID,
		Name:         "Reset User",
		Email:        "reset@example.com",
		PasswordHash: hash,
		Roles:        []string{"user"},
		CreatedAt:    pgTimestamp(now),
		UpdatedAt:    pgTimestamp(now),
	}
	queries.usersByEmail["reset@example.com"] = user
	queries.usersByID[userID.String()] = user

	svc, err := NewService(Config{
		Queries:         queries,
		Secret:          "test-secret",
		AccessTokenTTL:  time.Minute,
		RefreshTokenTTL: time.Hour,
		ResetTokenTTL:   time.Hour,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	handler := &Handler{
		Service:               svc,
		Mailer:                mailer,
		RefreshCookieName:     "rt",
		RefreshCookieSecure:   false,
		RefreshCookieSameSite: http.SameSiteLaxMode,
		PublicBaseURL:         "https://example.com",
	}

	// Seed a session that should be revoked after password reset.
	loginBody := bytes.NewBufferString(`{"email":"reset@example.com","password":"hunter2!!"}`)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", loginBody)
	loginRec := httptest.NewRecorder()
	handler.Login(loginRec, loginReq)
	loginRes := loginRec.Result()
	if loginRes.StatusCode != http.StatusOK {
		t.Fatalf("unexpected login status: %d", loginRes.StatusCode)
	}
	initialCookie := findCookie(loginRes.Cookies(), "rt")
	if initialCookie == nil {
		t.Fatalf("expected refresh cookie from login")
	}
	_ = loginRes.Body.Close()
	if len(queries.sessionsByToken) == 0 {
		t.Fatalf("expected session created during login")
	}

	// Trigger forgot password.
	forgotBody := bytes.NewBufferString(`{"email":"reset@example.com"}`)
	forgotReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password/forgot", forgotBody)
	forgotRec := httptest.NewRecorder()
	handler.Forgot(forgotRec, forgotReq)
	forgotRes := forgotRec.Result()
	if forgotRes.StatusCode != http.StatusNoContent {
		t.Fatalf("unexpected forgot status: %d", forgotRes.StatusCode)
	}
	_ = forgotRes.Body.Close()
	if len(mailer.Outbox) != 1 {
		t.Fatalf("expected email sent, got %d", len(mailer.Outbox))
	}
	token := extractTokenFromEmail(mailer.Outbox[0].HTML)
	if token == "" {
		t.Fatalf("expected reset token in email body")
	}

	// Complete reset with the token.
	resetPayload := map[string]string{
		"token":       token,
		"newPassword": "newPassw0rd!",
	}
	buf, _ := json.Marshal(resetPayload)
	resetReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password/reset", bytes.NewBuffer(buf))
	resetRec := httptest.NewRecorder()
	handler.Reset(resetRec, resetReq)
	resetRes := resetRec.Result()
	if resetRes.StatusCode != http.StatusNoContent {
		t.Fatalf("unexpected reset status: %d", resetRes.StatusCode)
	}
	_ = resetRes.Body.Close()

	if len(queries.resetsByToken) != 0 {
		t.Fatalf("expected password reset entries cleared")
	}
	if len(queries.sessionsByToken) != 0 {
		t.Fatalf("expected sessions revoked after reset")
	}

	// Token reuse should fail.
	reuseReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password/reset", bytes.NewBuffer(buf))
	reuseRec := httptest.NewRecorder()
	handler.Reset(reuseRec, reuseReq)
	reuseRes := reuseRec.Result()
	if reuseRes.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected bad request on token reuse, got %d", reuseRes.StatusCode)
	}
	_ = reuseRes.Body.Close()

	// Login with new password should succeed.
	mailer.Outbox = nil
	newLoginBody := bytes.NewBufferString(`{"email":"reset@example.com","password":"newPassw0rd!"}`)
	newLoginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", newLoginBody)
	newLoginRec := httptest.NewRecorder()
	handler.Login(newLoginRec, newLoginReq)
	newLoginRes := newLoginRec.Result()
	if newLoginRes.StatusCode != http.StatusOK {
		t.Fatalf("expected successful login with new password, got %d", newLoginRes.StatusCode)
	}
	_ = newLoginRes.Body.Close()
}

func extractTokenFromEmail(body string) string {
	idx := strings.Index(body, "token=")
	if idx == -1 {
		return ""
	}
	token := body[idx+len("token="):]
	if i := strings.Index(token, "&"); i >= 0 {
		token = token[:i]
	}
	if i := strings.Index(token, " "); i >= 0 {
		token = token[:i]
	}
	return strings.TrimSpace(token)
}
