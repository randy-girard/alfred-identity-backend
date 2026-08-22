package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
)

// AEAD seals plaintext with AES-256-GCM. Output: nonce || ciphertext||tag.
type AEAD struct {
	gcm cipher.AEAD
}

func NewAEAD(key []byte) (*AEAD, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &AEAD{gcm: gcm}, nil
}

func (a *AEAD) Seal(plaintext []byte, aad string) ([]byte, error) {
	nonce := make([]byte, a.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	out := a.gcm.Seal(nonce, nonce, plaintext, []byte(aad))
	return out, nil
}

func (a *AEAD) Open(blob []byte, aad string) ([]byte, error) {
	ns := a.gcm.NonceSize()
	if len(blob) < ns {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ct := blob[:ns], blob[ns:]
	return a.gcm.Open(nil, nonce, ct, []byte(aad))
}

func (a *AEAD) SealString(s, aad string) ([]byte, error) {
	return a.Seal([]byte(s), aad)
}

func (a *AEAD) OpenString(blob []byte, aad string) (string, error) {
	b, err := a.Open(blob, aad)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// MustRandToken returns a high-entropy URL-safe token (raw bytes as hex-ish base).
func MustRandToken(n int) string {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		panic(err)
	}
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	out := make([]byte, n)
	for i := range b {
		out[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(out)
}

func U64BE(n uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, n)
	return b
}
