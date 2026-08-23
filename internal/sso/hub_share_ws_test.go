package sso

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/alfred-identity/web/internal/presence"
)

func TestHubShareAndUnshareAccount(t *testing.T) {
	st := openHubTestStore(t)
	ctxBG := context.Background()

	owner, err := st.UpsertUser(ctxBG, "share-owner-"+randHex(4), "Owner", nil)
	if err != nil {
		t.Fatal(err)
	}
	friend, err := st.UpsertUser(ctxBG, "share-friend-"+randHex(4), "Friend", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := st.CreateToken(ctxBG, owner.ID)
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

	uname := "sharebox_" + randHex(5)
	writeWS(t, ctx, conn, map[string]any{
		"type": "share_account", "request_id": "s1",
		"username": uname, "password": "secret",
		"aliases": []string{"sharealias"}, "user_ids": []int64{friend.ID},
	})
	resp := readWSUntil(t, ctx, conn, "share_result")
	if resp["ok"] != true {
		t.Fatalf("share: %#v", resp)
	}
	acctID := int64(resp["account_id"].(float64))
	if acctID <= 0 {
		t.Fatalf("account_id=%v", resp["account_id"])
	}

	allowed, err := st.AllowedAccountIDs(ctxBG, friend)
	if err != nil || !containsInt64(allowed, acctID) {
		t.Fatalf("friend should see share: %v err=%v", allowed, err)
	}

	writeWS(t, ctx, conn, map[string]any{
		"type": "unshare_account", "request_id": "s2", "username": uname,
	})
	resp = readWSUntil(t, ctx, conn, "share_result")
	if resp["ok"] != true {
		t.Fatalf("unshare: %#v", resp)
	}
	allowed, err = st.AllowedAccountIDs(ctxBG, friend)
	if err != nil {
		t.Fatal(err)
	}
	if containsInt64(allowed, acctID) {
		t.Fatal("friend should lose share after unshare")
	}
}

func containsInt64(ids []int64, want int64) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
