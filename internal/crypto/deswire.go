package crypto

import (
	"crypto/des"
	"crypto/cipher"
	"encoding/hex"
	"fmt"
)

// EQ login DES uses a publicly known zero key/IV (wire obfuscation only).
var (
	desKey = make([]byte, 8)
	desIV  = make([]byte, 8)
)

// PackCredentials builds username\0password\0 and DES-CBC encrypts with zero pad.
func PackCredentials(username, password string) ([]byte, error) {
	plain := append([]byte(username), 0)
	plain = append(plain, []byte(password)...)
	plain = append(plain, 0)
	return EncryptDES(plain)
}

// EncryptDES zero-pads to 8 and encrypts with DES-CBC (key/IV all zeros).
func EncryptDES(plain []byte) ([]byte, error) {
	block, err := des.NewCipher(desKey)
	if err != nil {
		return nil, err
	}
	padded := zeroPad(plain, block.BlockSize())
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, desIV).CryptBlocks(out, padded)
	return out, nil
}

// DecryptDES decrypts DES-CBC ciphertext (length must be multiple of 8).
func DecryptDES(ct []byte) ([]byte, error) {
	if len(ct) == 0 || len(ct)%8 != 0 {
		return nil, fmt.Errorf("invalid DES ciphertext length %d", len(ct))
	}
	block, err := des.NewCipher(desKey)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, desIV).CryptBlocks(out, ct)
	return out, nil
}

func zeroPad(b []byte, blockSize int) []byte {
	if len(b)%blockSize == 0 {
		return b
	}
	n := blockSize - (len(b) % blockSize)
	out := make([]byte, len(b)+n)
	copy(out, b)
	return out
}

// GoldenUserPassCipher is the known vector for user\0pass\0 (for tests).
const GoldenUserPassCipher = "575ab3e46810e874f75cb31595902052"

func GoldenCipherBytes() []byte {
	b, _ := hex.DecodeString(GoldenUserPassCipher)
	return b
}
