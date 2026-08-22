# Web admin (Discord OAuth)

The daemon can serve a browser admin UI at `/admin/` for the same account, user, role, and session management as the desktop GUI.

## Enable

1. In the [Discord Developer Portal](https://discord.com/developers/applications) → your app → **OAuth2**:
   - Copy **Client ID** and **Client Secret** → `DISCORD_CLIENT_ID` / `DISCORD_CLIENT_SECRET`
   - Add redirect: `{WEB_PUBLIC_URL}/admin/oauth/callback`  
     Example: `http://127.0.0.1:8181/admin/oauth/callback` or `https://identity.example.com/admin/oauth/callback`
2. Under **OAuth2 → URL Generator** scopes used by login: `identify`, `guilds.members.read`
3. Set env:

```bash
DISCORD_ENABLED=true
WEB_ENABLED=true
WEB_PUBLIC_URL=http://127.0.0.1:8181   # public origin, no trailing slash
WEB_ACCESS_ROLE_ID=                     # optional; defaults to DISCORD_ADMIN_ROLE_ID
```

Bootstrap Discord user IDs in `DISCORD_BOOTSTRAP_ADMIN_IDS` can always open the web UI.

4. Restart the daemon and open `{WEB_PUBLIC_URL}/admin/`

## GUI source URL

Public (no login): `{WEB_PUBLIC_URL}/sso-source.json`

Members paste that URL (or just the origin) into the desktop app → **Connections → Add from URL**. Optional display name: `WEB_SSO_SOURCE_NAME`.

Settings → **GUI source URL** also shows a copy button.

If you previously registered `/web/oauth/callback`, update the Discord OAuth2 redirect to `/admin/oauth/callback` (Discord requires an exact match). Requests under the old `/web/` prefix are redirected to `/admin/`.

## Authorization

- Login is Discord OAuth only (no passwords).
- Access requires guild membership **and** either:
  - the `WEB_ACCESS_ROLE_ID` / `DISCORD_ADMIN_ROLE_ID` Discord role, or
  - a bootstrap admin snowflake.
- Revoked SSO users (`access_revoked`) cannot use the web UI.
- Every `/admin/api/*` request and `/admin/ws` re-checks roles from the database.

## Live updates

Mutations go through the same store as the SSO WebSocket admin API and call `BroadcastFullState`, so:

- All connected **desktop GUI** clients receive an updated `full_state` immediately.
- All open **web admin** browser tabs receive a pushed `state` message on `/admin/ws`.

## Pages

| Tab | Purpose |
|-----|---------|
| Overview | High-level counts (accounts, users, groups, shares, live clients/sessions) plus who’s online |
| Accounts | Add/edit/remove EQ accounts; access via Discord role, user, and/or access groups (OR); aliases/tags/characters; CSV import |
| Users | Revoke/restore access, edit Discord role grants cached for SSO |
| Groups | Access groups: Discord users + roles; link to EQ accounts for SSO login gating |
| Shared accounts | Private Local → Share copies: owner, who can use them, edit share list, remove SSO copy |
| Connections | Live SSO WebSocket clients (Discord user, GUI version, connected duration) |
| Sessions | Live EQ presence (heartbeat) rows |

### CSV import (Accounts)

Header row required:

```csv
username,password,role,aliases,tags,characters
equser,s3cret,123456789012345678,"tank,box","raid,alt","Hero,Alt"
equser2,pass,,solo,box,Main
```

| Column | Notes |
|--------|--------|
| `username` | EQ account name (upsert key, case-insensitive) |
| `password` | Required for new accounts; leave blank on update to keep current |
| `role` | Discord role **id** or cached **name** for elevated access; blank = all (any SSO token) |
| `aliases` | Comma-separated (quote the field); `\|` also accepted |
| `tags` | Comma-separated shared login tags |
| `characters` | Comma-separated character names (globally unique) |

Import upserts accounts and merges aliases/tags/characters, then broadcasts `full_state` to all clients.

## TLS

Terminate TLS at your reverse proxy and set `WEB_PUBLIC_URL` to the `https://` origin. Session cookies are marked `Secure` when the public URL is HTTPS. See [deploy-tls.md](deploy-tls.md).
