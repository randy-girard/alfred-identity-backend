package crypto

import (
	"bytes"
	"testing"
)

func TestDeriveKeySeparatesPurposes(t *testing.T) {
	master := bytes.Repeat([]byte{0x42}, 32)
	aead, err := DeriveKey(master, "aead")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := DeriveKey(master, "web-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(aead) != 32 || len(sess) != 32 {
		t.Fatalf("len aead=%d sess=%d", len(aead), len(sess))
	}
	if bytes.Equal(aead, sess) {
		t.Fatal("derived keys for different info must differ")
	}
	again, err := DeriveKey(master, "web-session")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sess, again) {
		t.Fatal("HKDF must be deterministic")
	}
}
