-- +goose Up
CREATE TABLE account_access_roles (
    eq_account_id    BIGINT NOT NULL REFERENCES eq_accounts(id) ON DELETE CASCADE,
    discord_role_id  TEXT NOT NULL,
    PRIMARY KEY (eq_account_id, discord_role_id)
);

CREATE TABLE account_access_users (
    eq_account_id BIGINT NOT NULL REFERENCES eq_accounts(id) ON DELETE CASCADE,
    user_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (eq_account_id, user_id)
);

CREATE INDEX account_access_roles_role_idx ON account_access_roles (discord_role_id);
CREATE INDEX account_access_users_user_idx ON account_access_users (user_id);

INSERT INTO account_access_roles (eq_account_id, discord_role_id)
SELECT id, required_discord_role_id
FROM eq_accounts
WHERE required_discord_role_id IS NOT NULL AND required_discord_role_id <> ''
ON CONFLICT DO NOTHING;

INSERT INTO account_access_users (eq_account_id, user_id)
SELECT id, required_user_id
FROM eq_accounts
WHERE required_user_id IS NOT NULL
ON CONFLICT DO NOTHING;

COMMENT ON TABLE account_access_roles IS
    'Discord roles that may use an EQ account (OR with users/groups). Empty with no users/groups = all SSO users.';
COMMENT ON TABLE account_access_users IS
    'Discord users that may use an EQ account (OR with roles/groups).';

-- +goose Down
DROP TABLE IF EXISTS account_access_users;
DROP TABLE IF EXISTS account_access_roles;
