package checkout

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestWriteErrorMapsLockConflictsToRetryable409(t *testing.T) {
	for _, code := range []string{"40P01", "55P03", "40001"} {
		t.Run(code, func(t *testing.T) {
			rec := httptest.NewRecorder()
			err := fmt.Errorf("reserve stock: %w", &pgconn.PgError{Code: code, Message: "deadlock detected"})
			(&Handler{}).writeError(rec, err)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409", rec.Code)
			}
			body := rec.Body.String()
			if strings.Contains(body, "SQLSTATE") || strings.Contains(body, "deadlock") {
				t.Fatalf("body leaks driver error: %s", body)
			}
			var resp struct {
				Error struct {
					Code    string         `json:"code"`
					Details map[string]any `json:"details"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v (%s)", err, body)
			}
			if resp.Error.Code != "CONFLICT" || resp.Error.Details["retryable"] != true {
				t.Fatalf("unexpected body: %s", body)
			}
		})
	}
}

func TestWriteErrorKeepsOtherErrorsAs400(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Handler{}).writeError(rec, errors.New("insufficient stock for Shirt"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	rec = httptest.NewRecorder()
	(&Handler{}).writeError(rec, &pgconn.PgError{Code: "23505"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unique violation status = %d, want 400", rec.Code)
	}
}
