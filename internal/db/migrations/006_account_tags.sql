-- +goose Up
CREATE TABLE IF NOT EXISTS account_tags (
    id              BIGSERIAL PRIMARY KEY,
    tag             TEXT NOT NULL,
    eq_account_id   BIGINT NOT NULL REFERENCES eq_accounts(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS account_tags_tag_lower ON account_tags (lower(tag));
CREATE UNIQUE INDEX IF NOT EXISTS account_tags_tag_account_uniq
    ON account_tags (lower(tag), eq_account_id);

COMMENT ON TABLE account_tags IS
    'Shared login tags: the same tag may point at many EQ accounts for cycling.';

-- Aliases are unique across all accounts (one alias → one account; no cycling).
DROP INDEX IF EXISTS aliases_alias_lower;
CREATE UNIQUE INDEX IF NOT EXISTS aliases_alias_lower_uniq ON aliases (lower(alias));

-- +goose Down
DROP INDEX IF EXISTS aliases_alias_lower_uniq;
CREATE INDEX IF NOT EXISTS aliases_alias_lower ON aliases (lower(alias));
DROP TABLE IF EXISTS account_tags;
