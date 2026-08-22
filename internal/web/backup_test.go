package web

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestConfigBackupJSONRoundTrip(t *testing.T) {
	bak := ConfigBackup{
		Version:    1,
		ExportedAt: time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC),
		Users: []ConfigBackupUser{{
			DiscordID: "111", DisplayName: "Alice", RoleIDs: []string{"r1"}, AccessRevoked: false,
		}},
		Groups: []ConfigBackupGroup{{
			Name: "Officers", WebRole: "admin",
			MemberDiscordIDs: []string{"111"}, MemberRoleIDs: []string{},
			AccountUsernames: []string{"tank"},
		}},
		Accounts: []ConfigBackupAccount{{
			Username: "tank", Password: "secret",
			Aliases: []string{"box"}, Tags: []string{"raid"}, Characters: []string{"Hero"},
			GroupNames: []string{"Officers"},
		}},
	}
	b, err := json.Marshal(bak)
	if err != nil {
		t.Fatal(err)
	}
	var got ConfigBackup
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || len(got.Users) != 1 || got.Users[0].DiscordID != "111" {
		t.Fatalf("users: %+v", got.Users)
	}
	if len(got.Groups) != 1 || got.Groups[0].WebRole != "admin" {
		t.Fatalf("groups: %+v", got.Groups)
	}
	if got.Accounts[0].Password != "secret" || !strings.EqualFold(got.Accounts[0].Username, "tank") {
		t.Fatalf("accounts: %+v", got.Accounts)
	}
}

func TestDecodeConfigBackup(t *testing.T) {
	ok, err := decodeConfigBackup(strings.NewReader(`{"version":1,"users":[]}`))
	if err != nil || ok.Version != 1 {
		t.Fatalf("%+v err=%v", ok, err)
	}
	norm, err := decodeConfigBackup(strings.NewReader(`{"users":[]}`))
	if err != nil || norm.Version != 1 {
		t.Fatalf("normalize version: %+v err=%v", norm, err)
	}
	if _, err := decodeConfigBackup(strings.NewReader(`{"version":99}`)); err == nil {
		t.Fatal("expected unsupported version")
	}
	if _, err := decodeConfigBackup(strings.NewReader(`{`)); err == nil {
		t.Fatal("expected invalid json")
	}
}
