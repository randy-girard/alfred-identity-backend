package sso

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alfred-identity/web/internal/crypto"
	"github.com/alfred-identity/web/internal/db"
	"github.com/alfred-identity/web/internal/presence"
	"github.com/alfred-identity/web/internal/store"
	"github.com/coder/websocket"
)

func openHubTestStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run hub websocket integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sqlDB, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("db connect: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Migrate(sqlDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	aead, err := crypto.NewAEAD(key)
	if err != nil {
		t.Fatal(err)
	}
	return &store.Store{DB: sqlDB, AEAD: aead, Key: key}
}

func dialHub(t *testing.T, h *Hub) (*websocket.Conn, context.Context, context.CancelFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	wsURL := "ws://" + strings.TrimPrefix(srv.URL, "http://")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
	return conn, ctx, cancel
}

func writeWS(t *testing.T, ctx context.Context, conn *websocket.Conn, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
}

func readWSMap(t *testing.T, ctx context.Context, conn *websocket.Conn) map[string]any {
	t.Helper()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("json: %v raw=%s", err, data)
	}
	return m
}

func readWSUntil(t *testing.T, ctx context.Context, conn *websocket.Conn, typ string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		readCtx, cancel := context.WithTimeout(ctx, time.Until(deadline))
		msg := readWSMap(t, readCtx, conn)
		cancel()
		if msg["type"] == typ {
			return msg
		}
	}
	t.Fatalf("timeout waiting for type %q", typ)
	return nil
}

func authHub(t *testing.T, ctx context.Context, conn *websocket.Conn, token string) map[string]any {
	t.Helper()
	writeWS(t, ctx, conn, map[string]any{
		"type":             "auth",
		"token":            token,
		"protocol_version": DefaultProtocolVersion,
		"client_version":   "test/1",
	})
	msg := readWSUntil(t, ctx, conn, "full_state")
	return msg
}

func TestHubLoginAuthAndHeartbeat(t *testing.T) {
	st := openHubTestStore(t)
	ctxBG := context.Background()
	u, err := st.UpsertUser(ctxBG, "hub-"+randHex(4), "Hub User", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := st.CreateToken(ctxBG, u.ID)
	if err != nil {
		t.Fatal(err)
	}

	uname := "hubacct_" + randHex(5)
	acctID, err := st.AddEQAccount(ctxBG, uname, "secret", "")
	if err != nil {
		t.Fatal(err)
	}
	charName := "Char" + randHex(3)
	if err := st.AddCharacter(ctxBG, charName, acctID); err != nil {
		t.Fatal(err)
	}
	tag := "boxtag_" + randHex(3)
	if err := st.AddTag(ctxBG, tag, acctID); err != nil {
		t.Fatal(err)
	}
	acct2, err := st.AddEQAccount(ctxBG, "hubacct2_"+randHex(5), "secret", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddTag(ctxBG, tag, acct2); err != nil {
		t.Fatal(err)
	}

	pres := presence.New(time.Minute)
	notified := make(chan struct{}, 8)
	h := &Hub{
		Store:           st,
		Presence:        pres,
		ProtocolVersion: DefaultProtocolVersion,
		Log:             slog.Default(),
	}
	h.OnStateChange(func() {
		select {
		case notified <- struct{}{}:
		default:
		}
	})

	conn, ctx, cancel := dialHub(t, h)
	defer cancel()
	authHub(t, ctx, conn, raw)

	// Direct username login while busy still succeeds.
	pres.Touch(acctID, charName, u.ID)
	writeWS(t, ctx, conn, map[string]any{
		"type": "login_auth", "request_id": "r1", "username": uname,
	})
	resp := readWSUntil(t, ctx, conn, "login_auth_response")
	if resp["error"] != nil {
		t.Fatalf("direct login: %#v", resp)
	}
	if int64(resp["account_id"].(float64)) != acctID {
		t.Fatalf("account_id=%v want %d", resp["account_id"], acctID)
	}
	if resp["encrypted_credentials"] == "" || resp["real_user"] != uname {
		t.Fatalf("creds missing: %#v", resp)
	}

	// Tag pool skips busy and picks the free account.
	writeWS(t, ctx, conn, map[string]any{
		"type": "login_auth", "request_id": "r2", "username": tag,
	})
	resp = readWSUntil(t, ctx, conn, "login_auth_response")
	if resp["error"] != nil {
		t.Fatalf("tag login: %#v", resp)
	}
	if int64(resp["account_id"].(float64)) != acct2 {
		t.Fatalf("expected free tag account %d, got %#v", acct2, resp)
	}

	// Mark both busy → tag pool all_busy.
	pres.Touch(acct2, "Other", u.ID)
	writeWS(t, ctx, conn, map[string]any{
		"type": "login_auth", "request_id": "r3", "username": tag,
	})
	resp = readWSUntil(t, ctx, conn, "login_auth_response")
	if resp["error"] != "all_busy" {
		t.Fatalf("expected all_busy: %#v", resp)
	}

	// Heartbeat online notifies listeners and sets presence.
	drain := func() {
		for {
			select {
			case <-notified:
			default:
				return
			}
		}
	}
	drain()
	writeWS(t, ctx, conn, map[string]any{
		"type": "heartbeat", "character_name": charName, "offline": false,
	})
	select {
	case <-notified:
	case <-time.After(2 * time.Second):
		t.Fatal("expected presence notify on heartbeat")
	}
	if !pres.IsBusy(acctID) {
		t.Fatal("expected account busy after heartbeat")
	}

	writeWS(t, ctx, conn, map[string]any{
		"type": "heartbeat", "character_name": charName, "offline": true,
	})
	select {
	case <-notified:
	case <-time.After(2 * time.Second):
		t.Fatal("expected presence notify on offline heartbeat")
	}
	if pres.IsBusy(acctID) {
		t.Fatal("expected clear after offline heartbeat")
	}

	writeWS(t, ctx, conn, map[string]any{"type": "ping"})
	pong := readWSUntil(t, ctx, conn, "pong")
	if pong["type"] != "pong" {
		t.Fatalf("ping: %#v", pong)
	}
}

func TestHubAuthUnauthorized(t *testing.T) {
	st := openHubTestStore(t)
	h := &Hub{
		Store:           st,
		Presence:        presence.New(time.Minute),
		ProtocolVersion: DefaultProtocolVersion,
		Log:             slog.Default(),
	}
	conn, ctx, cancel := dialHub(t, h)
	defer cancel()
	writeWS(t, ctx, conn, map[string]any{
		"type": "auth", "token": "nope", "protocol_version": DefaultProtocolVersion,
	})
	msg := readWSMap(t, ctx, conn)
	if msg["type"] != "error" || msg["message"] != "unauthorized" {
		t.Fatalf("%#v", msg)
	}
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	const hexdigits = "0123456789abcdef"
	out := make([]byte, n*2)
	for i, v := range b {
		out[i*2] = hexdigits[v>>4]
		out[i*2+1] = hexdigits[v&0x0f]
	}
	return string(out)
}
