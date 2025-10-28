package common

import (
	"crypto/sha256"
	"encoding/hex"
)

// Sha256Hex returns the SHA-256 digest of the input encoded as lowercase hex.
func Sha256Hex(input string) string {
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}
