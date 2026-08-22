-- +goose Up
ALTER TABLE account_groups
  ADD COLUMN IF NOT EXISTS discord_commands JSONB NOT NULL DEFAULT '[]'::jsonb;

COMMENT ON COLUMN account_groups.discord_commands IS
  'Slash command keys granted to group members (e.g. ["sso","whoami"]). Empty = no grant. When any group lists a command, only members of groups listing that command may use it.';

-- +goose Down
ALTER TABLE account_groups DROP COLUMN IF EXISTS discord_commands;
