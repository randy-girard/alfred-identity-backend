package crypto

import "testing"

func TestDESGolden(t *testing.T) {
	ct, err := PackCredentials("user", "pass")
	if err != nil {
		t.Fatal(err)
	}
	if hexEncode(ct) != GoldenUserPassCipher {
		t.Fatalf("got %s want %s", hexEncode(ct), GoldenUserPassCipher)
	}
	pt, err := DecryptDES(ct)
	if err != nil {
		t.Fatal(err)
	}
	if len(pt) < 10 || string(pt[:10]) != "user\x00pass\x00" {
		t.Fatalf("unexpected plaintext %q", pt)
	}
}

func hexEncode(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hexdigits[v>>4]
		out[i*2+1] = hexdigits[v&0x0f]
	}
	return string(out)
}

func trimZeros(b []byte) []byte {
	i := len(b)
	for i > 0 && b[i-1] == 0 {
		i--
	}
	return b[:i]
}

func TestAEADRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	a, err := NewAEAD(key)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := a.SealString("secret", "eq_accounts.password")
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.OpenString(sealed, "eq_accounts.password")
	if err != nil || got != "secret" {
		t.Fatalf("got %q err %v", got, err)
	}
	if _, err := a.OpenString(sealed, "wrong"); err == nil {
		t.Fatal("expected aad mismatch")
	}
	if _, err := NewAEAD([]byte("short")); err == nil {
		t.Fatal("expected short key error")
	}
	if _, err := a.Open(sealed[:4], "eq_accounts.password"); err == nil {
		t.Fatal("expected short ciphertext error")
	}
}

func TestHashTokenAndBlindIndex(t *testing.T) {
	h1 := HashToken("tok")
	h2 := HashToken("tok")
	if h1 != h2 || len(h1) != 64 {
		t.Fatalf("hash %q", h1)
	}
	if HashToken("other") == h1 {
		t.Fatal("expected different hash")
	}
	key := []byte("0123456789abcdef0123456789abcdef")
	b1 := BlindIndex(key, "user")
	b2 := BlindIndex(key, "user")
	if b1 != b2 || len(b1) != 64 {
		t.Fatalf("blind %q", b1)
	}
	if BlindIndex(key, "other") == b1 {
		t.Fatal("expected different blind index")
	}
}

func TestMustRandTokenAndU64BE(t *testing.T) {
	tok := MustRandToken(24)
	if len(tok) != 24 {
		t.Fatalf("len %d", len(tok))
	}
	b := U64BE(0x0102030405060708)
	if len(b) != 8 || b[0] != 1 || b[7] != 8 {
		t.Fatalf("%v", b)
	}
	gb := GoldenCipherBytes()
	if len(gb) != 16 {
		t.Fatalf("golden len %d", len(gb))
	}
}

func TestDecryptDESBadLength(t *testing.T) {
	if _, err := DecryptDES(nil); err == nil {
		t.Fatal("nil")
	}
	if _, err := DecryptDES([]byte{}); err == nil {
		t.Fatal("empty")
	}
	if _, err := DecryptDES([]byte{1, 2, 3}); err == nil {
		t.Fatal("short")
	}
	// exact block size round-trip (no extra pad)
	pt := []byte("12345678")
	ct, err := EncryptDES(pt)
	if err != nil || len(ct) != 8 {
		t.Fatalf("%v len=%d", err, len(ct))
	}
	got, err := DecryptDES(ct)
	if err != nil || string(got) != string(pt) {
		t.Fatalf("%q err=%v", got, err)
	}
}
