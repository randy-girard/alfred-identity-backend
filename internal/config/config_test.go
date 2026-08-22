package config

import (
	"encoding/base64"
	"testing"
)

func TestNormalizeAndValidateDiscordCommandPrefix(t *testing.T) {
	p := NormalizeDiscordCommandPrefix("  Alfred-Identity- ")
	if p != "alfred-identity-" {
		t.Fatalf("got %q", p)
	}
	if err := ValidateDiscordCommandPrefix(p); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDiscordCommandPrefix(""); err == nil {
		t.Fatal("expected empty error")
	}
	if err := ValidateDiscordCommandPrefix("bad prefix!"); err == nil {
		t.Fatal("expected invalid chars error")
	}
	if err := ValidateDiscordCommandPrefix("this-prefix-is-way-too-long-xx"); err == nil {
		t.Fatal("expected too long error")
	}
}

func TestEnvHelpers(t *testing.T) {
	t.Setenv("AI_TEST_OR", "  hello  ")
	if got := envOr("AI_TEST_OR", "def"); got != "hello" {
		t.Fatalf("envOr set: %q", got)
	}
	t.Setenv("AI_TEST_OR_EMPTY", "")
	if got := envOr("AI_TEST_OR_EMPTY", "def"); got != "def" {
		t.Fatalf("envOr empty: %q", got)
	}

	t.Setenv("AI_TEST_INT", "42")
	if got := envInt("AI_TEST_INT", 7); got != 42 {
		t.Fatalf("envInt: %d", got)
	}
	t.Setenv("AI_TEST_INT_BAD", "nope")
	if got := envInt("AI_TEST_INT_BAD", 7); got != 7 {
		t.Fatalf("envInt bad: %d", got)
	}
	t.Setenv("AI_TEST_INT_EMPTY", "")
	if got := envInt("AI_TEST_INT_EMPTY", 9); got != 9 {
		t.Fatalf("envInt empty: %d", got)
	}

	t.Setenv("AI_TEST_BOOL", "true")
	if !envBool("AI_TEST_BOOL", false) {
		t.Fatal("envBool true")
	}
	t.Setenv("AI_TEST_BOOL", "1")
	if !envBool("AI_TEST_BOOL", false) {
		t.Fatal("envBool 1")
	}
	t.Setenv("AI_TEST_BOOL", "yes")
	if !envBool("AI_TEST_BOOL", false) {
		t.Fatal("envBool yes")
	}
	t.Setenv("AI_TEST_BOOL", "false")
	if envBool("AI_TEST_BOOL", true) {
		t.Fatal("envBool false should be false")
	}
	t.Setenv("AI_TEST_BOOL_EMPTY", "")
	if !envBool("AI_TEST_BOOL_EMPTY", true) {
		t.Fatal("envBool empty uses default")
	}

	got := splitCSV(" a, b,,c ")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("splitCSV: %#v", got)
	}
	if len(splitCSV("")) != 0 {
		t.Fatal("empty csv")
	}
}

