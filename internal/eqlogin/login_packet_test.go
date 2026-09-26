package eqlogin

import (
	"testing"

	"github.com/alfred-identity/web/internal/crypto"
)

func TestSOECRC32Vectors(t *testing.T) {
	got := SOECRC32([]byte("123456789"), 0)
	if got != 0x22896B0A {
		t.Fatalf("got %08x", got)
	}
	got = SOECRC32([]byte("123456789"), 0x12345678)
	if got != 0xAAD05244 {
		t.Fatalf("got %08x", got)
	}
}

func TestParseLoginPacketAndRewrite(t *testing.T) {
	base := []byte{
		0x00, 0x03,
		0x04, 0x00, 0x15, 0x00, 0x00,
		0x20, 0x00, 0x09, 0x00, 0x01,
		0x02, 0x00,
		0x03, 0x00, 0x00, 0x00,
		0x00,
		0x02,
		0x00, 0x00, 0x00, 0x00,
	}
	base = append(base, crypto.GoldenCipherBytes()...)
	lp, ok := ParseLoginPacket(base)
	if !ok {
		t.Fatal("parse failed")
	}
	if lp.Username != "user" || lp.Password != "pass" {
		t.Fatalf("user=%q pass=%q", lp.Username, lp.Password)
	}
	out, err := lp.RewriteCredentials("realuser", "secret")
	if err != nil {
		t.Fatal(err)
	}
	parsed, ok := ParseLoginPacket(out)
	if !ok {
		t.Fatal("reparse failed")
	}
	if parsed.Username != "realuser" || parsed.Password != "secret" {
		t.Fatalf("rewritten user=%q pass=%q", parsed.Username, parsed.Password)
	}
}

func TestStripRestoreCRCAroundLogin(t *testing.T) {
	base := []byte{
		0x00, 0x03,
		0x04, 0x00, 0x15, 0x00, 0x00,
		0x20, 0x00, 0x09, 0x00, 0x01,
		0x02, 0x00,
		0x03, 0x00, 0x00, 0x00,
		0x00,
		0x02,
		0x00, 0x00, 0x00, 0x00,
	}
	base = append(base, crypto.GoldenCipherBytes()...)
	wire := RestoreCRC(base, 0x12345678, 2)
	if len(wire) != len(base)+2 {
		t.Fatalf("len=%d", len(wire))
	}
	body := StripForParse(wire, 2)
	lp, ok := ParseLoginPacket(body)
	if !ok || lp.Username != "user" {
		t.Fatalf("parse after strip: ok=%v lp=%+v", ok, lp)
	}
}

func TestParseSessionResponse(t *testing.T) {
	resp := make([]byte, 17)
	resp[0], resp[1] = 0x00, OpSessionResponse
	resp[10] = 2
	if _, ok := ParseSessionResponse(resp); !ok {
		t.Fatal("expected parse")
	}
	if PacketUsesCRC(resp) {
		t.Fatal("session response must not use CRC")
	}
}

func TestLooksLikeLoginFailureAndSuccess(t *testing.T) {
	if LooksLikeLoginFailure(nil) || LooksLikeLoginFailure([]byte{0x00}) {
		t.Fatal("short packet")
	}
	if !LooksLikeLoginFailure([]byte{0x00, OpDisconnect}) {
		t.Fatal("disconnect")
	}
	chat := []byte{0x00, OpPacket, 0x00, 0x00, byte(AppChatMessage), 0x00, 'x'}
	if !LooksLikeLoginFailure(chat) {
		t.Fatal("chat message opcode")
	}
	text := []byte{0x00, OpPacket, 0x00, 0x00, 0x00, 0x00}
	text = append(text, []byte("Error: Invalid Username or Password")...)
	if !LooksLikeLoginFailure(text) {
		t.Fatal("invalid username text")
	}
	list := []byte{0x00, OpPacket, 0x00, 0x00, byte(AppServerListResponse), 0x00}
	if LooksLikeLoginFailure(list) || !LooksLikeLoginSuccess(list) {
		t.Fatal("server list")
	}
	if !LooksLikeLoginSuccess([]byte{0x00, OpFragment, 0x00, 0x00}) {
		t.Fatal("fragment is treated as server-list success")
	}
	combined := []byte{0x00, OpCombined, byte(len(chat))}
	combined = append(combined, chat...)
	if !LooksLikeLoginFailure(combined) {
		t.Fatal("combined chat")
	}
}
