package store_test

import (
	"context"
	"testing"
)

func TestResolveLoginCandidatesMatchFlags(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	u, err := st.UpsertUser(ctx, "login-"+randHex(4), "Login User", nil)
	if err != nil {
		t.Fatal(err)
	}

	uname := "acct_" + randHex(6)
	id, err := st.AddEQAccount(ctx, uname, "pw-secret", "")
	if err != nil {
		t.Fatal(err)
	}
	alias := "alias_" + randHex(4)
	if err := st.AddAlias(ctx, alias, id); err != nil {
		t.Fatal(err)
	}
	tag := "tag_" + randHex(4)
	if err := st.AddTag(ctx, tag, id); err != nil {
		t.Fatal(err)
	}
	charName := "Hero" + randHex(3)
	if err := st.AddCharacter(ctx, charName, id); err != nil {
		t.Fatal(err)
	}

	byUser, err := st.ResolveLoginCandidates(ctx, u, uname)
	if err != nil {
		t.Fatal(err)
	}
	if len(byUser) != 1 || !byUser[0].ByUser || byUser[0].ID != id {
		t.Fatalf("username match: %#v", byUser)
	}

	byAlias, err := st.ResolveLoginCandidates(ctx, u, alias)
	if err != nil {
		t.Fatal(err)
	}
	if len(byAlias) != 1 || !byAlias[0].ByAlias || byAlias[0].ByUser {
		t.Fatalf("alias match: %#v", byAlias)
	}

	byTag, err := st.ResolveLoginCandidates(ctx, u, tag)
	if err != nil {
		t.Fatal(err)
	}
	if len(byTag) != 1 || !byTag[0].ByTag || byTag[0].Direct() {
		t.Fatalf("tag match should not be Direct: %#v", byTag)
	}

	byChar, err := st.ResolveLoginCandidates(ctx, u, charName)
	if err != nil {
		t.Fatal(err)
	}
	if len(byChar) != 1 || !byChar[0].ByCharacter || !byChar[0].Direct() {
		t.Fatalf("character match: %#v", byChar)
	}

	empty, err := st.ResolveLoginCandidates(ctx, u, "")
	if err != nil || empty != nil {
		t.Fatalf("empty name: %#v err=%v", empty, err)
	}
	miss, err := st.ResolveLoginCandidates(ctx, u, "no-such-"+randHex(4))
	if err != nil || len(miss) != 0 {
		t.Fatalf("miss: %#v err=%v", miss, err)
	}
}

func TestAccountCharacterCredentialsRoundTrip(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	uname := "cred_" + randHex(6)
	id, err := st.AddEQAccount(ctx, uname, "hunter2", "note")
	if err != nil {
		t.Fatal(err)
	}
	found, ok, err := st.FindEQAccountIDByUsername(ctx, uname)
	if err != nil || !ok || found != id {
		t.Fatalf("find: id=%d ok=%v err=%v", found, ok, err)
	}
	found, ok, err = st.FindEQAccountIDByUsername(ctx, "")
	if err != nil || ok {
		t.Fatalf("empty find: ok=%v err=%v", ok, err)
	}

	charName := "Tank" + randHex(3)
	if err := st.AddCharacter(ctx, charName, id); err != nil {
		t.Fatal(err)
	}
	acctID, err := st.AccountIDByCharacter(ctx, charName)
	if err != nil || acctID != id {
		t.Fatalf("by char: %d err=%v", acctID, err)
	}
	if _, err := st.AccountIDByCharacter(ctx, "Missing"+randHex(3)); err == nil {
		t.Fatal("expected missing character error")
	}

	gotUser, gotPass, err := st.DecryptCredentials(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if gotUser != uname || gotPass != "hunter2" {
		t.Fatalf("creds user=%q pass=%q", gotUser, gotPass)
	}

	if err := st.SetEQDisabled(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.DecryptCredentials(ctx, id); err == nil {
		t.Fatal("disabled account should fail DecryptCredentials")
	}
	gotUser, gotPass, err = st.DecryptCredentialsAny(ctx, id)
	if err != nil || gotUser != uname || gotPass != "hunter2" {
		t.Fatalf("DecryptCredentialsAny: %q %q err=%v", gotUser, gotPass, err)
	}

	if err := st.RemoveCharacter(ctx, charName, id); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AccountIDByCharacter(ctx, charName); err == nil {
		t.Fatal("character should be gone")
	}
}

func TestAliasTagUniqueAndRemove(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	a, err := st.AddEQAccount(ctx, "a_"+randHex(5), "p", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.AddEQAccount(ctx, "b_"+randHex(5), "p", "")
	if err != nil {
		t.Fatal(err)
	}
	alias := "sharedalias_" + randHex(4)
	if err := st.AddAlias(ctx, alias, a); err != nil {
		t.Fatal(err)
	}
	if err := st.AddAlias(ctx, alias, b); err == nil {
		t.Fatal("duplicate alias should fail")
	}
	if err := st.RemoveAlias(ctx, alias, a); err != nil {
		t.Fatal(err)
	}
	if err := st.AddAlias(ctx, alias, b); err != nil {
		t.Fatal(err)
	}

	tag := "pool_" + randHex(4)
	if err := st.AddTag(ctx, tag, a); err != nil {
		t.Fatal(err)
	}
	if err := st.AddTag(ctx, tag, b); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveTag(ctx, tag, a); err != nil {
		t.Fatal(err)
	}
}
