package sso

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/alfred-identity/web/internal/presence"
)

func TestHubAdminAccountCRUD(t *testing.T) {
	st := openHubTestStore(t)
	ctxBG := context.Background()

	admin, err := st.UpsertUser(ctxBG, "hadmin-"+randHex(4), "Admin", nil)
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

	uname := "adminacct_" + randHex(5)
	writeWS(t, ctx, conn, map[string]any{
		"type": "admin_add_account", "request_id": "a1",
		"username": uname, "password": "hunter2",
	})
	resp := readWSUntil(t, ctx, conn, "admin_result")
	if resp["ok"] != true {
		t.Fatalf("add account: %#v", resp)
	}
	acctID := int64(resp["account_id"].(float64))
	if acctID <= 0 {
		t.Fatalf("account_id=%v", resp["account_id"])
	}

	writeWS(t, ctx, conn, map[string]any{
		"type": "admin_add_alias", "request_id": "a2",
		"alias": "alias_" + randHex(3), "account_id": acctID,
	})
	if readWSUntil(t, ctx, conn, "admin_result")["ok"] != true {
		t.Fatal("add alias")
	}

	tag := "tag_" + randHex(3)
	writeWS(t, ctx, conn, map[string]any{
		"type": "admin_add_tag", "request_id": "a3",
		"tag": tag, "account_id": acctID,
	})
	if readWSUntil(t, ctx, conn, "admin_result")["ok"] != true {
		t.Fatal("add tag")
	}

	charName := "Adm" + randHex(3)
	writeWS(t, ctx, conn, map[string]any{
		"type": "admin_add_character", "request_id": "a4",
		"name": charName, "account_id": acctID,
	})
	if readWSUntil(t, ctx, conn, "admin_result")["ok"] != true {
		t.Fatal("add character")
	}

	writeWS(t, ctx, conn, map[string]any{
		"type": "admin_remove_character", "request_id": "a5",
		"name": charName, "account_id": acctID,
	})
	if readWSUntil(t, ctx, conn, "admin_result")["ok"] != true {
		t.Fatal("remove character")
	}

	writeWS(t, ctx, conn, map[string]any{
		"type": "admin_remove_tag", "request_id": "a6",
		"tag": tag, "account_id": acctID,
	})
	if readWSUntil(t, ctx, conn, "admin_result")["ok"] != true {
		t.Fatal("remove tag")
	}

	writeWS(t, ctx, conn, map[string]any{
		"type": "admin_remove_account", "request_id": "a7",
		"account_id": acctID,
	})
	if readWSUntil(t, ctx, conn, "admin_result")["ok"] != true {
		t.Fatal("remove account")
	}
}

func TestHubAdminForbiddenForNonAdmin(t *testing.T) {
	st := openHubTestStore(t)
	ctxBG := context.Background()
	u, err := st.UpsertUser(ctxBG, "huser-"+randHex(4), "User", nil)
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
		AdminRoleID:     "only-admins",
		Log:             slog.Default(),
	}
	conn, ctx, cancel := dialHub(t, h)
	defer cancel()
	authHub(t, ctx, conn, raw)

	writeWS(t, ctx, conn, map[string]any{
		"type": "admin_add_account", "request_id": "x1",
		"username": "nope_" + randHex(4), "password": "pw",
	})
	resp := readWSUntil(t, ctx, conn, "admin_result")
	if resp["ok"] != false || resp["error"] != "forbidden" {
		t.Fatalf("%#v", resp)
	}
}
