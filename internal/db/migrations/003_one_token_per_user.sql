-- +goose Up
-- One active (non-revoked) SSO token per Discord user.
CREATE UNIQUE INDEX IF NOT EXISTS api_tokens_one_active_per_user
    ON api_tokens (user_id)
    WHERE revoked_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS api_tokens_one_active_per_user;
