# Security review — alfred-identity & alfred-identity-backend

**Date:** 2026-08-25  
**Status:** All listed findings remediated (2026-08-25).  
**Scope:** Full codebase review (both apps on `main`). Focus: password/credential leakage and RBAC.

---

## Remediation summary

| # | Finding | Status |
|---|---|---|
| 1 | Readonly web users got admin-level data | Fixed — role-aware `buildState`; audit is `requireAdmin`; admin-only tabs hidden |
| 2 | SSO admin WS could mutate private shares | Fixed — shared `CheckRestrictedAccountManage`; applied to all account `admin_*` mutators |
| 3 | Web/group admin could mint Discord admin via role cache | Fixed — web + WS role edit APIs return `roles_managed_by_discord`; Discord bot/import still use `SetUserRoles` |
| 4 | Presence / sessions leaked across ACL | Fixed — `FullStateForUser` and web state filter online/sessions; SSO directory/groups/roles kept for share UI |
| 5 | Empty ACL footgun | Fixed — UI confirm + docs; optional `REQUIRE_ACCOUNT_ACL=true` |
| 6 | Admin exports dumped passwords by default | Fixed — omit passwords unless `?include_passwords=1` (UI confirms) |
| 7 | Desktop plaintext secrets on disk | Fixed — docs; list DTO omits passwords; `GetLocalAccountPassword` for edit form |
| 8 | Web session HMAC reused DEK | Fixed — HKDF `web-session` (optional `WEB_SESSION_KEY`) |

---

## Already in good shape (unchanged)

- EQ passwords and API token secrets encrypted at rest (AES-256-GCM); tokens authenticated via hash lookup.
- `ResolveLoginCandidates` / `AllowedAccountIDs` gate `login_auth` (no cross-ACL decrypt on the allowed path).
- Web mutations use `rejectIfReadonly`; create/import/export/backup use `requireAdmin`.
- Desktop source DTOs expose `has_token`, not the raw token.
- Discord `/sso get` uses ephemeral replies; server logs avoid printing passwords.
- OAuth uses signed CSRF state and requires guild membership.
- EQ DES zero-key credential packing remains a protocol requirement (accepted risk).

---

## Out of scope / accepted risks

- EQ DES zero-key credential packing (protocol requirement).
- Discord delivering SSO tokens in ephemeral embeds (necessary UX; users must treat as secrets).
- Admins who already hold Discord admin having broad guild-account password access (product intent).
- Empty ACL = all authenticated SSO users (product default; hardened with UX/docs; optional deny via env).

---

## TLDR (plain language) — what was fixed

### 1. Read-only users see too much
**Problem:** People who are only supposed to *look* could still download the full staff list, who’s online, and the activity log.  
**Fix:** Only show them the accounts they’re allowed to see. Hide the admin-only lists and the full log.

### 2. Admins can change someone else’s private shared accounts
**Problem:** Guild admins could change passwords or delete accounts that someone shared privately from the desktop app—even though the website already blocked that.  
**Fix:** Apply the same rules on the desktop/admin connection: private shares stay under the owner’s control.

### 3. A website “admin” can promote themselves to full Discord admin
**Problem:** Someone given website admin through a group could fake Discord admin powers in the system and unlock more control than they should have.  
**Fix:** Stop letting website/desktop admins hand-edit Discord roles. Roles come from Discord itself.

### 4. People can see who’s playing on accounts they can’t use
**Problem:** Presence (“who’s online”) was shared too widely, so users learned activity on accounts they shouldn’t know about.  
**Fix:** Only show online status for accounts that person is allowed to use. Admins can still see everything for support.

### 5. “Open to everyone” passwords are easy to misunderstand
**Problem:** The game login format isn’t real encryption. If an account is left open to “everyone,” any logged-in SSO user can get that password. That’s intentional for the game, but easy to misconfigure.  
**Fix:** Don’t change the game format. Make the “everyone” setting clearer (confirmations/docs). Keep TLS so passwords aren’t sniffed on the network.

### 6. Admin exports include all passwords in one file
**Problem:** A stolen admin session could download a file with every account password. Needed for backups, but risky.  
**Fix:** Keep exports admin-only, and require a clear “yes, include passwords” step before downloading them.

### 7. Desktop app stores secrets in plain files on the PC
**Problem:** Local passwords and login tokens sit in files on the computer. Fine for a personal login helper; risky on shared or backed-up machines.  
**Fix:** Document that those files are secrets. List views no longer send passwords into the UI until you open an account to edit.

### 8. One secret key does two jobs
**Problem:** The same master key both protected stored passwords and signed website login cookies, so one leak was worse than it needed to be.  
**Fix:** Use separate keys for those jobs (derived from the existing one). People may need to log in again once after the change.
