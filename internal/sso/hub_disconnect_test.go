package sso

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/alfred-identity/web/internal/presence"
)

func TestHubDisconnectUserClosesClient(t *testing.T) {
	st := openHubTestStore(t)
	ctxBG := context.Background()
	u, err := st.UpsertUser(ctxBG, "disc-"+randHex(4), "User", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := st.CreateToken(ctxBG, u.ID)
	if err != nil {
		t.Fatal(err)
	}

	h := &Hub{
		Store:           st,
		Presence:        presence.New(time.Minute),
		ProtocolVersion: DefaultProtocolVersion,
		Log:             slog.Default(),
	}
	conn, ctx, cancel := dialHub(t, h)
	defer cancel()
	authHub(t, ctx, conn, raw)

	if len(h.Connections()) != 1 {
		t.Fatalf("connections=%d", len(h.Connections()))
	}

	h.DisconnectUser(u.ID, "access revoked")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(h.Connections()) == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(h.Connections()) != 0 {
		t.Fatalf("expected disconnect, still %d", len(h.Connections()))
	}

	readCtx, readCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer readCancel()
	_, data, err := conn.Read(readCtx)
	if err != nil {
		return // connection already closed before error frame
	}
	var msg map[string]any
	if json.Unmarshal(data, &msg) != nil || msg["type"] != "error" {
		t.Fatalf("expected error frame before close: %s err=%v", data, err)
	}
	readCtx2, readCancel2 := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer readCancel2()
	_, _, err = conn.Read(readCtx2)
	if err == nil {
		t.Fatal("expected read error after disconnect")
	}
}

func TestHubAdminUpdateAccount(t *testing.T) {
	st := openHubTestStore(t)
	ctxBG := context.Background()
	admin, err := st.UpsertUser(ctxBG, "upd-"+randHex(4), "Admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := st.CreateToken(ctxBG, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	acctID, err := st.AddEQAccount(ctxBG, "updacct_"+randHex(5), "old", "")
	if err != nil {
		t.Fatal(err)
	}

	h := &Hub{
		Store:             st,
		Presence:          presence.New(time.Minute),
		ProtocolVersion:   DefaultProtocolVersion,
		BootstrapAdminIDs: []string{admin.DiscordID},
		Log:               slog.Default(),
	}
	conn, ctx, cancel := dialHub(t, h)
	defer cancel()
	authHub(t, ctx, conn, raw)

	disabled := true
	writeWS(t, ctx, conn, map[string]any{
		"type": "admin_update_account", "request_id": "u1",
		"account_id": acctID, "password": "newpass", "disabled": disabled,
	})
	resp := readWSUntil(t, ctx, conn, "admin_result")
	if resp["ok"] != true {
		t.Fatalf("%#v", resp)
	}
	user, pass, err := st.DecryptCredentialsAny(ctxBG, acctID)
	if err != nil || pass != "newpass" {
		t.Fatalf("pass=%q err=%v", pass, err)
	}
	meta, err := st.LoadEQAccountMeta(ctxBG, acctID)
	if err != nil || !meta.Disabled || meta.Username != user {
		t.Fatalf("meta=%#v err=%v", meta, err)
	}
}
