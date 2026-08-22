package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DatabaseURL            string
	HTTPAddr               string
	WSPath                 string
	ProtocolVersion        int
	DataEncryptionKey      []byte
	DiscordEnabled         bool
	DiscordToken           string
	DiscordClientID        string
	DiscordClientSecret    string
	DiscordGuildID         string
	DiscordAdminRoleID     string
	DiscordBootstrapAdmins []string
	DiscordCommandPrefix   string
	DiscordRoleSyncEvery   time.Duration
	WebEnabled             bool
	WebPublicURL           string
	WebAccessRoleID        string // empty → DiscordAdminRoleID
	WebSSOSourceName       string // display name in Discord /sso get JSON and /sso-source.json (SSO_SOURCE_NAME or WEB_SSO_SOURCE_NAME)
	PresenceTTL            time.Duration
	LoginAuthRatePerMin    int
}

const DefaultDiscordCommandPrefix = "alfred-identity-"

func Load() (Config, error) {
	keyB64 := strings.TrimSpace(os.Getenv("DATA_ENCRYPTION_KEY"))
	if keyB64 == "" {
		return Config{}, fmt.Errorf("DATA_ENCRYPTION_KEY is required (32-byte key, base64)")
	}
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return Config{}, fmt.Errorf("DATA_ENCRYPTION_KEY: %w", err)
	}
	if len(key) != 32 {
		return Config{}, fmt.Errorf("DATA_ENCRYPTION_KEY must decode to 32 bytes, got %d", len(key))
	}

	cfg := Config{
		DatabaseURL:            envOr("DATABASE_URL", "postgres://alfred:alfred@localhost:5432/alfred_identity?sslmode=disable"),
		HTTPAddr:               envOr("HTTP_ADDR", "0.0.0.0:8080"),
		WSPath:                 envOr("WS_PATH", "/ws/sso"),
		ProtocolVersion:        envInt("PROTOCOL_VERSION", 1),
		DataEncryptionKey:      key,
		DiscordEnabled:         envBool("DISCORD_ENABLED", false),
		DiscordToken:           os.Getenv("DISCORD_TOKEN"),
		DiscordClientID:        os.Getenv("DISCORD_CLIENT_ID"),
		DiscordClientSecret:    os.Getenv("DISCORD_CLIENT_SECRET"),
		DiscordGuildID:         os.Getenv("DISCORD_GUILD_ID"),
		DiscordAdminRoleID:     os.Getenv("DISCORD_ADMIN_ROLE_ID"),
		DiscordBootstrapAdmins: splitCSV(os.Getenv("DISCORD_BOOTSTRAP_ADMIN_IDS")),
		DiscordCommandPrefix:   NormalizeDiscordCommandPrefix(envOr("DISCORD_COMMAND_PREFIX", DefaultDiscordCommandPrefix)),
		DiscordRoleSyncEvery:   time.Duration(envInt("DISCORD_ROLE_SYNC_SECONDS", 300)) * time.Second,
		WebEnabled:             envBool("WEB_ENABLED", false),
		WebPublicURL:           strings.TrimRight(strings.TrimSpace(os.Getenv("WEB_PUBLIC_URL")), "/"),
		WebAccessRoleID:        strings.TrimSpace(os.Getenv("WEB_ACCESS_ROLE_ID")),
		WebSSOSourceName:       firstNonEmpty(strings.TrimSpace(os.Getenv("SSO_SOURCE_NAME")), strings.TrimSpace(os.Getenv("WEB_SSO_SOURCE_NAME"))),
		PresenceTTL:            time.Duration(envInt("PRESENCE_TTL_SECONDS", 90)) * time.Second,
		LoginAuthRatePerMin:    envInt("LOGIN_AUTH_RATE_LIMIT_PER_MIN", 30),
	}
	if cfg.DiscordEnabled && cfg.DiscordToken == "" {
		return Config{}, fmt.Errorf("DISCORD_TOKEN required when DISCORD_ENABLED=true")
	}
	if err := ValidateDiscordCommandPrefix(cfg.DiscordCommandPrefix); err != nil {
		return Config{}, err
	}
	if cfg.WebEnabled {
		if !cfg.DiscordEnabled {
			return Config{}, fmt.Errorf("WEB_ENABLED requires DISCORD_ENABLED=true")
		}
		if cfg.DiscordClientID == "" || cfg.DiscordClientSecret == "" {
			return Config{}, fmt.Errorf("DISCORD_CLIENT_ID and DISCORD_CLIENT_SECRET required when WEB_ENABLED=true")
		}
		if cfg.DiscordGuildID == "" {
			return Config{}, fmt.Errorf("DISCORD_GUILD_ID required when WEB_ENABLED=true")
		}
		if cfg.WebPublicURL == "" {
			return Config{}, fmt.Errorf("WEB_PUBLIC_URL required when WEB_ENABLED=true (e.g. https://identity.example.com)")
		}
		if cfg.WebAccessRoleID == "" {
			cfg.WebAccessRoleID = cfg.DiscordAdminRoleID
		}
		if cfg.WebAccessRoleID == "" && len(cfg.DiscordBootstrapAdmins) == 0 {
			return Config{}, fmt.Errorf("WEB_ACCESS_ROLE_ID or DISCORD_ADMIN_ROLE_ID or DISCORD_BOOTSTRAP_ADMIN_IDS required when WEB_ENABLED=true")
		}
	}
	return cfg, nil
}

// NormalizeDiscordCommandPrefix lowercases and trims the slash-command name prefix.
func NormalizeDiscordCommandPrefix(p string) string {
	return strings.ToLower(strings.TrimSpace(p))
}

// ValidateDiscordCommandPrefix ensures Discord name rules and room for longest suffix ("whoami").
func ValidateDiscordCommandPrefix(p string) error {
	if p == "" {
		return fmt.Errorf("DISCORD_COMMAND_PREFIX must not be empty")
	}
	for _, r := range p {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return fmt.Errorf("DISCORD_COMMAND_PREFIX %q has invalid characters (use a-z, 0-9, -, _)", p)
	}
	const longest = "whoami" // alfred-identity-whoami
	if len(p)+len(longest) > 32 {
		return fmt.Errorf("DISCORD_COMMAND_PREFIX too long (%d); prefix+command must be ≤32 chars", len(p))
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func envBool(k string, def bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(k)))
	if v == "" {
		return def
	}
	return v == "1" || v == "true" || v == "yes"
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
