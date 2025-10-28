package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

// TokenValidator validates structural and contextual properties of JWT tokens.
type TokenValidator struct {
	Issuer    string
	Audience  string
	ClockSkew time.Duration
	Algorithm jwa.SignatureAlgorithm
}

// Validate ensures the supplied token satisfies issuer, audience, expiry, and algorithm requirements.
func (v TokenValidator) Validate(tok jwt.Token, algorithm jwa.SignatureAlgorithm, now time.Time) error {
	if tok == nil {
		return errors.New("auth: token is nil")
	}

	if algorithm == "" {
		return errors.New("auth: token missing algorithm")
	}
	if v.Algorithm != "" && algorithm != v.Algorithm {
		return fmt.Errorf("auth: unexpected token algorithm %s", algorithm)
	}

	options := []jwt.ValidateOption{
		jwt.WithClock(jwt.ClockFunc(func() time.Time { return now })),
	}
	if v.ClockSkew > 0 {
		options = append(options, jwt.WithAcceptableSkew(v.ClockSkew))
	}
	if v.Issuer != "" {
		options = append(options, jwt.WithIssuer(v.Issuer))
	}
	if v.Audience != "" {
		options = append(options, jwt.WithAudience(v.Audience))
	}

	return jwt.Validate(tok, options...)
}
