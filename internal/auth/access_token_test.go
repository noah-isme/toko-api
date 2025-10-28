package auth

import (
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

func TestServiceParseAccessTokenSuccess(t *testing.T) {
	queries := newFakeQueries()
	svc, err := NewService(Config{
		Queries:         queries,
		Secret:          "super-secret-key",
		AccessTokenTTL:  time.Minute,
		RefreshTokenTTL: time.Hour,
		ResetTokenTTL:   time.Hour,
		Issuer:          "backend-toko",
		Audience:        "toko-frontend",
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	fixed := time.Now()
	svc.WithNow(func() time.Time { return fixed })

	token, _, err := svc.signAccessToken("user-id")
	if err != nil {
		t.Fatalf("sign access token: %v", err)
	}
	subject, err := svc.ParseAccessToken(token)
	if err != nil {
		t.Fatalf("parse access token: %v", err)
	}
	if subject != "user-id" {
		t.Fatalf("unexpected subject: %s", subject)
	}
}

func TestServiceParseAccessTokenRejectsAlgorithmMismatch(t *testing.T) {
	queries := newFakeQueries()
	svc, err := NewService(Config{
		Queries:         queries,
		Secret:          "super-secret-key",
		AccessTokenTTL:  time.Minute,
		RefreshTokenTTL: time.Hour,
		ResetTokenTTL:   time.Hour,
		Issuer:          "backend-toko",
		Audience:        "toko-frontend",
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	fixed := time.Now()
	svc.WithNow(func() time.Time { return fixed })

	built, err := jwt.NewBuilder().
		Subject("user-id").
		Issuer(svc.issuer).
		Audience([]string{svc.audience}).
		IssuedAt(fixed).
		NotBefore(fixed.Add(-svc.clockSkew)).
		Expiration(fixed.Add(svc.accessTTL)).
		Build()
	if err != nil {
		t.Fatalf("build token: %v", err)
	}
	signed, err := jwt.Sign(built, jwt.WithKey(jwa.HS384, svc.secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	if _, err := svc.ParseAccessToken(string(signed)); err == nil {
		t.Fatal("expected algorithm mismatch error")
	}
}
