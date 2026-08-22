# alfred-identity-web

Go service for Discord-managed EQ bot/SSO accounts, Postgres-backed secrets (AES-GCM), and a WebSocket SSO API consumed by the **Alfred Identity** desktop app (alfred-identity-app).

## Quick start (Docker Compose)

```bash
cd alfred-identity-web
cp .env.example .env
# Set DATA_ENCRYPTION_KEY (32 bytes base64): openssl rand -base64 32
# Optional: Discord token, guild, bootstrap admin IDs — or leave DISCORD_ENABLED=false
docker compose up --build
```

Compose maps host **8181** → container `8080`:

| Endpoint | URL |
|----------|-----|
| Health | `GET http://127.0.0.1:8181/health` |
| SSO WebSocket | `ws://127.0.0.1:8181/ws/sso` |
| GUI source JSON | `GET http://127.0.0.1:8181/sso-source.json` |
| Web admin (optional) | `http://127.0.0.1:8181/admin/` |

In production, terminate TLS at an external reverse proxy and point the GUI at `wss://…/ws/sso`. See [docs/deploy-tls.md](docs/deploy-tls.md).

## Web admin

Optional Discord OAuth UI for the same account/user/session management as the desktop app (alfred-identity-app). Enable with `WEB_ENABLED=true` and configure OAuth redirect + client secret — see [docs/web-admin.md](docs/web-admin.md). Mutations broadcast to all SSO WebSocket clients immediately.

## SSO token for the GUI

**With Discord enabled:** `/alfred-identity-sso create`, then `/alfred-identity-sso get` for the secret. One active token per Discord user (create replaces the previous).

Any valid SSO token gets **base** access to all non-elevated EQ accounts. Elevated accounts also require a Discord role (see [docs/discord-bot-setup.md](docs/discord-bot-setup.md)).

**Without Discord** (`DISCORD_ENABLED=false`):

```bash
# with Compose DB reachable, or DATABASE_URL pointing at Postgres
go run ./cmd/seedtoken <discord_id> <display_name>
```

Paste the printed token into the GUI **SSO** tab as a source URL `ws://127.0.0.1:8181/ws/sso`.

## Discord

Slash commands use `DISCORD_COMMAND_PREFIX` (default `alfred-identity-`, e.g. `/alfred-identity-sso get`).
Setup guide: [docs/discord-bot-setup.md](docs/discord-bot-setup.md).

Useful Discord commands: SSO tokens (`create` / `get` / `list` / `revoke`) and whoami. Discord roles for elevated access sync automatically (periodic + member events). Account and group management is done in the GUI or web admin.

## Docs

- [docs/ws-api.md](docs/ws-api.md) — WebSocket contract (mirrored in the GUI)
- [docs/web-admin.md](docs/web-admin.md) — Discord OAuth web admin
- [docs/deploy-tls.md](docs/deploy-tls.md) — external TLS reverse proxy
- [../alfred-identity-app/README.md](../alfred-identity-app/README.md) — Alfred Identity desktop app (alfred-identity-app)

## Build (binary only)

```bash
./scripts/build.sh
```

## Tests and coverage

```bash
make test          # unit tests (race detector)
make coverage      # writes coverage/index.html (+ source.html, func.txt)
```

Open `coverage/index.html` in a browser. Store ACL / share integration tests run when `TEST_DATABASE_URL` (or `DATABASE_URL`) points at Postgres; otherwise they skip. From the repo root: `make test` / `make coverage` runs daemon and GUI (also writes root `coverage/index.html`).
