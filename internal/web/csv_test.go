package web

import (
	"strings"
	"testing"
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
