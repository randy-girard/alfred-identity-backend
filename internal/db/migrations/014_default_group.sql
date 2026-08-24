-- +goose Up
-- Seed a baseline "Default" group for users with no other membership.
-- web_role=readonly so OAuth web UI works after Discord /sso get.
-- discord_commands stays empty so existing open slash-command behavior is unchanged;
-- admins can add sso/whoami on the Groups tab if they want to restrict commands.
INSERT INTO account_groups (name, description, web_role, discord_commands)
SELECT
  'Default',
  'Auto-assigned when a user has no other group (SSO slash command or first web login).',
  'readonly',
  '[]'::jsonb
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
