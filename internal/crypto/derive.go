package crypto

import (
	"crypto/sha256"
	"io"

	"golang.org/x/crypto/hkdf"
)

// DeriveKey returns a 32-byte key derived from master via HKDF-SHA256.
// Use distinct info strings to separate key purposes (e.g. "aead", "web-session").
func DeriveKey(master []byte, info string) ([]byte, error) {
	r := hkdf.New(sha256.New, master, nil, []byte(info))
	out := make([]byte, 32)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, err
	}
	return out, nil
}
