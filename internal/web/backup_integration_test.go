package web

import (
	"context"
	"strings"
	"testing"
)

func TestConfigBackupImportRoundTrip(t *testing.T) {
	st := openTestStoreForWeb(t)
	ctx := context.Background()

	// Import-only: export scans every eq_accounts row and fails when a shared
	// TEST DB contains rows sealed under other packages' AEAD keys.
	did := "bak-only-" + testRandHex(4)
	uname := "bakonly_" + testRandHex(5)
	gname := "bakonlyg-" + testRandHex(3)
	alias := "bakal_" + testRandHex(3)
	tag := "bakt_" + testRandHex(3)
	charName := "BakH" + testRandHex(2)
	body := `{
	  "version": 1,
	  "discord_roles": [{"id":"role-bak","name":"BakRole"}],
	  "users": [{"discord_id":"` + did + `","display_name":"Bak","role_ids":["role-bak"],"access_revoked":false}],
	  "groups": [{"name":"` + gname + `","description":"d","web_role":"readonly","discord_commands":["whoami"],"member_discord_ids":["` + did + `"],"member_role_ids":[],"account_usernames":["` + uname + `"]}],
	  "accounts": [{"username":"` + uname + `","password":"pw1","aliases":["` + alias + `"],"tags":["` + tag + `"],"characters":["` + charName + `"],"group_names":["` + gname + `"]}]
	}`
	s := New(Options{
		Store:      st,
		SessionKey: []byte("test-session-key-32-bytes-long!!"),
		PublicURL:  "http://127.0.0.1:8181",
	})
	res, err := s.importConfigBackup(ctx, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) > 0 {
		t.Fatalf("import errors: %#v", res.Errors)
	}
	if res.UsersAdded < 1 || res.AccountsAdded < 1 || res.GroupsAdded < 1 {
		t.Fatalf("%+v", res)
	}
	id, ok, err := st.FindEQAccountIDByUsername(ctx, uname)
	if err != nil || !ok {
		t.Fatalf("account missing ok=%v err=%v", ok, err)
	}
	user, pass, err := st.DecryptCredentials(ctx, id)
	if err != nil || user != uname || pass != "pw1" {
		t.Fatalf("creds %q %q err=%v", user, pass, err)
	}
	meta, err := st.LoadEQAccountMeta(ctx, id)
	if err != nil || len(meta.Aliases) == 0 || len(meta.Tags) == 0 || len(meta.Characters) == 0 {
		t.Fatalf("meta=%#v err=%v", meta, err)
	}
}

func TestImportConfigBackupCreatesUsersAndAccounts(t *testing.T) {
	st := openTestStoreForWeb(t)
	ctx := context.Background()
	s := New(Options{
		Store:      st,
		SessionKey: []byte("test-session-key-32-bytes-long!!"),
		PublicURL:  "http://127.0.0.1:8181",
	})

	did := "newuser-" + testRandHex(4)
	uname := "newacct_" + testRandHex(5)
	body := `{
	  "version": 1,
	  "users": [{"discord_id":"` + did + `","display_name":"New","role_ids":[],"access_revoked":false}],
	  "groups": [{"name":"ng-` + testRandHex(3) + `","web_role":"admin","member_discord_ids":["` + did + `"],"member_role_ids":[],"account_usernames":["` + uname + `"]}],
	  "accounts": [{"username":"` + uname + `","password":"secret","aliases":["a1_` + testRandHex(3) + `"],"tags":["t1_` + testRandHex(3) + `"],"characters":["C` + testRandHex(2) + `"],"group_names":[]}]
	}`
	res, err := s.importConfigBackup(ctx, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if res.UsersAdded < 1 || res.AccountsAdded < 1 || res.GroupsAdded < 1 {
		t.Fatalf("%+v", res)
	}
	id, ok, err := st.FindEQAccountIDByUsername(ctx, uname)
	if err != nil || !ok || id <= 0 {
		t.Fatalf("account missing ok=%v err=%v", ok, err)
	}
	u, err := st.UserByDiscordID(ctx, did)
	if err != nil || u.DisplayName != "New" {
		t.Fatalf("user: %#v err=%v", u, err)
	}
}
