package config

import (
	"bytes"
	"crypto/ecdh"
	"encoding/base64"
	"errors"
	"fmt"
)

// validateVAPIDKeys checks that the configured VAPID key pair is usable before
// the app boots. A bad server key must fail loudly here: at send time it would
// look like a per-subscription failure. Errors never include key material.
//
// Rules: both keys are base64url (padded or unpadded, as `make vapid-keys` and
// web-push tooling print them); the private key is a 32-byte P-256 scalar; the
// public key is a 65-byte uncompressed P-256 point; and the public key is the
// one derived from the private key.
func validateVAPIDKeys(publicKey, privateKey string) error {
	priv, err := decodeBase64URL(privateKey)
	if err != nil {
		return fmt.Errorf("PUSH_VAPID_PRIVATE_KEY %w", err)
	}
	if len(priv) != 32 {
		return fmt.Errorf("PUSH_VAPID_PRIVATE_KEY must decode to 32 bytes, got %d", len(priv))
	}
	pub, err := decodeBase64URL(publicKey)
	if err != nil {
		return fmt.Errorf("PUSH_VAPID_PUBLIC_KEY %w", err)
	}
	if len(pub) != 65 || pub[0] != 0x04 {
		return fmt.Errorf("PUSH_VAPID_PUBLIC_KEY must decode to a 65-byte uncompressed P-256 point, got %d bytes", len(pub))
	}
	if _, err := ecdh.P256().NewPublicKey(pub); err != nil {
		return errors.New("PUSH_VAPID_PUBLIC_KEY is not a valid P-256 point")
	}
	key, err := ecdh.P256().NewPrivateKey(priv)
	if err != nil {
		return errors.New("PUSH_VAPID_PRIVATE_KEY is not a valid P-256 private key")
	}
	if !bytes.Equal(key.PublicKey().Bytes(), pub) {
		return errors.New("PUSH_VAPID_PUBLIC_KEY does not match PUSH_VAPID_PRIVATE_KEY")
	}
	return nil
}

// errBase64URL reports a decode failure without echoing the input.
var errBase64URL = errors.New("is not base64url (use the URL-safe alphabet, no quotes or whitespace)")

func decodeBase64URL(s string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	if b, err := base64.URLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return nil, errBase64URL
}
