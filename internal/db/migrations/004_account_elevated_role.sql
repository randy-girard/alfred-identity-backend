-- +goose Up
ALTER TABLE eq_accounts
    ADD COLUMN required_discord_role_id TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN eq_accounts.required_discord_role_id IS
    'Empty = base access (any SSO token). Non-empty = Discord role id required in addition to SSO.';

-- +goose Down
ALTER TABLE eq_accounts DROP COLUMN IF EXISTS required_discord_role_id;
