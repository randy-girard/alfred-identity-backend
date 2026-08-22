-- +goose Up
CREATE TABLE users (
    id              BIGSERIAL PRIMARY KEY,
    discord_id      TEXT NOT NULL UNIQUE,
    display_name    TEXT NOT NULL DEFAULT '',
    role_ids_json   TEXT NOT NULL DEFAULT '[]',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE api_tokens (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash      TEXT NOT NULL UNIQUE,
    token_cipher    BYTEA,
    label           TEXT NOT NULL DEFAULT '',
    revoked_at      TIMESTAMPTZ,
    last_used_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE account_groups (
    id              BIGSERIAL PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    description     TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE group_members (
    id              BIGSERIAL PRIMARY KEY,
    group_id        BIGINT NOT NULL REFERENCES account_groups(id) ON DELETE CASCADE,
    user_id         BIGINT REFERENCES users(id) ON DELETE CASCADE,
    discord_role_id TEXT,
    CHECK (user_id IS NOT NULL OR discord_role_id IS NOT NULL)
);

CREATE UNIQUE INDEX group_members_user_uniq ON group_members (group_id, user_id) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX group_members_role_uniq ON group_members (group_id, discord_role_id) WHERE discord_role_id IS NOT NULL;

CREATE TABLE eq_accounts (
    id                  BIGSERIAL PRIMARY KEY,
    username_cipher     BYTEA NOT NULL,
    username_blind      TEXT NOT NULL UNIQUE,
    password_cipher     BYTEA NOT NULL,
    notes_cipher        BYTEA,
    disabled            BOOLEAN NOT NULL DEFAULT false,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE account_group_links (
    group_id        BIGINT NOT NULL REFERENCES account_groups(id) ON DELETE CASCADE,
    eq_account_id   BIGINT NOT NULL REFERENCES eq_accounts(id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, eq_account_id)
);

CREATE TABLE aliases (
    id              BIGSERIAL PRIMARY KEY,
    alias           TEXT NOT NULL,
    eq_account_id   BIGINT NOT NULL REFERENCES eq_accounts(id) ON DELETE CASCADE
);

CREATE INDEX aliases_alias_lower ON aliases (lower(alias));

CREATE TABLE characters (
    id              BIGSERIAL PRIMARY KEY,
    name            TEXT NOT NULL,
    eq_account_id   BIGINT NOT NULL REFERENCES eq_accounts(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX characters_name_lower ON characters (lower(name));

CREATE TABLE audit_log (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT,
    action          TEXT NOT NULL,
    detail_cipher   BYTEA,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE presence (
    eq_account_id   BIGINT PRIMARY KEY REFERENCES eq_accounts(id) ON DELETE CASCADE,
    character_name  TEXT NOT NULL DEFAULT '',
    user_id         BIGINT,
    last_seen       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS presence;
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS characters;
DROP TABLE IF EXISTS aliases;
DROP TABLE IF EXISTS account_group_links;
DROP TABLE IF EXISTS eq_accounts;
DROP TABLE IF EXISTS group_members;
DROP TABLE IF EXISTS account_groups;
DROP TABLE IF EXISTS api_tokens;
DROP TABLE IF EXISTS users;
