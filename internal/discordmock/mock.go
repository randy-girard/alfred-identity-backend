package discordmock

// Package discordmock documents Discord-optional mode.
// When DISCORD_ENABLED=false the daemon skips the Discord gateway;
// WS SSO and DB paths still work. Seed users/tokens via SQL or a future admin HTTP API.
const EnabledEnv = "DISCORD_ENABLED"
