# Discord bot setup

1. Open [Discord Developer Portal](https://discord.com/developers/applications) → **New Application** (e.g. `alfred-identity-backend`).
2. **Bot** → Add Bot → copy token → `DISCORD_TOKEN` in `.env` (never commit).
3. Enable **Server Members Intent** (required for role sync and elevated accounts).
4. **OAuth2 → URL Generator**: scopes `bot` + `applications.commands`; invite to the guild.
5. Application ID → `DISCORD_CLIENT_ID`; guild snowflake → `DISCORD_GUILD_ID`.
6. Admin role snowflake → `DISCORD_ADMIN_ROLE_ID`.
7. First operators → `DISCORD_BOOTSTRAP_ADMIN_IDS` (comma-separated user snowflakes).
8. Set `DISCORD_ENABLED=true` and restart the daemon (guild-scoped command registration on ready).
9. Optional: `DISCORD_COMMAND_PREFIX` (default `alfred-identity-`) — slash commands are `{prefix}sso` and `{prefix}whoami`. Changing it re-registers via bulk overwrite.
10. Optional: `DISCORD_ROLE_SYNC_SECONDS` (default `300`) — how often the bot refreshes Discord roles for known users.
11. Optional: `SSO_SOURCE_NAME` — friendly name shown in `/sso get` JSON pasted into **Alfred Identity** (defaults to `Local daemon` when the host is localhost).
12. Optional web admin: set `DISCORD_CLIENT_SECRET`, `WEB_ENABLED=true`, `WEB_PUBLIC_URL`, and add OAuth2 redirect `{WEB_PUBLIC_URL}/admin/oauth/callback`. See [web-admin.md](web-admin.md).

## Access model

| Access | Who can use the EQ account via SSO |
|--------|-------------------------------------|
| **Base** (default) | Anyone with a valid SSO token |
| **Elevated** | SSO token **and** the Discord role set on that account |

Users: `/alfred-identity-sso create` → paste into **Alfred Identity**. Base accounts show up automatically.

On the **Groups** tab in **alfred-identity-backend**, you can restrict who may use Discord slash commands (`/sso`, `/whoami`). When any group enables a command, only members of groups with that command can use it. Discord bootstrap admins and the configured admin role bypass restrictions.

EQ accounts, aliases, tags, characters, private Local → Share access, and user access are managed in **Alfred Identity** or **alfred-identity-backend** web admin.

Discord roles for elevated access are kept fresh automatically:
- On bot startup and every `DISCORD_ROLE_SYNC_SECONDS` (default 5 minutes)
- Immediately on guild member role changes / leaves
- Guild role name cache updates when roles are created/renamed/deleted

## Typical setup

1. Admins add EQ accounts (and optional elevated roles) in the GUI or web admin
2. Users run `/alfred-identity-sso create` and paste the token into **Alfred Identity**

## Commands (`DISCORD_COMMAND_PREFIX`, default `alfred-identity-`)

| Command | Purpose |
|---------|---------|
| `/{prefix}sso create\|revoke\|list\|get` | SSO API tokens (create/retrieve for the GUI) |
| `/{prefix}whoami` | Status + how many SSO accounts you can use |

Token replies are ephemeral. Keep secrets private.
