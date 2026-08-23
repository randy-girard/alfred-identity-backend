# WebSocket SSO API (v1)

Protocol version: **1** (hard-reject mismatch on `auth`).

Endpoint: `WS_PATH` (default `/ws/sso`). Production: terminate TLS at an external reverse proxy (`wss://`).

## Client → server

### `auth`
```json
{ "type": "auth", "token": "<raw api token>", "protocol_version": 1, "client_version": "gui/0.1.0" }
```

### `get_state`
```json
{ "type": "get_state" }
```
Re-sends `full_state` for the authenticated user (after Discord group/account changes).

### `login_auth`
```json
{ "type": "login_auth", "request_id": "uuid", "username": "tag-alias-or-character" }
```
Daemon is authoritative: resolve **tag** (shared → cycle free / non-busy accounts), unique **alias**, **username**, or **character** → allowed non-disabled accounts. Busy/presence skipping applies **only** to multi-account **tag** pools; direct identity matches always return credentials (EQ reports already-logged-in if the session is occupied).

### `heartbeat`
```json
{ "type": "heartbeat", "character_name": "Hero", "offline": false }
```
Server looks up character → EQ account; marks **account** online. Unknown character ignored. Only accounts the token may access are accepted.

### `pong`
```json
{ "type": "pong" }
```

### `share_account` / `unshare_account` (any authenticated user)
```json
{ "type": "share_account", "request_id": "uuid", "username": "equser", "password": "secret", "aliases": ["tank"], "user_ids": [2, 3], "role_ids": ["123456789012345678"], "group_ids": [1] }
{ "type": "unshare_account", "request_id": "uuid", "username": "equser" }
```
Publishes a **restricted** EQ account owned by the caller (from a local GUI account) and grants SSO login to listed Discord users, roles, and/or access groups. Empty grant lists keep an owner-only copy for that type. `unshare_account` deletes the restricted SSO copy. Broadcasts `full_state`. Responses use `share_result`. When Discord is enabled, users **newly** added to the direct user share list receive a DM notification.

`full_state` also includes `directory` (SSO users for the share picker), `groups`, `roles`, and `user_id` / `discord_id` / `display_name` for the connected client.

### `admin_add_account` (admin only)
```json
{ "type": "admin_add_account", "request_id": "uuid", "username": "equser", "password": "secret", "required_role_id": "optional-discord-role-id" }
```
Requires Discord bootstrap admin or `DISCORD_ADMIN_ROLE_ID`. Password is never echoed back. Optional `required_role_id` restricts SSO access to users who hold that Discord role.

### `admin_update_account` (admin only)
```json
{ "type": "admin_update_account", "request_id": "uuid", "account_id": 1, "password": "new", "disabled": false, "required_role_id": "" }
```
Updates password (omit/empty = unchanged), `disabled`, and/or required Discord role (`""` clears). Broadcasts `full_state`.

### `admin_add_alias` / `admin_add_tag` / `admin_add_character` (admin only)
```json
{ "type": "admin_add_alias", "request_id": "uuid", "alias": "solo", "account_id": 1 }
{ "type": "admin_add_tag", "request_id": "uuid", "tag": "box", "account_id": 1 }
{ "type": "admin_add_character", "request_id": "uuid", "name": "Hero", "account_id": 1 }
```
Aliases are globally unique (one account). Tags may be attached to many accounts for login cycling.

### `admin_remove_alias` / `admin_remove_tag` (admin only)
```json
{ "type": "admin_remove_alias", "request_id": "uuid", "alias": "solo", "account_id": 1 }
{ "type": "admin_remove_tag", "request_id": "uuid", "tag": "box", "account_id": 1 }
```
Removes the alias/tag from that account and broadcasts `full_state`.

### `admin_remove_account` (admin only)
```json
{ "type": "admin_remove_account", "request_id": "uuid", "account_id": 1 }
```
Deletes the EQ account and cascading aliases/tags/characters. Broadcasts `full_state`.

### `admin_set_user_access` (admin only)
```json
{ "type": "admin_set_user_access", "request_id": "uuid", "user_id": 1, "revoked": true }
```
When `revoked` is true, active tokens are revoked and WS sessions for that user are closed. The user row is kept; new token creation is rejected until access is restored.

### `admin_set_user_roles` (admin only)
```json
{ "type": "admin_set_user_roles", "request_id": "uuid", "user_id": 1, "role_ids": ["discord-role-id"] }
```
Replaces the user's cached Discord role IDs (same field Discord resync writes).

After a successful admin mutation, the daemon **broadcasts** a fresh `full_state` to every connected client (each payload is ACL-filtered for that user). Admins also receive an `admin` object:

```json
{
  "admin": {
    "users": [
      {
        "id": 1,
        "discord_id": "…",
        "display_name": "…",
        "role_ids": ["…"],
        "access_revoked": false,
        "has_active_token": true
      }
    ],
    "roles": [{ "id": "…", "name": "Member" }]
  }
}
```
`roles` is the Discord guild role cache (`discord_roles` table), synced by the bot on ready / periodic role sync / guild role events.

## Server → client

### `full_state` (after auth / reconnect / broadcast — no `delta` in v1)
```json
{
  "type": "full_state",
  "is_admin": false,
  "state": {
    "accounts": [
      { "id": 1, "username": "equser", "disabled": false, "elevated": true, "required_role_id": "123456789", "aliases": ["solo"], "tags": ["box"], "characters": ["Hero"] }
    ],
    "online": [{ "account_id": 1, "character_name": "Hero" }]
  }
}
```
`is_admin` is true when the token’s Discord user is a bootstrap admin or holds `DISCORD_ADMIN_ROLE_ID` (roles from last Discord interaction / resync).

### `admin_result`
```json
{ "type": "admin_result", "request_id": "uuid", "ok": true, "account_id": 1 }
{ "type": "admin_result", "request_id": "uuid", "ok": false, "error": "forbidden|unauthorized|rate_limited|…" }
```

**Never** includes passwords, hashes, or DES blobs.

### `login_auth_response`
Success:
```json
{
  "type": "login_auth_response",
  "request_id": "uuid",
  "real_user": "equser",
  "encrypted_credentials": "<base64 DES-CBC blob>",
  "account_id": 1
}
```
Error:
```json
{ "type": "login_auth_response", "request_id": "uuid", "error": "not_found|all_busy|rate_limited|internal" }
```

DES wire: username`\0`password`\0`, zero-pad to 8, DES-CBC with key/IV eight zero bytes. Treat blob as a password — ephemeral, TLS only, never log or persist.

### `error` / `ping`
```json
{ "type": "error", "message": "..." }
{ "type": "ping" }
```

## Golden DES vector

Plain: `user\0pass\0` (+ zero pad) → cipher hex `575ab3e46810e874f75cb31595902052`
