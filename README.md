# Alfred Identity Backend

Go daemon for Discord-managed EQ bot/SSO accounts, Postgres-backed secrets (AES-GCM), a WebSocket SSO API for the **[alfred-identity](https://github.com/randy-girard/alfred-identity)** desktop GUI, and an optional browser admin UI (**Alfred Identity Management**).

## Requirements

- Go 1.26+ (see `go.mod`)
- Docker + Docker Compose (recommended), or Postgres + a local binary build
- Discord application (when `DISCORD_ENABLED=true`)

## Project layout

```
cmd/
  daemon/               # HTTP + WebSocket + Discord bot entrypoint
  seedtoken/            # Bootstrap SSO token when Discord is disabled
internal/
  config/               # Environment loading and validation
  crypto/               # AES-GCM for stored passwords
  db/                   # Postgres connect + SQL migrations
  discord/              # Slash commands (/sso, /whoami)
  httpapi/              # Shared HTTP helpers (health, request logging)
  metrics/              # DB latency / pool / live client sampling
  presence/             # EQ heartbeat / online tracking
  sso/                  # WebSocket hub + admin API for GUI clients
  store/                # Postgres data access layer
  web/                  # Discord OAuth admin UI + static SPA
docs/                   # Discord setup, web admin, TLS deploy
scripts/                # build.sh, test.sh, coverage HTML generator
```

Unit tests live next to each package under `internal/` (co-located `*_test.go` files). `cmd/` is excluded from `make test`.

## Quick start (Docker Compose)

```bash
cd alfred-identity-backend
cp .env.example .env
# Required: DATA_ENCRYPTION_KEY (32 bytes base64)
openssl rand -base64 32
# Optional: Discord token, guild, bootstrap admin IDs — or DISCORD_ENABLED=false
docker compose up --build
```

Compose maps host **8181** → container `8080`:

| Endpoint | URL |
|----------|-----|
| Health | `GET http://127.0.0.1:8181/health` |
| SSO WebSocket | `ws://127.0.0.1:8181/ws/sso` |
| Web admin (when `WEB_ENABLED=true`) | `http://127.0.0.1:8181/admin/` |

In production, terminate TLS at a reverse proxy and point the GUI at `wss://…/ws/sso`. See [docs/deploy-tls.md](docs/deploy-tls.md).

## Build (binary only)

```bash
./scripts/build.sh
# → bin/daemon
```

Requires `DATA_ENCRYPTION_KEY` and `DATABASE_URL` in the environment (or `.env` via `godotenv`).

## Tests and coverage

```bash
make test          # unit tests (race detector)
make coverage      # writes coverage/index.html (+ source.html, func.txt)
```

Open `coverage/index.html` in a browser.

Store integration tests (`TestShare*`, `TestToken*`) run when `TEST_DATABASE_URL` or `DATABASE_URL` points at Postgres; otherwise they skip. CI runs them in a separate job with a Postgres service.

---

## Help — operating the daemon

### What this service does

1. **Stores** guild EQ accounts (encrypted passwords), users, access groups, aliases/tags/characters, and SSO API tokens in Postgres.
2. **Authenticates** desktop GUI clients over `ws://host/ws/sso` using per-user API tokens.
3. **Resolves logins** when the GUI proxy asks for credentials (`login_auth` over the WebSocket).
4. **Optionally** runs a Discord bot for SSO tokens and identity lookup.
5. **Optionally** serves **Alfred Identity Management** at `/admin/` for the same data as the GUI admin features.

Mutations from the web admin or GUI admin API broadcast `full_state` to all connected GUI clients immediately.

### First-time setup

1. Copy `.env.example` → `.env` and set `DATA_ENCRYPTION_KEY`.
2. Start with `docker compose up --build`.
3. **With Discord:** follow [Discord bot setup](#discord-bot-setup) below (full detail: [docs/discord-bot-setup.md](docs/discord-bot-setup.md)).
4. **Without Discord:** leave `DISCORD_ENABLED=false`, then:
   ```bash
   go run ./cmd/seedtoken <discord_id> <display_name>
   ```
   Use the printed `token=` value in Alfred Identity source JSON (see below).
5. Add EQ accounts in **Alfred Identity Management** (`/admin/`) or from the **Alfred Identity** GUI (when connected as admin).
6. Users connect the **Alfred Identity** desktop app with **Login w/ SSO** and a source JSON that includes `host` + `token`.

### SSO token for the desktop GUI

**With Discord enabled:** run `/alfred-identity-sso get` (subcommand **`get`**, not `create` — it returns an existing token or creates one). The bot replies with the secret and **Alfred Identity source JSON**. Paste that JSON into the GUI → **Connections** → **Add from JSON**. One active token per Discord user (`get` again returns the same token; use `revoke` then `get` to rotate).

**Without Discord** (`DISCORD_ENABLED=false`):

```bash
go run ./cmd/seedtoken 123456789012345678 "Local User"
```

Build source JSON manually (host is your public `host:port`, e.g. `127.0.0.1:8181`):

```json
{
  "name": "Local daemon",
  "host": "127.0.0.1:8181",
  "token": "<token from seedtoken output>"
}
```

Set **`SSO_SOURCE_NAME`** in `.env` to customize the `name` field in Discord `/sso get` JSON (legacy alias: `WEB_SSO_SOURCE_NAME`). Defaults to `Local daemon` for localhost.

### Access model

| Access | Who can log in via SSO |
|--------|-------------------------|
| **Base** (default) | Anyone with a valid SSO token |
| **Elevated** | SSO token **and** the Discord role set on that account |
| **Group / user grants** | OR-combined with role requirements on each account |
| **Private share** | Owner-granted copies from another user's Local → Share (users, Discord roles, and/or access groups) |

Discord roles for elevated access sync automatically (periodic + member events). See [docs/discord-bot-setup.md](docs/discord-bot-setup.md).

### Discord bot setup

Use this when `DISCORD_ENABLED=true`. Step-by-step with screenshots-level detail: [docs/discord-bot-setup.md](docs/discord-bot-setup.md).

#### Developer Portal

1. [Create an application](https://discord.com/developers/applications) → copy **Application ID** → `DISCORD_CLIENT_ID`.
2. **Bot** → Add Bot → copy token → `DISCORD_TOKEN`.
3. **Bot → Privileged Gateway Intents** — turn **ON** only **Server Members Intent** (role sync). Leave Presence and Message Content **OFF**.

#### Invite the bot (permissions)

**OAuth2 → URL Generator:**

| Setting | Value |
|---------|--------|
| Scopes | `bot`, `applications.commands` |
| Bot permissions | View Channels, Send Messages, Embed Links, Use Application Commands |

Or open this URL (replace `YOUR_CLIENT_ID`):

```
https://discord.com/api/oauth2/authorize?client_id=YOUR_CLIENT_ID&permissions=2147503104&scope=bot%20applications.commands
```

| Permission | Why |
|------------|-----|
| View Channels | Baseline guild access |
| Send Messages | DMs when a private account is shared with someone |
| Embed Links | Ephemeral `/sso` and `/whoami` replies |
| Use Application Commands | Slash commands in your guild |

Do **not** grant Administrator — the bot only reads roles and sends slash/DM messages.

After inviting, set in `.env`:

- `DISCORD_GUILD_ID` — right-click your server → Copy Server ID (Developer Mode on)
- `DISCORD_ADMIN_ROLE_ID` — operator role snowflake
- `DISCORD_BOOTSTRAP_ADMIN_IDS` — your user snowflake(s), comma-separated
- `DISCORD_ENABLED=true`

Restart the daemon. You should see `discord ready` in logs and slash commands `/alfred-identity-sso` / `/alfred-identity-whoami` in the guild.

**Share DMs:** recipients must allow *Direct messages from server members* in Discord privacy settings.

**Web admin (optional):** separate user OAuth — `identify` + `guilds.members.read`, redirect `{WEB_PUBLIC_URL}/admin/oauth/callback`. See [docs/web-admin.md](docs/web-admin.md).

### Discord slash commands

Prefix: `DISCORD_COMMAND_PREFIX` (default `alfred-identity-`).

| Command | Purpose |
|---------|---------|
| `/{prefix}sso get` | Get or create your SSO token + GUI source JSON |
| `/{prefix}sso list` | Active token metadata (no secret) |
| `/{prefix}sso revoke` | Revoke your token (optional `id`) |
| `/{prefix}whoami` | Identity, cached roles, SSO account count |

**Group restrictions:** on the web admin **Groups** tab, you can limit which Discord users may run `/sso` or `/whoami`. When any group enables a command restriction, only members of groups granted that command can use it. Bootstrap admins and the configured admin role bypass restrictions.

EQ accounts, aliases, tags, characters, groups, and user access are managed in **Alfred Identity** or **Alfred Identity Management** — not via Discord (except SSO tokens).

### Web admin (Alfred Identity Management)

Enable with `WEB_ENABLED=true`, Discord OAuth credentials, and `WEB_PUBLIC_URL`. Full setup: [docs/web-admin.md](docs/web-admin.md).

| Tab | Purpose |
|-----|---------|
| **Overview** | Counts, live GUI connections, in-game sessions, metrics charts |
| **Accounts** | EQ accounts; role/user/group access; aliases, tags, characters; CSV import/export |
| **Users** | Revoke/restore access; edit cached Discord role grants |
| **Groups** | Access groups (users + roles → accounts); **Discord slash command** restrictions |
| **Shared accounts** | Private Local → Share copies |
| **Connections** | Live SSO WebSocket clients |
| **Sessions** | Live EQ presence (heartbeats) |
| **Audit log** | Admin action history |
| **Settings** | Full JSON backup export / import (tokens are not migrated) |

### Key environment variables

| Variable | Purpose |
|----------|---------|
| `DATA_ENCRYPTION_KEY` | Required. 32-byte AES key, base64 |
| `DATABASE_URL` | Postgres connection string |
| `HTTP_ADDR` | Listen address (default `0.0.0.0:8080`; Compose uses `8181` on the host) |
| `WS_PATH` | SSO WebSocket path (default `/ws/sso`) |
| `DISCORD_*` | Bot token, guild, admin role, bootstrap IDs, command prefix |
| `SSO_SOURCE_NAME` | Display name in `/sso get` GUI JSON |
| `WEB_ENABLED` / `WEB_PUBLIC_URL` | Browser admin + OAuth redirect origin |
| `PRESENCE_TTL_SECONDS` | How long EQ heartbeats count as online |
| `LOGIN_AUTH_RATE_LIMIT_PER_MIN` | Per-client login_auth throttle |

See [.env.example](.env.example) for the full list.

---

## Docs

- [docs/discord-bot-setup.md](docs/discord-bot-setup.md) — Discord application, intents, bot permissions, invite URL, share DMs, slash commands
- [docs/web-admin.md](docs/web-admin.md) — OAuth web admin, CSV import, live updates
- [docs/deploy-tls.md](docs/deploy-tls.md) — TLS reverse proxy
- [docs/ws-api.md](docs/ws-api.md) — WebSocket contract (mirrored in the GUI repo)
- [alfred-identity README](../alfred-identity/README.md) — desktop GUI setup and usage
