package appprocess

import "testing"

func TestParseRole(t *testing.T) {
	got, err := ParseRole(" WEB ")
	if err != nil || got != RoleWeb {
		t.Fatalf("got %q err=%v", got, err)
	}
	got, err = ParseRole("")
	if err != nil || got != RoleAll {
		t.Fatalf("empty: %q %v", got, err)
	}
	if _, err := ParseRole("worker"); err == nil {
		t.Fatal("expected unknown")
	}
}

func TestFromArgsAndEnv(t *testing.T) {
	got, err := FromArgsAndEnv(nil, "")
	if err != nil || got != RoleAll {
		t.Fatalf("default %q %v", got, err)
	}
	got, err = FromArgsAndEnv(nil, "discord")
	if err != nil || got != RoleDiscord {
		t.Fatalf("env %q %v", got, err)
	}
	got, err = FromArgsAndEnv([]string{"-process", "web"}, "discord")
	if err != nil || got != RoleWeb {
		t.Fatalf("flag wins %q %v", got, err)
	}
	got, err = FromArgsAndEnv([]string{"--process=all"}, "web")
	if err != nil || got != RoleAll {
		t.Fatalf("long flag %q %v", got, err)
	}
	if _, err := FromArgsAndEnv([]string{"-process"}, ""); err == nil {
		t.Fatal("expected missing value")
	}
}

func TestRoleWants(t *testing.T) {
	if !RoleWeb.WantsHTTP() || RoleWeb.WantsDiscordGateway() || !RoleWeb.WantsShareREST() {
		t.Fatal("web")
	}
	if RoleDiscord.WantsHTTP() || !RoleDiscord.WantsDiscordGateway() || RoleDiscord.WantsShareREST() {
		t.Fatal("discord")
	}
	if !RoleAll.WantsHTTP() || !RoleAll.WantsDiscordGateway() || RoleAll.WantsShareREST() {
		t.Fatal("all uses in-process gateway for DMs, not a second REST client")
	}
}
