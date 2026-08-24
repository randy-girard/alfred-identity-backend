-- +goose Up
-- Align Default group to locked base permissions: no web UI, Discord slash commands allowed.
UPDATE account_groups
SET
  web_role = '',
  discord_commands = '["sso","whoami"]'::jsonb,
  description = CASE
    WHEN description IS NULL OR description = '' OR description LIKE 'Auto-assigned%'
      THEN 'System group — auto-assigned when a user has no other group. Discord commands only (no web UI).'
    ELSE description
  END
WHERE lower(name) = 'default';

-- +goose Down
UPDATE account_groups
SET
  web_role = 'readonly',
  discord_commands = '[]'::jsonb
WHERE lower(name) = 'default';
