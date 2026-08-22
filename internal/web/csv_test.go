package web

import (
	"strings"
	"testing"

	"github.com/alfred-identity/web/internal/store"
)

func TestParseSSOAccountsCSV(t *testing.T) {
	in := `username,password,role,aliases,tags,characters
acct1,secret1,123456789012345678,"tank, box","raid|alt","Hero, AltTwo"
acct2,secret2,Officer Role,solo,box,Main
`
	rows, err := parseSSOAccountsCSV(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows", len(rows))
	}
	if rows[0].Username != "acct1" || rows[0].Password != "secret1" {
		t.Fatalf("row0 credentials: %+v", rows[0])
	}
	if rows[0].Role != "123456789012345678" {
		t.Fatalf("role: %q", rows[0].Role)
	}
	if len(rows[0].Aliases) != 2 || rows[0].Aliases[0] != "tank" {
		t.Fatalf("aliases: %#v", rows[0].Aliases)
	}
	if len(rows[0].Tags) != 2 {
		t.Fatalf("tags: %#v", rows[0].Tags)
	}
	if len(rows[0].Characters) != 2 || rows[0].Characters[1] != "AltTwo" {
		t.Fatalf("chars: %#v", rows[0].Characters)
	}
	if rows[1].Role != "Officer Role" || len(rows[1].Aliases) != 1 {
		t.Fatalf("row1: %+v", rows[1])
	}
}

func TestSplitMultiPipe(t *testing.T) {
	got := splitMulti("a|b|a")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("%#v", got)
	}
}

func TestResolveRoleIDAndSnowflake(t *testing.T) {
	roles := []store.DiscordRole{
		{ID: "111111111111111111", Name: "Officer"},
		{ID: "222222222222222222", Name: "Member"},
	}
	id, err := resolveRoleID("", roles)
	if err != nil || id != "" {
		t.Fatalf("empty: %q %v", id, err)
	}
	id, err = resolveRoleID("111111111111111111", roles)
	if err != nil || id != "111111111111111111" {
		t.Fatalf("by id: %q %v", id, err)
	}
	id, err = resolveRoleID("officer", roles)
	if err != nil || id != "111111111111111111" {
		t.Fatalf("by name: %q %v", id, err)
	}
	id, err = resolveRoleID("333333333333333333", roles)
	if err != nil || id != "333333333333333333" {
		t.Fatalf("raw snowflake: %q %v", id, err)
	}
	if _, err := resolveRoleID("Nope", roles); err == nil {
		t.Fatal("expected unknown")
	}
	if !isDiscordSnowflake("12345678901234567") {
		t.Fatal("17 digits")
	}
	if isDiscordSnowflake("123") || isDiscordSnowflake("abcdefghijklmnopq") {
		t.Fatal("invalid snowflakes")
	}
}

func TestRoleLabelAndJoinMulti(t *testing.T) {
	roles := []store.DiscordRole{{ID: "1", Name: "Officer"}, {ID: "2", Name: ""}}
	if got := roleLabel("1", roles); got != "Officer" {
		t.Fatalf("%q", got)
	}
	if got := roleLabel("2", roles); got != "2" {
		t.Fatalf("empty name fallback: %q", got)
	}
	if got := roleLabel("9", roles); got != "9" {
		t.Fatalf("unknown: %q", got)
	}
	if joinMulti(nil) != "" || joinMulti([]string{}) != "" {
		t.Fatal("empty join")
	}
	if got := joinMulti([]string{"a", "b"}); got != "a,b" {
		t.Fatalf("%q", got)
	}
}

func TestParseSSOAccountsCSVEmpty(t *testing.T) {
	if _, err := parseSSOAccountsCSV(strings.NewReader("username,password\n")); err == nil {
		t.Fatal("expected no rows")
	}
}

func TestParseSSOAccountsCSVSkipsBlanks(t *testing.T) {
	in := "name,password\n,secret\nacct,pw\n"
	rows, err := parseSSOAccountsCSV(strings.NewReader(in))
	if err != nil || len(rows) != 1 || rows[0].Username != "acct" {
		t.Fatalf("%+v err=%v", rows, err)
	}
}
