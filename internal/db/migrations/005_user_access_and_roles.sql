-- +goose Up
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS access_revoked BOOLEAN NOT NULL DEFAULT false;

COMMENT ON COLUMN users.access_revoked IS
    'When true, SSO auth and new tokens are rejected; user row is kept.';

CREATE TABLE IF NOT EXISTS discord_roles (
    role_id     TEXT PRIMARY KEY,
    name        TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE discord_roles IS
    'Discord guild role id→name cache, synced by the bot for admin UI.';

-- +goose Down
DROP TABLE IF EXISTS discord_roles;
ALTER TABLE users DROP COLUMN IF EXISTS access_revoked;
