package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// HashToken stores only a SHA-256 hex digest of the raw API token.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// BlindIndex returns HMAC-SHA256 hex of normalized value (for username lookup).
func BlindIndex(key []byte, normalized string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(normalized))
	return hex.EncodeToString(mac.Sum(nil))
}
