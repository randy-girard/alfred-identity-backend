-- +goose Up
-- Seed a baseline "Default" group for users with no other membership.
-- Base permissions: no web UI login; Discord /sso and /whoami allowed.
INSERT INTO account_groups (name, description, web_role, discord_commands)
SELECT
  'Default',
  'System group — auto-assigned when a user has no other group. Discord commands only (no web UI).',
  '',
  '["sso","whoami"]'::jsonb
WHERE NOT EXISTS (
  SELECT 1 FROM account_groups WHERE lower(name) = 'default'
);

-- Backfill: users with no direct group membership get Default.
INSERT INTO group_members (group_id, user_id)
SELECT g.id, u.id
FROM users u
CROSS JOIN account_groups g
WHERE lower(g.name) = 'default'
  AND NOT EXISTS (
    SELECT 1 FROM group_members gm WHERE gm.user_id = u.id
  )
ON CONFLICT DO NOTHING;

-- +goose Down
DELETE FROM group_members
WHERE group_id IN (SELECT id FROM account_groups WHERE lower(name) = 'default');
DELETE FROM account_groups WHERE lower(name) = 'default';
