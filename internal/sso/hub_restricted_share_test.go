package sso

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/alfred-identity/web/internal/presence"
)

func TestHubAdminCannotMutatePrivateShare(t *testing.T) {
	st := openHubTestStore(t)
	ctxBG := context.Background()

	owner, err := st.UpsertUser(ctxBG, "share-owner-"+randHex(4), "Owner", nil)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := st.UpsertUser(ctxBG, "share-admin-"+randHex(4), "Admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	shareID, _, err := st.ShareLocalAccount(ctxBG, owner, "privshare_"+randHex(6), "secret", nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := st.CreateToken(ctxBG, admin.ID)
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

	writeWS(t, ctx, conn, map[string]any{
		"type": "admin_update_account", "request_id": "p1",
		"account_id": shareID, "password": "hijacked",
	})
	resp := readWSUntil(t, ctx, conn, "admin_result")
	if resp["ok"] != false || resp["error"] != "share_password_managed_in_gui" {
		t.Fatalf("password update: %#v", resp)
	}

	writeWS(t, ctx, conn, map[string]any{
		"type": "admin_remove_account", "request_id": "p2",
		"account_id": shareID,
	})
	resp = readWSUntil(t, ctx, conn, "admin_result")
	if resp["ok"] != false || resp["error"] != "share_not_owner" {
		t.Fatalf("remove share: %#v", resp)
	}

}
