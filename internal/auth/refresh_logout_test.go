package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/google/uuid"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

type loginResponse struct {
	Data struct {
		AccessToken string `json:"accessToken"`
	} `json:"data"`
}

type refreshTokenResponse struct {
	AccessToken string `json:"accessToken"`
}

func TestRefreshRotateAndLogout(t *testing.T) {
	queries := newFakeQueries()
	userID := uuid.New()
	pgID, _ := pgUUIDFromString(userID.String())
	hash, err := argon2id.CreateHash("password123", argon2id.DefaultParams)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	now := time.Now()
	user := dbgen.User{
		ID:           pgID,
		Name:         "Test User",
		Email:        "user@example.com",
		PasswordHash: hash,
		Roles:        []string{"user"},
		CreatedAt:    pgTimestamp(now),
		UpdatedAt:    pgTimestamp(now),
	}
	queries.usersByEmail["user@example.com"] = user
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
		Mailer:                &common.InMemoryEmail{},
		RefreshCookieName:     "rt",
		RefreshCookieSecure:   false,
		RefreshCookieSameSite: http.SameSiteLaxMode,
	}

	// Login to obtain refresh cookie.
	loginBody := bytes.NewBufferString(`{"email":"user@example.com","password":"password123"}`)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", loginBody)
	loginRec := httptest.NewRecorder()
	handler.Login(loginRec, loginReq)
	loginRes := loginRec.Result()
	if loginRes.StatusCode != http.StatusOK {
		t.Fatalf("unexpected login status: %d", loginRes.StatusCode)
	}
	var loginPayload loginResponse
	if err := json.NewDecoder(loginRes.Body).Decode(&loginPayload); err != nil {
		t.Fatalf("decode login payload: %v", err)
	}
	_ = loginRes.Body.Close()
	if loginPayload.Data.AccessToken == "" {
		t.Fatalf("expected access token in login response")
	}

	cookie := findCookie(loginRes.Cookies(), "rt")
	if cookie == nil {
		t.Fatalf("expected refresh cookie after login")
	}
	originalRefresh := cookie.Value
	originalHashed := hashRefreshToken(originalRefresh)
	if _, ok := queries.sessionsByToken[originalHashed]; !ok {
		t.Fatalf("expected session stored for initial refresh token")
	}

	// Perform refresh to rotate token.
	refreshReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	refreshReq.AddCookie(cookie)
	refreshRec := httptest.NewRecorder()
	handler.Refresh(refreshRec, refreshReq)
	refreshRes := refreshRec.Result()
	if refreshRes.StatusCode != http.StatusOK {
		t.Fatalf("unexpected refresh status: %d", refreshRes.StatusCode)
	}
	var refreshPayload refreshTokenResponse
	if err := json.NewDecoder(refreshRes.Body).Decode(&refreshPayload); err != nil {
		t.Fatalf("decode refresh payload: %v", err)
	}
	_ = refreshRes.Body.Close()
	if refreshPayload.AccessToken == "" {
		t.Fatalf("expected access token in refresh response")
	}
	rotatedCookie := findCookie(refreshRes.Cookies(), "rt")
	if rotatedCookie == nil {
		t.Fatalf("expected rotated refresh cookie")
	}
	if rotatedCookie.Value == originalRefresh {
		t.Fatalf("expected refresh token rotation")
	}
	rotatedHashed := hashRefreshToken(rotatedCookie.Value)
	if _, ok := queries.sessionsByToken[rotatedHashed]; !ok {
		t.Fatalf("expected session stored for rotated token")
	}
	if _, ok := queries.sessionsByToken[originalHashed]; ok {
		t.Fatalf("expected old session removed after rotation")
	}

	// Attempt reuse of old refresh token should fail.
	reuseReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	reuseReq.AddCookie(&http.Cookie{Name: "rt", Value: originalRefresh})
	reuseRec := httptest.NewRecorder()
	handler.Refresh(reuseRec, reuseReq)
	reuseRes := reuseRec.Result()
	if reuseRes.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized on token reuse, got %d", reuseRes.StatusCode)
	}
	_ = reuseRes.Body.Close()

	// Logout should revoke session and clear cookie.
	logoutReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logoutReq.AddCookie(rotatedCookie)
	logoutRec := httptest.NewRecorder()
	handler.Logout(logoutRec, logoutReq)
	logoutRes := logoutRec.Result()
	if logoutRes.StatusCode != http.StatusNoContent {
		t.Fatalf("unexpected logout status: %d", logoutRes.StatusCode)
	}
	clearedCookie := findCookie(logoutRes.Cookies(), "rt")
	if clearedCookie == nil {
		t.Fatalf("expected cookie clearing on logout")
	}
	if clearedCookie.MaxAge != -1 {
		t.Fatalf("expected logout cookie MaxAge -1, got %d", clearedCookie.MaxAge)
	}
	if _, ok := queries.sessionsByToken[rotatedHashed]; ok {
		t.Fatalf("expected session removed after logout")
	}
}

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, c := range cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}
