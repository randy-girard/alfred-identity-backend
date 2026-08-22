-- +goose Up
ALTER TABLE eq_accounts
    ADD COLUMN owner_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN restricted BOOLEAN NOT NULL DEFAULT false;

CREATE INDEX eq_accounts_owner_idx ON eq_accounts (owner_user_id) WHERE owner_user_id IS NOT NULL;

CREATE TABLE account_shares (
    eq_account_id BIGINT NOT NULL REFERENCES eq_accounts(id) ON DELETE CASCADE,
    user_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (eq_account_id, user_id)
);

CREATE INDEX account_shares_user_idx ON account_shares (user_id);

-- +goose Down
DROP TABLE IF EXISTS account_shares;
DROP INDEX IF EXISTS eq_accounts_owner_idx;
ALTER TABLE eq_accounts
    DROP COLUMN IF EXISTS restricted,
    DROP COLUMN IF EXISTS owner_user_id;
