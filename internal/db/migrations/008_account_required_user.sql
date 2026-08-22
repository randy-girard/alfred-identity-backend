-- +goose Up
ALTER TABLE eq_accounts
    ADD COLUMN required_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX eq_accounts_required_user_id_idx ON eq_accounts (required_user_id)
    WHERE required_user_id IS NOT NULL;

COMMENT ON COLUMN eq_accounts.required_user_id IS
    'When set (and account is not restricted), only this SSO user may use the account. Combined with required_discord_role_id and account_group_links via OR.';

-- +goose Down
DROP INDEX IF EXISTS eq_accounts_required_user_id_idx;
ALTER TABLE eq_accounts DROP COLUMN IF EXISTS required_user_id;
