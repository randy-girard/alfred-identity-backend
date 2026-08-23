package web

import (
	"context"
	"strings"
	"testing"

	"github.com/alfred-identity/web/internal/store"
)

func TestImportSSOAccountsCSV(t *testing.T) {
	st := openTestStoreForWeb(t)
	ctx := context.Background()
	actor, err := st.UpsertUser(ctx, "csv-"+testRandHex(4), "CSV Actor", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertDiscordRoles(ctx, []store.DiscordRole{
		{ID: "111111111111111111", Name: "Raider"},
	}); err != nil {
		t.Fatal(err)
	}

	s := New(Options{
		Store:      st,
		SessionKey: []byte("test-session-key-32-bytes-long!!"),
		PublicURL:  "http://127.0.0.1:8181",
	})

	uname := "csvacct_" + testRandHex(4)
	charName := "Hero" + testRandHex(2)
	csvIn := "username,password,role,aliases,tags,characters\n" +
		uname + `,secret,Raider,tank|box,raid,` + charName + "\n"
	res, err := s.importSSOAccountsCSV(ctx, actor, strings.NewReader(csvIn))
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 1 || len(res.Errors) != 0 {
		t.Fatalf("%+v", res)
	}

	id, ok, err := st.FindEQAccountIDByUsername(ctx, uname)
	if err != nil || !ok {
		t.Fatalf("find: ok=%v err=%v", ok, err)
	}
	user, pass, err := st.DecryptCredentials(ctx, id)
	if err != nil || user != uname || pass != "secret" {
		t.Fatalf("creds %q %q err=%v", user, pass, err)
	}
	acctID, err := st.AccountIDByCharacter(ctx, charName)
	if err != nil || acctID != id {
		t.Fatalf("character link: %d err=%v", acctID, err)
	}

	// Update existing row
	res, err = s.importSSOAccountsCSV(ctx, actor, strings.NewReader(csvIn))
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated != 1 {
		t.Fatalf("expected update: %+v", res)
	}

	bad := `username,password,role
badacct,pw,NotARealRole
`
	res, err = s.importSSOAccountsCSV(ctx, actor, strings.NewReader(bad))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) == 0 {
		t.Fatalf("expected role error: %+v", res)
	}
}

func TestWebAccessLevelLegacyAndGroup(t *testing.T) {
	st := openTestStoreForWeb(t)
	ctx := context.Background()

	s := New(Options{
		Store:             st,
		SessionKey:        []byte("test-session-key-32-bytes-long!!"),
		PublicURL:         "http://127.0.0.1:8181",
		AdminRoleID:       "admin-role",
		AccessRoleID:      "legacy-web",
		BootstrapAdminIDs: []string{"boot-csv"},
	})

	if got := s.webAccessLevel(ctx, store.User{DiscordID: "boot-csv"}); got != webRoleAdmin {
		t.Fatalf("bootstrap=%q", got)
	}
	if got := s.webAccessLevel(ctx, store.User{DiscordID: "x", RoleIDs: []string{"admin-role"}}); got != webRoleAdmin {
		t.Fatalf("admin role=%q", got)
	}
	if got := s.webAccessLevel(ctx, store.User{DiscordID: "x", RoleIDs: []string{"legacy-web"}}); got != webRoleReadonly {
		t.Fatalf("legacy=%q", got)
	}
	if got := s.webAccessLevel(ctx, store.User{DiscordID: "x"}); got != webRoleNone {
		t.Fatalf("denied=%q", got)
	}

	u, err := st.UpsertUser(ctx, "grpweb-"+testRandHex(4), "GW", nil)
	if err != nil {
		t.Fatal(err)
	}
	gid, err := st.CreateGroup(ctx, "webg-"+testRandHex(4), "", "admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddGroupUser(ctx, gid, u.ID); err != nil {
		t.Fatal(err)
	}
	if got := s.webAccessLevel(ctx, u); got != webRoleAdmin {
		t.Fatalf("group admin=%q", got)
	}
	if !s.canAccessWeb(ctx, u) || !s.isWebAdmin(ctx, u) {
		t.Fatal("group admin helpers")
	}
}