func TestLoadMinimal(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	t.Setenv("DATA_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("DISCORD_ENABLED", "false")
	t.Setenv("WEB_ENABLED", "false")
	t.Setenv("HTTP_ADDR", "127.0.0.1:9090")
	t.Setenv("WEB_SSO_SOURCE_NAME", "Test Guild")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != "127.0.0.1:9090" {
		t.Fatalf("HTTPAddr %q", cfg.HTTPAddr)
	}
	if cfg.WebSSOSourceName != "Test Guild" {
		t.Fatalf("source name %q", cfg.WebSSOSourceName)
	}
	if cfg.DiscordEnabled || cfg.WebEnabled {
		t.Fatal("discord/web should be off")
	}
}

func TestLoadSSOSourceNamePrefersSSO_SOURCE_NAME(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	t.Setenv("DATA_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("DISCORD_ENABLED", "false")
	t.Setenv("WEB_ENABLED", "false")
	t.Setenv("SSO_SOURCE_NAME", "Guild SSO")
	t.Setenv("WEB_SSO_SOURCE_NAME", "Legacy Name")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WebSSOSourceName != "Guild SSO" {
		t.Fatalf("source name %q", cfg.WebSSOSourceName)
	}
}

func TestLoadRequiresEncryptionKey(t *testing.T) {
	t.Setenv("DATA_ENCRYPTION_KEY", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadWebRequiresDiscord(t *testing.T) {
	key := make([]byte, 32)
	t.Setenv("DATA_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("DISCORD_ENABLED", "false")
	t.Setenv("WEB_ENABLED", "true")
	t.Setenv("WEB_PUBLIC_URL", "http://127.0.0.1:8181")
	if _, err := Load(); err == nil {
		t.Fatal("expected WEB_ENABLED requires Discord")
	}
}

func TestLoadDiscordRequiresToken(t *testing.T) {
	key := make([]byte, 32)
	t.Setenv("DATA_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("DISCORD_ENABLED", "true")
	t.Setenv("DISCORD_TOKEN", "")
	t.Setenv("WEB_ENABLED", "false")
	if _, err := Load(); err == nil {
		t.Fatal("expected DISCORD_TOKEN required")
	}
}

func TestLoadWebHappyAndFallbacks(t *testing.T) {
	key := make([]byte, 32)
	t.Setenv("DATA_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("DISCORD_ENABLED", "true")
	t.Setenv("DISCORD_TOKEN", "tok")
	t.Setenv("WEB_ENABLED", "true")
	t.Setenv("DISCORD_CLIENT_ID", "cid")
	t.Setenv("DISCORD_CLIENT_SECRET", "sec")
	t.Setenv("DISCORD_GUILD_ID", "gid")
	t.Setenv("WEB_PUBLIC_URL", "https://identity.example.com/")
	t.Setenv("DISCORD_ADMIN_ROLE_ID", "admin-role")
	t.Setenv("WEB_ACCESS_ROLE_ID", "")
	t.Setenv("DISCORD_BOOTSTRAP_ADMIN_IDS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WebPublicURL != "https://identity.example.com" {
		t.Fatalf("public url %q", cfg.WebPublicURL)
	}
	if cfg.WebAccessRoleID != "admin-role" {
		t.Fatalf("fallback access role %q", cfg.WebAccessRoleID)
	}
}

func TestLoadWebRequiresAccessPath(t *testing.T) {
	key := make([]byte, 32)
	t.Setenv("DATA_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("DISCORD_ENABLED", "true")
	t.Setenv("DISCORD_TOKEN", "tok")
	t.Setenv("WEB_ENABLED", "true")
	t.Setenv("DISCORD_CLIENT_ID", "cid")
	t.Setenv("DISCORD_CLIENT_SECRET", "sec")
	t.Setenv("DISCORD_GUILD_ID", "gid")
	t.Setenv("WEB_PUBLIC_URL", "http://127.0.0.1:8181")
	t.Setenv("DISCORD_ADMIN_ROLE_ID", "")
	t.Setenv("WEB_ACCESS_ROLE_ID", "")
	t.Setenv("DISCORD_BOOTSTRAP_ADMIN_IDS", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected access role / bootstrap required")
	}

	t.Setenv("DISCORD_BOOTSTRAP_ADMIN_IDS", "111,222")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.DiscordBootstrapAdmins) != 2 {
		t.Fatalf("%#v", cfg.DiscordBootstrapAdmins)
	}
}

func TestLoadWebMissingClient(t *testing.T) {
	key := make([]byte, 32)
	t.Setenv("DATA_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("DISCORD_ENABLED", "true")
	t.Setenv("DISCORD_TOKEN", "tok")
	t.Setenv("WEB_ENABLED", "true")
	t.Setenv("DISCORD_CLIENT_ID", "")
	t.Setenv("DISCORD_CLIENT_SECRET", "")
	t.Setenv("DISCORD_GUILD_ID", "gid")
	t.Setenv("WEB_PUBLIC_URL", "http://127.0.0.1:8181")
	t.Setenv("DISCORD_BOOTSTRAP_ADMIN_IDS", "1")
	if _, err := Load(); err == nil {
		t.Fatal("expected client id/secret required")
	}
}

func TestLoadKeyAndWebURLGates(t *testing.T) {
	t.Setenv("DATA_ENCRYPTION_KEY", "!!!not-base64!!!")
	if _, err := Load(); err == nil {
		t.Fatal("expected bad base64")
	}
	t.Setenv("DATA_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte("short")))
	if _, err := Load(); err == nil {
		t.Fatal("expected wrong key length")
	}

	key := make([]byte, 32)
	t.Setenv("DATA_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("DISCORD_ENABLED", "true")
	t.Setenv("DISCORD_TOKEN", "tok")
	t.Setenv("WEB_ENABLED", "true")
	t.Setenv("DISCORD_CLIENT_ID", "cid")
	t.Setenv("DISCORD_CLIENT_SECRET", "sec")
	t.Setenv("DISCORD_GUILD_ID", "")
	t.Setenv("WEB_PUBLIC_URL", "http://127.0.0.1:8181")
	t.Setenv("DISCORD_BOOTSTRAP_ADMIN_IDS", "1")
	if _, err := Load(); err == nil {
		t.Fatal("expected guild required")
	}
	t.Setenv("DISCORD_GUILD_ID", "gid")
	t.Setenv("WEB_PUBLIC_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected public url required")
	}
}
