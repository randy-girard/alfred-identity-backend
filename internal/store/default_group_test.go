package store_test

import (
	"context"
	"testing"

	"github.com/alfred-identity/web/internal/store"
)

func TestEnsureDefaultGroupAndAssign(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	gid, err := st.EnsureDefaultGroup(ctx)
	if err != nil || gid <= 0 {
		t.Fatalf("EnsureDefaultGroup: id=%d err=%v", gid, err)
	}
	again, err := st.EnsureDefaultGroup(ctx)
	if err != nil || again != gid {
		t.Fatalf("idempotent EnsureDefaultGroup: %d want %d err=%v", again, gid, err)
	}
	found, ok, err := st.FindGroupIDByName(ctx, store.DefaultGroupName)
	if err != nil || !ok || found != gid {
		t.Fatalf("find Default: %d ok=%v err=%v", found, ok, err)
	}

	details, err := st.ListGroupDetails(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var def *store.GroupDetail
	for i := range details {
		if details[i].ID == gid {
			def = &details[i]
			break
		}
	}
	if def == nil || !def.IsDefault {
		t.Fatalf("expected is_default detail: %#v", def)
	}
	if def.WebRole != "" {
		t.Fatalf("web_role=%q want empty", def.WebRole)
	}
	if len(def.DiscordCommands) != 2 || def.DiscordCommands[0] != "sso" || def.DiscordCommands[1] != "whoami" {
		t.Fatalf("discord_commands=%v", def.DiscordCommands)
	}

	u, err := st.UpsertUser(ctx, "def-"+randHex(4), "NoGroups", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureUserInDefaultGroupIfNone(ctx, u); err != nil {
		t.Fatal(err)
	}
	groups, err := st.ListGroupsForUser(ctx, u)
	if err != nil || len(groups) != 1 {
		t.Fatalf("groups=%#v err=%v", groups, err)
	}
	if groups[0]["name"] != store.DefaultGroupName {
		t.Fatalf("name=%v", groups[0]["name"])
	}
	role, err := st.HighestWebRoleForUser(ctx, u)
	if err != nil || role != "" {
		t.Fatalf("web role=%q err=%v (Default must not grant web UI)", role, err)
	}
	okCmd, err := st.UserCanUseDiscordCommand(ctx, u, "sso")
	if err != nil || !okCmd {
		t.Fatalf("Default should grant sso: ok=%v err=%v", okCmd, err)
	}

	if err := st.DeleteGroup(ctx, gid); err == nil {
		t.Fatal("expected delete of Default to fail")
	}
	if _, err := st.CreateGroup(ctx, "Default", "", "admin", nil); err == nil {
		t.Fatal("expected create with reserved name to fail")
	}

	// Already in Default — second call is a no-op.
	if err := st.EnsureUserInDefaultGroupIfNone(ctx, u); err != nil {
		t.Fatal(err)
	}

	// User already in another group is not added to Default.
	other, err := st.UpsertUser(ctx, "def2-"+randHex(4), "HasGroup", nil)
	if err != nil {
		t.Fatal(err)
	}
	ogid, err := st.CreateGroup(ctx, "other-"+randHex(4), "", "admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddGroupUser(ctx, ogid, other.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureUserInDefaultGroupIfNone(ctx, other); err != nil {
		t.Fatal(err)
	}
	groups, err = st.ListGroupsForUser(ctx, other)
	if err != nil || len(groups) != 1 {
		t.Fatalf("should stay in one group: %#v err=%v", groups, err)
	}
	if groups[0]["name"] == store.DefaultGroupName {
		t.Fatal("should not replace existing group with Default")
	}

	// Locked base perms survive UpdateGroupMeta attempts.
	if err := st.UpdateGroupMeta(ctx, gid, "Renamed", "kept desc", "admin", nil); err != nil {
		t.Fatal(err)
	}
	details, err = st.ListGroupDetails(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range details {
		if g.ID != gid {
			continue
		}
		if g.Name != store.DefaultGroupName || g.WebRole != "" || len(g.DiscordCommands) != 2 {
			t.Fatalf("locked fields mutated: %#v", g)
		}
		if g.Description != "kept desc" {
			t.Fatalf("description should update: %q", g.Description)
		}
	}
}
