package discord

import (
	"strings"
	"testing"
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
		for _, want := range []string{"create", "revoke", "list", "get"} {
			if !subs[want] {
				t.Fatalf("sso missing subcommand %q", want)
			}
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
