package auth

import (
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

func TestTokenValidatorValidateSuccess(t *testing.T) {
	now := time.Now()
	token, err := jwt.NewBuilder().
		Issuer("issuer").
		Audience([]string{"aud"}).
		Subject("sub").
		IssuedAt(now).
		NotBefore(now).
		Expiration(now.Add(time.Minute)).
		Build()
	if err != nil {
		t.Fatalf("build token: %v", err)
	}

	validator := TokenValidator{Issuer: "issuer", Audience: "aud", ClockSkew: time.Second, Algorithm: jwa.HS256}
	if err := validator.Validate(token, jwa.HS256, now); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestTokenValidatorIssuerMismatch(t *testing.T) {
	now := time.Now()
	token, _ := jwt.NewBuilder().
		Issuer("other").
		Audience([]string{"aud"}).
		Subject("sub").
		IssuedAt(now).
		NotBefore(now).
		Expiration(now.Add(time.Minute)).
		Build()

	validator := TokenValidator{Issuer: "issuer", Audience: "aud", Algorithm: jwa.HS256}
	if err := validator.Validate(token, jwa.HS256, now); err == nil {
		t.Fatal("expected issuer mismatch error")
	}
}

func TestTokenValidatorExpiry(t *testing.T) {
	now := time.Now()
	token, _ := jwt.NewBuilder().
		Issuer("issuer").
		Audience([]string{"aud"}).
		Subject("sub").
		IssuedAt(now.Add(-2 * time.Hour)).
		NotBefore(now.Add(-2 * time.Hour)).
		Expiration(now.Add(-time.Minute)).
		Build()

	validator := TokenValidator{Issuer: "issuer", Audience: "aud", Algorithm: jwa.HS256}
	if err := validator.Validate(token, jwa.HS256, now); err == nil {
		t.Fatal("expected expiration error")
	}
}

func TestTokenValidatorNotBefore(t *testing.T) {
	now := time.Now()
	token, _ := jwt.NewBuilder().
		Issuer("issuer").
		Audience([]string{"aud"}).
		Subject("sub").
		IssuedAt(now).
		NotBefore(now.Add(5 * time.Minute)).
		Expiration(now.Add(10 * time.Minute)).
		Build()
	validator := TokenValidator{Issuer: "issuer", Audience: "aud", Algorithm: jwa.HS256, ClockSkew: time.Second}
	if err := validator.Validate(token, jwa.HS256, now); err == nil {
		t.Fatal("expected not-before validation error")
	}
}

func TestTokenValidatorAlgorithmMismatch(t *testing.T) {
	now := time.Now()
	token, _ := jwt.NewBuilder().
		Issuer("issuer").
		Audience([]string{"aud"}).
		Subject("sub").
		IssuedAt(now).
		Expiration(now.Add(time.Minute)).
		Build()
	validator := TokenValidator{Issuer: "issuer", Audience: "aud", Algorithm: jwa.HS256}
	if err := validator.Validate(token, jwa.RS256, now); err == nil {
		t.Fatal("expected algorithm mismatch error")
	}
}
