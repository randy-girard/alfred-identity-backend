package sso

import (
	"strings"
	"testing"

	"github.com/alfred-identity/web/internal/store"
)

func TestValidateFields(t *testing.T) {
	if err := validateUsername(""); err == nil || err.Error() != "username_required" {
		t.Fatalf("empty user: %v", err)
	}
	if err := validateUsername(strings.Repeat("a", 65)); err == nil {
		t.Fatal("expected too long")
	}
	if err := validateUsername("bad\nname"); err == nil {
		t.Fatal("expected invalid")
	}
	if err := validateUsername("ok"); err != nil {
		t.Fatal(err)
	}

	if err := validatePassword(""); err == nil {
		t.Fatal("password required")
	}
	if err := validatePassword(strings.Repeat("p", 129)); err == nil {
		t.Fatal("password too long")
	}
	if err := validatePassword("ok"); err != nil {
		t.Fatal(err)
	}

	if err := validateAlias(""); err == nil {
		t.Fatal("alias required")
	}
	if err := validateAlias(strings.Repeat("a", 65)); err == nil {
		t.Fatal("alias too long")
	}
	if err := validateAlias("bad\ralias"); err == nil {
		t.Fatal("alias invalid")
	}
	if err := validateAlias("ok"); err != nil {
		t.Fatal(err)
	}
	if err := validateTag(""); err == nil {
		t.Fatal("tag required")
	}
	if err := validateTag(strings.Repeat("t", 65)); err == nil {
		t.Fatal("tag too long")
	}
	if err := validateTag("bad\ntag"); err == nil {
		t.Fatal("tag invalid")
	}
	if err := validateCharacter(""); err == nil {
		t.Fatal("char required")
	}
	if err := validateCharacter(strings.Repeat("c", 65)); err == nil {
		t.Fatal("char too long")
	}
	if err := validateCharacter("Hero"); err != nil {
		t.Fatal(err)
	}
	if err := validatePassword("a\x00b"); err == nil {
		t.Fatal("password nul")
	}
}

func TestUserIsAdmin(t *testing.T) {
	h := &Hub{
		BootstrapAdminIDs: []string{"boot"},
		AdminRoleID:       "admin-role",
	}
	if !h.IsAdmin(store.User{DiscordID: "boot"}) {
		t.Fatal("bootstrap")
	}
	if !h.IsAdmin(store.User{DiscordID: "u", RoleIDs: []string{"admin-role"}}) {
		t.Fatal("role")
	}
	if h.IsAdmin(store.User{DiscordID: "u", RoleIDs: []string{"other"}}) {
		t.Fatal("should not be admin")
	}
	h2 := &Hub{BootstrapAdminIDs: nil, AdminRoleID: ""}
	if h2.IsAdmin(store.User{DiscordID: "u", RoleIDs: []string{"x"}}) {
		t.Fatal("empty admin role")
	}
}
