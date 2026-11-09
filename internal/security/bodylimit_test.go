package security

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBodyLimitAllowsWithinLimit(t *testing.T) {
	limiter := BodyLimit{Max: 10}
	var captured string
	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}
		captured = string(data)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/payload", strings.NewReader("hello"))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if captured != "hello" {
		t.Fatalf("expected body to pass through, got %q", captured)
	}
}

func TestBodyLimitRejectsOversized(t *testing.T) {
	limiter := BodyLimit{Max: 5}
	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/payload", strings.NewReader("excessive"))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rr.Code)
	}
}

func TestBodyLimitRejectsContentLength(t *testing.T) {
	limiter := BodyLimit{Max: 5}
	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/payload", strings.NewReader("content"))
	req.ContentLength = 100
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for declared oversized body, got %d", rr.Code)
	}
}

func TestBodyLimitRejectsWithJSONEnvelope(t *testing.T) {
	limiter := BodyLimit{Max: 5}
	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run for an oversized body")
	}))

	req := httptest.NewRequest(http.MethodPost, "/payload", strings.NewReader("excessive"))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected JSON content type, got %q", ct)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rr.Body.String(), err)
	}
	if body.Error.Code != "PAYLOAD_TOO_LARGE" || body.Error.Message == "" {
		t.Fatalf("unexpected error body: %+v", body)
	}
}

func TestBodyLimitOverrideAllowsLargerBodyUnbuffered(t *testing.T) {
	limiter := BodyLimit{Max: 5, Overrides: []BodyLimitOverride{{Method: http.MethodPost, Path: "/upload", Max: 50}}}
	var got int
	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}
		got = len(data)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader(strings.Repeat("x", 40)))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || got != 40 {
		t.Fatalf("expected 200 with 40 bytes, got %d with %d bytes", rr.Code, got)
	}
}

func TestBodyLimitOverrideStillCapsStreamedBody(t *testing.T) {
	limiter := BodyLimit{Max: 5, Overrides: []BodyLimitOverride{{Method: http.MethodPost, Path: "/upload", Max: 10}}}
	var readErr error
	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader(strings.Repeat("x", 20)))
	req.ContentLength = -1 // unknown length: only the MaxBytesReader can catch it
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	var maxErr *http.MaxBytesError
	if !errors.As(readErr, &maxErr) {
		t.Fatalf("expected MaxBytesError from the handler read, got %v", readErr)
	}
}

func TestBodyLimitOverrideRejectsDeclaredLength(t *testing.T) {
	limiter := BodyLimit{Max: 5, Overrides: []BodyLimitOverride{{Method: http.MethodPost, Path: "/upload", Max: 10}}}
	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run")
	}))

	req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("x"))
	req.ContentLength = 11
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rr.Code)
	}
}

func TestBodyLimitOverrideMatchesMethodAndPathExactly(t *testing.T) {
	limiter := BodyLimit{Max: 5, Overrides: []BodyLimitOverride{{Method: http.MethodPost, Path: "/upload", Max: 50}}}
	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, tc := range []struct{ method, path string }{
		{http.MethodPut, "/upload"},
		{http.MethodPost, "/upload/"},
		{http.MethodPost, "/upload/other"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(strings.Repeat("x", 20)))
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("%s %s: expected global 413, got %d", tc.method, tc.path, rr.Code)
		}
	}
}
