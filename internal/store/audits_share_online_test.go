package store_test

import (
	"context"
	"testing"

	"github.com/alfred-identity/web/internal/store"
)

func TestUpdateGroupAndAuditsAndShareOnline(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	u, err := st.UpsertUser(ctx, "aud-"+randHex(4), "Auditor", nil)
	if err != nil {
		t.Fatal(err)
	}
	gname := "ug-" + randHex(4)
	gid, err := st.CreateGroup(ctx, gname, "old", "readonly", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateGroupMeta(ctx, gid, gname, "new-desc", "admin", []string{"sso"}); err != nil {
		t.Fatal(err)
	}
	details, err := st.ListGroupDetails(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range details {
		if d.ID == gid {
			found = true
			if d.Description != "new-desc" || d.WebRole != "admin" {
				t.Fatalf("%#v", d)
			}
		}
	}
	if !found {
		t.Fatal("group missing from details")
	}

	acct, err := st.AddEQAccount(ctx, "audacct_"+randHex(5), "pw", "")
	if err != nil {
		t.Fatal(err)
	}
	st.AuditAccount(ctx, u.ID, acct, "test_action", "detail=1")
	entries, err := st.ListAccountAudits(ctx, acct, 0, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("expected audit rows")
	}

	char := "ShareOnline" + randHex(2)
	if err := st.AddCharacter(ctx, char, acct); err != nil {
		t.Fatal(err)
	}
	owner, err := st.UpsertUser(ctx, "own2-"+randHex(4), "Own", nil)
	if err != nil {
		t.Fatal(err)
	}
	shareName := "shonline_" + randHex(5)
	shareID, _, err := st.ShareLocalAccount(ctx, owner, shareName, "pw", nil, []int64{u.ID}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddCharacter(ctx, "ShChar"+randHex(2), shareID); err != nil {
		t.Fatal(err)
	}
	online, err := st.BuildShareOnline(ctx, owner.ID, []store.PresenceHint{
		{AccountID: shareID, CharacterName: "ShChar", UserID: u.ID},
		{AccountID: acct, CharacterName: char, UserID: u.ID}, // not owned restricted — filtered
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(online) != 1 || online[0].AccountID != shareID {
		t.Fatalf("share online=%#v", online)
	}
}
