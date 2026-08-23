# Discord bot setup

Complete setup for the Alfred Identity backend Discord bot: slash commands (`/sso`, `/whoami`), guild role sync for elevated accounts, and DMs when someone shares a private account with you.

## 1. Create the application

1. Open the [Discord Developer Portal](https://discord.com/developers/applications) → **New Application** (e.g. `alfred-identity-backend`).
2. **General Information** → copy **Application ID** → `DISCORD_CLIENT_ID` in `.env`.

## 2. Bot token

1. **Bot** → **Add Bot**.
2. **Reset Token** → copy once → `DISCORD_TOKEN` in `.env` (never commit).
3. Leave **Public Bot** enabled (default).

## 3. Privileged Gateway Intents

Under **Bot → Privileged Gateway Intents**:

| Intent | Required? | Why |
|--------|-----------|-----|
| **Server Members Intent** | **Yes — turn ON** | Sync guild member roles for elevated SSO accounts, `/whoami`, and web admin user lists |
| **Presence Intent** | No — leave OFF | Not used |
| **Message Content Intent** | No — leave OFF | Bot uses slash commands only; it does not read channel messages |

The daemon requests `Guilds` + `GuildMembers` intents at runtime (`internal/discord/bot.go`).

## 4. Bot permissions (guild invite)

Invite the bot with scopes **`bot`** and **`applications.commands`**.

In **OAuth2 → URL Generator**:

1. **Scopes:** `bot`, `applications.commands`
2. **Bot Permissions** — enable exactly these:

| Permission | Required? | Why |
|------------|-----------|-----|
| **View Channels** | Yes | Baseline guild access |
| **Send Messages** | Yes | Direct messages when a private account is shared with someone |
| **Embed Links** | Yes | Ephemeral embed replies for `/sso` and `/whoami` |
| **Use Application Commands** | Yes* | Slash commands in the guild |

\*Selecting the `applications.commands` scope usually covers slash commands; enable **Use Application Commands** in the permission list as well so re-invites stay correct.

**Do not** grant Administrator or broad moderation permissions — the bot only reads roles and sends DMs/slash replies; it never assigns Discord roles or deletes messages.

### Pre-built permission value

If you build the invite URL manually, use permission integer **`2147503104`** (View Channels + Send Messages + Embed Links + Use Application Commands):

```
https://discord.com/api/oauth2/authorize?client_id=YOUR_CLIENT_ID&permissions=2147503104&scope=bot%20applications.commands
```

Replace `YOUR_CLIENT_ID` with `DISCORD_CLIENT_ID`. Open the URL while logged in as a guild admin and pick your server.

If you change intents or permissions later, **re-invite** the bot (or update its role in **Server Settings → Roles**) and restart the daemon.

## 5. Guild and admin configuration

1. Copy your Discord **server (guild) ID** → `DISCORD_GUILD_ID`  
   (Developer Mode → right-click server → Copy Server ID)
2. Create or pick an **admin role** for operators → copy role ID → `DISCORD_ADMIN_ROLE_ID`
3. Add your Discord user snowflake(s) → `DISCORD_BOOTSTRAP_ADMIN_IDS` (comma-separated) for first-run access before roles are wired up
4. Set `DISCORD_ENABLED=true` and restart the daemon

On startup the bot registers guild slash commands (`/{prefix}sso`, `/{prefix}whoami`) and begins role sync.

## 6. Environment variables

| Variable | Purpose |
|----------|---------|
| `DISCORD_TOKEN` | Bot token from the Developer Portal |
| `DISCORD_CLIENT_ID` | Application ID (also used for web admin OAuth) |
| `DISCORD_GUILD_ID` | Guild where the bot runs and commands are registered |
| `DISCORD_ADMIN_ROLE_ID` | Discord role that grants admin/operator access |
| `DISCORD_BOOTSTRAP_ADMIN_IDS` | Comma-separated user snowflakes for bootstrap admins |
| `DISCORD_COMMAND_PREFIX` | Slash command prefix (default `alfred-identity-`) |
| `DISCORD_ROLE_SYNC_SECONDS` | Role refresh interval (default `300`) |
| `SSO_SOURCE_NAME` | Display name in `/sso get` GUI JSON |

See [.env.example](../.env.example) for the full list.

## 7. Share notification DMs

When someone uses **Local → Share** in **Alfred Identity**, newly added recipients get a Discord DM from the bot.

Requirements:

- `DISCORD_ENABLED=true` and the bot is online
- Recipient has logged into SSO at least once (so their Discord ID is in the database)
- Recipient shares the guild with the bot
- Recipient allows **Direct Messages → Allow direct messages from server members** in Discord **User Settings → Privacy & Safety**

Failed DMs are logged only; the share still succeeds.

## 8. Web admin OAuth (optional, separate from the bot invite)

The browser admin uses **user** OAuth, not the bot invite. See [web-admin.md](web-admin.md).

Additional Developer Portal steps:

1. **OAuth2 → Client Secret** → `DISCORD_CLIENT_SECRET`
2. **OAuth2 → Redirects** → `{WEB_PUBLIC_URL}/admin/oauth/callback`
3. User login scopes: `identify`, `guilds.members.read`

## 9. Verify the setup

1. Bot appears **online** in the member list after `docker compose up` (or `./bin/daemon`)
2. Slash commands appear: `/alfred-identity-sso`, `/alfred-identity-whoami` (or your prefix)
3. `/alfred-identity-sso get` returns an SSO token and **Alfred Identity** source JSON
4. `/alfred-identity-whoami` shows your cached roles and account count
5. Daemon logs: `discord ready`, `register command ok`

---

## Access model

| Access | Who can use the EQ account via SSO |
|--------|-------------------------------------|
| **Base** (default) | Anyone with a valid SSO token |
| **Elevated** | SSO token **and** the Discord role set on that account |

Users run `/alfred-identity-sso get` → paste JSON into **Alfred Identity**. Base accounts show up automatically.

On the **Groups** tab in **Alfred Identity Management**, you can restrict who may use Discord slash commands (`/sso`, `/whoami`). When any group enables a command, only members of groups with that command can use it. Discord bootstrap admins and the configured admin role bypass restrictions.

EQ accounts, aliases, tags, characters, private Local → Share access, and user access are managed in **Alfred Identity** or **Alfred Identity Management**.

When someone shares a private account from **Alfred Identity**, newly added recipients receive a Discord DM (if `DISCORD_ENABLED` and the bot can message them). Existing recipients are not notified again when the share list is updated.

Discord roles for elevated access are kept fresh automatically:

- On bot startup and every `DISCORD_ROLE_SYNC_SECONDS` (default 5 minutes)
- Immediately on guild member role changes / leaves
- Guild role name cache updates when roles are created/renamed/deleted

## Typical setup

1. Admins add EQ accounts (and optional elevated roles) in the GUI or web admin
2. Users run `/alfred-identity-sso get` and paste the token into **Alfred Identity**

## Commands (`DISCORD_COMMAND_PREFIX`, default `alfred-identity-`)

| Command | Purpose |
|---------|---------|
| `/{prefix}sso get\|revoke\|list` | SSO API tokens (get/create for the GUI) |
| `/{prefix}whoami` | Status + how many SSO accounts you can use |

Token replies are ephemeral. Keep secrets private.
