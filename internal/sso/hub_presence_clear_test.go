package sso

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/alfred-identity/web/internal/presence"
)

func drainNotify(ch chan struct{}) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func TestHubHeartbeatClearsPreviousAccountForSameUser(t *testing.T) {
	st := openHubTestStore(t)
	ctxBG := context.Background()
	u, err := st.UpsertUser(ctxBG, "sw-"+randHex(4), "Switcher", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := st.CreateToken(ctxBG, u.ID)
	if err != nil {
		t.Fatal(err)
	}

	acct1, err := st.AddEQAccount(ctxBG, "sw1_"+randHex(5), "pw", "")
	if err != nil {
		t.Fatal(err)
	}
	char1 := "Char" + randHex(3)
	if err := st.AddCharacter(ctxBG, char1, acct1); err != nil {
		t.Fatal(err)
	}
	acct2, err := st.AddEQAccount(ctxBG, "sw2_"+randHex(5), "pw", "")
	if err != nil {
		t.Fatal(err)
	}
	char2 := "Char" + randHex(3)
	if err := st.AddCharacter(ctxBG, char2, acct2); err != nil {
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
	drainNotify(notified)

	writeWS(t, ctx, conn, map[string]any{
		"type": "heartbeat", "character_name": char1, "offline": false,
	})
	select {
	case <-notified:
	case <-time.After(2 * time.Second):
		t.Fatal("expected notify after first heartbeat")
	}
	if !pres.IsBusy(acct1) {
		t.Fatal("acct1 should be busy after first heartbeat")
	}
	drainNotify(notified)

	writeWS(t, ctx, conn, map[string]any{
		"type": "heartbeat", "character_name": char2, "offline": false,
	})
	select {
	case <-notified:
	case <-time.After(2 * time.Second):
		t.Fatal("expected notify after switch heartbeat")
	}
	if pres.IsBusy(acct1) {
		t.Fatal("acct1 should be cleared after switching to char2")
	}
	if !pres.IsBusy(acct2) {
		t.Fatal("acct2 should be busy after second heartbeat")
	}
}

func TestHubLoginAuthClearsPreviousAccountForSameUser(t *testing.T) {
	st := openHubTestStore(t)
	ctxBG := context.Background()
	u, err := st.UpsertUser(ctxBG, "la-"+randHex(4), "LoginAuth", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := st.CreateToken(ctxBG, u.ID)
	if err != nil {
		t.Fatal(err)
	}

	uname1 := "la1_" + randHex(5)
	acct1, err := st.AddEQAccount(ctxBG, uname1, "pw", "")
	if err != nil {
		t.Fatal(err)
	}
	char1 := "Char" + randHex(3)
	if err := st.AddCharacter(ctxBG, char1, acct1); err != nil {
		t.Fatal(err)
	}
	uname2 := "la2_" + randHex(5)
	acct2, err := st.AddEQAccount(ctxBG, uname2, "pw", "")
	if err != nil {
		t.Fatal(err)
	}

	pres := presence.New(time.Minute)
	pres.Touch(acct1, char1, u.ID)
	h := &Hub{
		Store:           st,
		Presence:        pres,
		ProtocolVersion: DefaultProtocolVersion,
		Log:             slog.Default(),
	}
	conn, ctx, cancel := dialHub(t, h)
	defer cancel()
	authHub(t, ctx, conn, raw)

	writeWS(t, ctx, conn, map[string]any{
		"type": "login_auth", "request_id": "sw1", "username": uname2,
	})
	resp := readWSUntil(t, ctx, conn, "login_auth_response")
	if resp["error"] != nil {
		t.Fatalf("login_auth: %#v", resp)
	}
	if pres.IsBusy(acct1) {
		t.Fatal("acct1 presence should be cleared after login_auth to acct2")
	}
	if int64(resp["account_id"].(float64)) != acct2 {
		t.Fatalf("account_id=%v want %d", resp["account_id"], acct2)
	}
}
