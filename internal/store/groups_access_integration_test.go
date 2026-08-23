package store_test

import (
	"context"
	"testing"

	"github.com/alfred-identity/web/internal/store"
)

func TestGroupAccessAndAdminState(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	admin, err := st.UpsertUser(ctx, "admin-"+randHex(4), "Admin", []string{"admin-role"})
	if err != nil {
		t.Fatal(err)
	}
	member, err := st.UpsertUser(ctx, "member-"+randHex(4), "Member", nil)
	if err != nil {
		t.Fatal(err)
	}

	gname := "g-" + randHex(4)
	gid, err := st.CreateGroup(ctx, gname, "desc", "readonly", []string{"sso"})
	if err != nil {
		t.Fatal(err)
	}
	found, ok, err := st.FindGroupIDByName(ctx, gname)
	if err != nil || !ok || found != gid {
		t.Fatalf("find group: %d ok=%v err=%v want %d", found, ok, err, gid)
	}
	if _, ok, err := st.FindGroupIDByName(ctx, "missing-"+randHex(4)); err != nil || ok {
		t.Fatalf("missing group ok=%v err=%v", ok, err)
	}

	if err := st.AddGroupUser(ctx, gid, member.ID); err != nil {
		t.Fatal(err)
	}
	role, err := st.HighestWebRoleForUser(ctx, member)
	if err != nil || role != "readonly" {
		t.Fatalf("web role=%q err=%v", role, err)
	}
	if err := st.AddGroupRole(ctx, gid, "guild-role"); err != nil {
		t.Fatal(err)
	}
	roled, err := st.UpsertUser(ctx, "roled-"+randHex(4), "Roled", []string{"guild-role"})
	if err != nil {
		t.Fatal(err)
	}
	role, err = st.HighestWebRoleForUser(ctx, roled)
	if err != nil || role != "readonly" {
		t.Fatalf("role-via-group=%q err=%v", role, err)
	}

	acct, err := st.AddEQAccount(ctx, "grpacct_"+randHex(5), "pw", "notes")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.LinkAccountGroup(ctx, acct, gid); err != nil {
		t.Fatal(err)
	}
	if err := st.SetEQPassword(ctx, acct, "new-pw"); err != nil {
		t.Fatal(err)
	}
	user, pass, err := st.DecryptCredentials(ctx, acct)
	if err != nil || pass != "new-pw" || !stringsHasPrefix(user, "grpacct_") {
		t.Fatalf("creds %q %q err=%v", user, pass, err)
	}

	charName := "GChar" + randHex(3)
	if err := st.AddCharacter(ctx, charName, acct); err != nil {
		t.Fatal(err)
	}
	chars, err := st.ListCharacters(ctx, acct)
	if err != nil || len(chars) != 1 || chars[0].Name != charName {
		t.Fatalf("chars=%#v err=%v", chars, err)
	}

	allowed, err := st.AllowedAccountIDs(ctx, member)
	if err != nil {
		t.Fatal(err)
	}
	if !containsID(allowed, acct) {
		t.Fatal("group member should see linked account")
	}

	fs, err := st.FullStateForUser(ctx, member, []store.OnlineEntry{{AccountID: acct, CharacterName: charName}})
	if err != nil {
		t.Fatal(err)
	}
	if len(fs.Accounts) == 0 || len(fs.Online) != 1 {
		t.Fatalf("full state accounts=%d online=%d", len(fs.Accounts), len(fs.Online))
	}

	adminState, err := st.AdminState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(adminState.Users) == 0 {
		t.Fatal("expected admin users")
	}

	raw, _, err := st.CreateToken(ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	tokUser, _, err := st.UserByToken(ctx, raw)
	if err != nil || tokUser.ID != admin.ID {
		t.Fatalf("UserByToken: %#v err=%v", tokUser, err)
	}
	if err := st.SetUserAccessRevoked(ctx, admin.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UserByToken(ctx, raw); err == nil {
		t.Fatal("revoked user token should fail")
	}
	_ = st.SetUserAccessRevoked(ctx, admin.ID, false)

	if err := st.DeleteEQAccount(ctx, acct); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveGroupUser(ctx, gid, member.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteGroup(ctx, gid); err != nil {
		t.Fatal(err)
	}
}

func TestSetAccountAccessFiltersAllowed(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	owner, err := st.UpsertUser(ctx, "own-"+randHex(4), "Owner", nil)
	if err != nil {
		t.Fatal(err)
	}
	friend, err := st.UpsertUser(ctx, "fr-"+randHex(4), "Friend", []string{"need-role"})
	if err != nil {
		t.Fatal(err)
	}
	stranger, err := st.UpsertUser(ctx, "st-"+randHex(4), "Stranger", nil)
	if err != nil {
		t.Fatal(err)
	}

	acct, err := st.AddEQAccount(ctx, "lim_"+randHex(5), "pw", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetAccountAccess(ctx, acct, []string{"need-role"}, []int64{friend.ID}, nil); err != nil {
		t.Fatal(err)
	}

	friendAllowed, err := st.AllowedAccountIDs(ctx, friend)
	if err != nil {
		t.Fatal(err)
	}
	strangerAllowed, err := st.AllowedAccountIDs(ctx, stranger)
	if err != nil {
		t.Fatal(err)
	}
	ownerAllowed, err := st.AllowedAccountIDs(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if !containsID(friendAllowed, acct) {
		t.Fatal("friend should see limited account")
	}
	if containsID(strangerAllowed, acct) {
		t.Fatal("stranger must not see limited account")
	}
	// Open base accounts are visible; limited ones are not unless granted.
	_ = ownerAllowed

	meta, err := st.LoadEQAccountMeta(ctx, acct)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.RequiredRoleIDs) == 0 && meta.RequiredRoleID == "" {
		t.Fatalf("expected role grant on meta: %#v", meta)
	}
}

func stringsHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
