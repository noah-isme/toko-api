package auth

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/noah-isme/backend-toko/internal/common"
)

// A duplicate email surfaces from pgx v5 as a *pgconn.PgError with SQLSTATE
// 23505; Register must map it to 409 EMAIL_ALREADY_USED, not a 500.
func TestRegisterDuplicateEmailReturnsConflict(t *testing.T) {
	svc, err := NewService(Config{
		Queries:         newFakeQueries(),
		Secret:          "test-secret",
		AccessTokenTTL:  time.Minute,
		RefreshTokenTTL: time.Hour,
		ResetTokenTTL:   time.Hour,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := context.Background()
	if _, err := svc.Register(ctx, "Ana", "ana@example.com", "hunter2!!"); err != nil {
		t.Fatalf("first register: %v", err)
	}
	_, err = svc.Register(ctx, "Ana Lagi", " ANA@example.com", "hunter3!!")
	var appErr *common.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("second register: want *common.AppError, got %T: %v", err, err)
	}
	if appErr.Code != "EMAIL_ALREADY_USED" || appErr.HTTPStatus != http.StatusConflict {
		t.Fatalf("second register: got %s/%d, want EMAIL_ALREADY_USED/409", appErr.Code, appErr.HTTPStatus)
	}
}
