-- +goose Up
ALTER TABLE account_groups
  ADD COLUMN IF NOT EXISTS web_role TEXT NOT NULL DEFAULT '';

ALTER TABLE account_groups
  DROP CONSTRAINT IF EXISTS account_groups_web_role_check;

ALTER TABLE account_groups
  ADD CONSTRAINT account_groups_web_role_check
  CHECK (web_role IN ('', 'admin', 'readonly'));

-- +goose Down
ALTER TABLE account_groups DROP CONSTRAINT IF EXISTS account_groups_web_role_check;
ALTER TABLE account_groups DROP COLUMN IF EXISTS web_role;
