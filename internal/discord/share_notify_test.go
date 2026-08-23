package discord

import (
	"strings"
	"testing"

	"github.com/alfred-identity/web/internal/store"
)

func TestFormatShareNotifyMessage(t *testing.T) {
	msg := formatShareNotifyMessage(store.User{
		DisplayName: "Alice",
		DiscordID:   "123",
	}, "tankbox", []string{"tankbox", "tank", "tank", "  "})
	for _, want := range []string{"Alice", "tankbox", "tank", "<@123>"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("missing %q in %s", want, msg)
		}
	}
	// Aliases line should not list the account username itself.
	if strings.Contains(msg, "**Aliases:** tankbox") {
		t.Fatalf("username should not be listed as alias: %s", msg)
	}
	msg2 := formatShareNotifyMessage(store.User{}, "solo", nil)
	if !strings.Contains(msg2, "Someone") || !strings.Contains(msg2, "solo") {
		t.Fatalf("%s", msg2)
	}
}

func TestCleanShareAliases(t *testing.T) {
	got := cleanShareAliases("Main", []string{"Main", "alt", "ALT", "", "box"})
	if len(got) != 2 || got[0] != "alt" || got[1] != "box" {
		t.Fatalf("%#v", got)
	}
}

func TestNotifyAccountSharedNilSafe(t *testing.T) {
	var b *Bot
	b.NotifyAccountShared(nil, store.User{}, "x", nil, []int64{1})
	b = &Bot{}
	b.NotifyAccountShared(nil, store.User{}, "x", nil, nil)
	b.NotifyAccountShared(nil, store.User{}, "x", nil, []int64{1})
}
