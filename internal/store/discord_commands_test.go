package store

import "testing"

func TestNormalizeDiscordCommands(t *testing.T) {
	got, err := NormalizeDiscordCommands([]string{"whoami", "sso", "sso", " WHOAMI "})
	if err != nil || len(got) != 2 || got[0] != "sso" || got[1] != "whoami" {
		t.Fatalf("got=%v err=%v", got, err)
	}
	if _, err := NormalizeDiscordCommands([]string{"nope"}); err == nil {
		t.Fatal("expected error")
	}
}
