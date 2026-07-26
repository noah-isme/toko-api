package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// seedUser registers a user directly in the fake store and returns its id.
func seedUser(t *testing.T, queries *fakeQueries, email string) string {
	t.Helper()
	userID := uuid.New()
	pgID, _ := pgUUIDFromString(userID.String())
	now := time.Now()
	user := dbgen.User{
		ID:           pgID,
		Name:         "Profile User",
		Email:        email,
		PasswordHash: "unused",
		Roles:        []string{"user"},
		CreatedAt:    pgTimestamp(now),
		UpdatedAt:    pgTimestamp(now),
	}
	queries.usersByEmail[email] = user
	queries.usersByID[userID.String()] = user
	return userID.String()
}

func newTestHandler(t *testing.T, queries *fakeQueries, mailer common.EmailSender) *Handler {
	t.Helper()
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
	return &Handler{
		Service:               svc,
		Mailer:                mailer,
		RefreshCookieName:     "rt",
		RefreshCookieSameSite: http.SameSiteLaxMode,
		PublicBaseURL:         "https://example.com",
	}
}

func decodeUserData(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var wrapper struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, body)
	}
	return wrapper.Data
}

func TestUpdateProfileUpdatesNameAndPhone(t *testing.T) {
	queries := newFakeQueries()
	userID := seedUser(t, queries, "profile@example.com")
	handler := newTestHandler(t, queries, &common.InMemoryEmail{})

	body := bytes.NewBufferString(`{"name":"Budi Santoso","phone":"08123456789"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/users/me", body)
	req = req.WithContext(common.WithUserID(req.Context(), userID))
	rec := httptest.NewRecorder()

	handler.UpdateProfile(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	data := decodeUserData(t, rec.Body.Bytes())
	if data["name"] != "Budi Santoso" {
		t.Fatalf("name not updated: %v", data["name"])
	}
	if data["phone"] != "08123456789" {
		t.Fatalf("phone not updated: %v", data["phone"])
	}
}

func TestUpdateProfileLeavesOmittedFieldsUntouched(t *testing.T) {
	queries := newFakeQueries()
	userID := seedUser(t, queries, "partial@example.com")
	handler := newTestHandler(t, queries, &common.InMemoryEmail{})

	// First set a phone number.
	first := httptest.NewRequest(http.MethodPatch, "/api/v1/users/me",
		bytes.NewBufferString(`{"phone":"0811111111"}`))
	first = first.WithContext(common.WithUserID(first.Context(), userID))
	handler.UpdateProfile(httptest.NewRecorder(), first)

	// Then change only the name; the phone must survive.
	second := httptest.NewRequest(http.MethodPatch, "/api/v1/users/me",
		bytes.NewBufferString(`{"name":"Only Name"}`))
	second = second.WithContext(common.WithUserID(second.Context(), userID))
	rec := httptest.NewRecorder()
	handler.UpdateProfile(rec, second)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	data := decodeUserData(t, rec.Body.Bytes())
	if data["name"] != "Only Name" {
		t.Fatalf("name not updated: %v", data["name"])
	}
	if data["phone"] != "0811111111" {
		t.Fatalf("omitted phone should be preserved, got %v", data["phone"])
	}
}

func TestUpdateProfileRejectsEmptyPayload(t *testing.T) {
	queries := newFakeQueries()
	userID := seedUser(t, queries, "empty@example.com")
	handler := newTestHandler(t, queries, &common.InMemoryEmail{})

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/users/me", bytes.NewBufferString(`{}`))
	req = req.WithContext(common.WithUserID(req.Context(), userID))
	rec := httptest.NewRecorder()

	handler.UpdateProfile(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty payload, got %d", rec.Code)
	}
}

func TestUpdateProfileRequiresAuthenticatedUser(t *testing.T) {
	queries := newFakeQueries()
	handler := newTestHandler(t, queries, &common.InMemoryEmail{})

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/users/me",
		bytes.NewBufferString(`{"name":"Nobody"}`))
	rec := httptest.NewRecorder()

	handler.UpdateProfile(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without user context, got %d", rec.Code)
	}
}

func TestEmailVerificationFlow(t *testing.T) {
	queries := newFakeQueries()
	mailer := &common.InMemoryEmail{}
	userID := seedUser(t, queries, "verify@example.com")
	handler := newTestHandler(t, queries, mailer)

	// Request a verification email.
	resendReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/email/resend",
		bytes.NewBufferString(`{"email":"verify@example.com"}`))
	resendRec := httptest.NewRecorder()
	handler.ResendVerification(resendRec, resendReq)

	if resendRec.Code != http.StatusOK {
		t.Fatalf("unexpected resend status: %d body=%s", resendRec.Code, resendRec.Body.String())
	}
	if len(mailer.Outbox) != 1 {
		t.Fatalf("expected one verification email, got %d", len(mailer.Outbox))
	}
	token := extractTokenFromEmail(mailer.Outbox[0].HTML)
	if token == "" {
		t.Fatalf("expected verification token in email body")
	}

	// Consume the token.
	payload, _ := json.Marshal(map[string]string{"token": token})
	verifyRec := httptest.NewRecorder()
	handler.VerifyEmail(verifyRec, httptest.NewRequest(http.MethodPost,
		"/api/v1/auth/email/verify", bytes.NewBuffer(payload)))

	if verifyRec.Code != http.StatusOK {
		t.Fatalf("unexpected verify status: %d body=%s", verifyRec.Code, verifyRec.Body.String())
	}

	// The user must now read back as verified.
	meUser, err := handler.Service.Me(common.WithUserID(t.Context(), userID), userID)
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	if !meUser.EmailVerified {
		t.Fatalf("expected emailVerified to be true after verification")
	}

	// The same token must not work twice.
	replayRec := httptest.NewRecorder()
	handler.VerifyEmail(replayRec, httptest.NewRequest(http.MethodPost,
		"/api/v1/auth/email/verify", bytes.NewBuffer(payload)))
	if replayRec.Code != http.StatusBadRequest {
		t.Fatalf("expected replayed token to be rejected, got %d", replayRec.Code)
	}
}

func TestVerifyEmailRejectsUnknownToken(t *testing.T) {
	queries := newFakeQueries()
	handler := newTestHandler(t, queries, &common.InMemoryEmail{})

	rec := httptest.NewRecorder()
	handler.VerifyEmail(rec, httptest.NewRequest(http.MethodPost,
		"/api/v1/auth/email/verify", bytes.NewBufferString(`{"token":"not-a-real-token"}`)))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown token, got %d", rec.Code)
	}
}

func TestResendVerificationHidesUnknownEmails(t *testing.T) {
	queries := newFakeQueries()
	mailer := &common.InMemoryEmail{}
	handler := newTestHandler(t, queries, mailer)

	rec := httptest.NewRecorder()
	handler.ResendVerification(rec, httptest.NewRequest(http.MethodPost,
		"/api/v1/auth/email/resend", bytes.NewBufferString(`{"email":"ghost@example.com"}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 to avoid disclosing account existence, got %d", rec.Code)
	}
	if len(mailer.Outbox) != 0 {
		t.Fatalf("expected no email for unknown address, got %d", len(mailer.Outbox))
	}
}

func TestResendVerificationSkipsAlreadyVerifiedUsers(t *testing.T) {
	queries := newFakeQueries()
	mailer := &common.InMemoryEmail{}
	userID := seedUser(t, queries, "done@example.com")

	// Mark the seeded user verified up front.
	pgID, _ := pgUUIDFromString(userID)
	if _, err := queries.MarkUserEmailVerified(t.Context(), pgID); err != nil {
		t.Fatalf("mark verified: %v", err)
	}

	handler := newTestHandler(t, queries, mailer)
	rec := httptest.NewRecorder()
	handler.ResendVerification(rec, httptest.NewRequest(http.MethodPost,
		"/api/v1/auth/email/resend", bytes.NewBufferString(`{"email":"done@example.com"}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if len(mailer.Outbox) != 0 {
		t.Fatalf("expected no email for already-verified user, got %d", len(mailer.Outbox))
	}
}
