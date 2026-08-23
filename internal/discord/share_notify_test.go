package discord

import (
	"strings"
	"testing"

	"github.com/alfred-identity/web/internal/store"
)

func TestFormatShareNotifyMessage(t *testing.T) {
	msg := formatShareNotifyMessage(store.User{
		DiscordID:   "111",
		DisplayName: "Alice",
	}, "eqbox", []string{"tank", "eqbox", " main "})

	if !strings.Contains(msg, "<@111> (Alice)") {
		t.Fatalf("owner mention missing: %q", msg)
	}
	if !strings.Contains(msg, "**Account:** eqbox") {
		t.Fatalf("account missing: %q", msg)
	}
	if !strings.Contains(msg, "**Aliases:** tank, main") {
		t.Fatalf("aliases missing: %q", msg)
	}
	if !strings.Contains(msg, "private share") {
		t.Fatalf("private share note missing: %q", msg)
	}
}

func TestCleanShareAliases(t *testing.T) {
	got := cleanShareAliases("eqbox", []string{"tank", "EQBOX", "", "tank"})
	want := []string{"tank"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("got %#v want %#v", got, want)
	}
}
