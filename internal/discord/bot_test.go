package discord

import (
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func TestCommandDefsKeepsSSOHelpersOnly(t *testing.T) {
	cmds := commandDefs("alfred-identity-")
	if len(cmds) != 2 {
		t.Fatalf("want 2 commands, got %d", len(cmds))
	}
	names := map[string]bool{}
	for _, c := range cmds {
		names[c.Name] = true
	}
	for _, want := range []string{
		"alfred-identity-sso",
		"alfred-identity-whoami",
	} {
		if !names[want] {
			t.Fatalf("missing command %q in %#v", want, names)
		}
	}
	for _, banned := range []string{
		"account", "group", "alias", "tag", "character", "status", "roles",
	} {
		for name := range names {
			if strings.HasSuffix(name, "-"+banned) {
				t.Fatalf("unexpected management command still registered: %s", name)
			}
		}
	}
}

func TestCommandDefsSSOSubcommands(t *testing.T) {
	cmds := commandDefs("test-")
	var found bool
	for _, c := range cmds {
		if c.Name != "test-sso" {
			continue
		}
		found = true
		subs := map[string]bool{}
		for _, o := range c.Options {
			subs[o.Name] = true
		}
		for _, want := range []string{"get", "revoke", "list"} {
			if !subs[want] {
				t.Fatalf("sso missing subcommand %q", want)
			}
		}
		if subs["create"] {
			t.Fatal("create subcommand should be removed")
		}
	}
	if !found {
		t.Fatal("test-sso missing")
	}
}

func TestCmdAndSlash(t *testing.T) {
	b := &Bot{}
	b.Cfg.DiscordCommandPrefix = "alfred-identity-"
	if got := b.cmd("sso"); got != "alfred-identity-sso" {
		t.Fatalf("cmd: %q", got)
	}
	if got := b.slash("sso"); got != "/alfred-identity-sso" {
		t.Fatalf("slash: %q", got)
	}
}

func TestFmtTimeAndOptInt(t *testing.T) {
	ts := time.Date(2024, 6, 1, 12, 30, 0, 0, time.UTC)
	if got := fmtTime(ts); got != "2024-06-01 12:30 UTC" {
		t.Fatalf("fmtTime: %q", got)
	}
	if got := fmtTime("plain"); got != "plain" {
		t.Fatalf("fmtTime fallback: %q", got)
	}

	sub := &discordgo.ApplicationCommandInteractionDataOption{
		Options: []*discordgo.ApplicationCommandInteractionDataOption{
			{Name: "limit", Type: discordgo.ApplicationCommandOptionInteger, Value: float64(5)},
		},
	}
	if got := optInt(sub, "limit"); got != 5 {
		t.Fatalf("optInt: %d", got)
	}
	if got := optInt(sub, "missing"); got != 0 {
		t.Fatalf("missing: %d", got)
	}
}

func TestInteractionIdentity(t *testing.T) {
	id, name, roles := interactionIdentity(&discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "1", Username: "alice"},
				Roles: []string{"r1"},
			},
		},
	})
	if id != "1" || name != "alice" || len(roles) != 1 {
		t.Fatalf("%s %s %#v", id, name, roles)
	}
	id, name, roles = interactionIdentity(&discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			User: &discordgo.User{ID: "2", Username: "bob"},
		},
	})
	if id != "2" || name != "bob" || roles != nil {
		t.Fatalf("%s %s %#v", id, name, roles)
	}
	id, name, roles = interactionIdentity(&discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{},
	})
	if id != "" || name != "" || roles != nil {
		t.Fatalf("empty: %s %s %#v", id, name, roles)
	}
}
