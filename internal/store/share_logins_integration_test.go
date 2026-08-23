package store_test

import (
	"context"
	"testing"
)

func TestListOwnedShareLogins(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	owner, err := st.UpsertUser(ctx, "own-"+randHex(4), "Owner", nil)
	if err != nil {
		t.Fatal(err)
	}
	friend, err := st.UpsertUser(ctx, "fr-"+randHex(4), "Friend", nil)
	if err != nil {
		t.Fatal(err)
	}

	uname := "shlog_" + randHex(5)
	shareID, _, err := st.ShareLocalAccount(ctx, owner, uname, "pw", nil, []int64{friend.ID}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	st.AuditAccount(ctx, friend.ID, shareID, "login_auth", "alias-login")

	logins, err := st.ListOwnedShareLogins(ctx, owner.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(logins) == 0 {
		t.Fatal("expected login_auth audit for owned share")
	}
	if logins[0].AccountID != shareID || logins[0].Detail != "alias-login" {
		t.Fatalf("%#v", logins[0])
	}

	empty, err := st.ListOwnedShareLogins(ctx, 0, 10)
	if err != nil || empty != nil {
		t.Fatalf("zero owner: %#v err=%v", empty, err)
	}
}

func TestDiscordCommandAccess(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	u, err := st.UpsertUser(ctx, "dc-"+randHex(4), "User", []string{"raid-role"})
	if err != nil {
		t.Fatal(err)
	}
	stranger, err := st.UpsertUser(ctx, "dc2-"+randHex(4), "Stranger", nil)
	if err != nil {
		t.Fatal(err)
	}

	gid, err := st.CreateGroup(ctx, "dcg-"+randHex(4), "", "", []string{"sso"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddGroupRole(ctx, gid, "raid-role"); err != nil {
		t.Fatal(err)
	}

	restricted, err := st.IsDiscordCommandRestricted(ctx, "sso")
	if err != nil || !restricted {
		t.Fatalf("restricted=%v err=%v", restricted, err)
	}

	ok, err := st.UserCanUseDiscordCommand(ctx, u, "sso")
	if err != nil || !ok {
		t.Fatalf("role member should use sso: ok=%v err=%v", ok, err)
	}
	ok, err = st.UserCanUseDiscordCommand(ctx, stranger, "sso")
	if err != nil || ok {
		t.Fatalf("stranger should not use restricted sso: ok=%v err=%v", ok, err)
	}

	if _, err := st.IsDiscordCommandRestricted(ctx, "nope"); err == nil {
		t.Fatal("expected unknown command error")
	}
}

func TestListDirectoryUsers(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	u, err := st.UpsertUser(ctx, "dir-"+randHex(4), "Dir", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := st.CreateToken(ctx, u.ID)
	if err != nil || raw == "" {
		t.Fatal(err)
	}
	list, err := st.ListDirectoryUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range list {
		if d.ID == u.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("directory missing user %d: %#v", u.ID, list)
	}
}
