package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// KnownDiscordCommandKeys are slash command short names (without DISCORD_COMMAND_PREFIX).
var KnownDiscordCommandKeys = []string{"sso", "whoami"}

var knownDiscordCommands = map[string]bool{
	"sso":    true,
	"whoami": true,
}

// NormalizeDiscordCommands validates and deduplicates command keys.
func NormalizeDiscordCommands(cmds []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, c := range cmds {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "" {
			continue
		}
		if !knownDiscordCommands[c] {
			return nil, fmt.Errorf("discord_commands: unknown command %q (allowed: sso, whoami)", c)
		}
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	sort.Strings(out)
	if out == nil {
		return []string{}, nil
	}
	return out, nil
}

func marshalDiscordCommands(cmds []string) (string, error) {
	norm, err := NormalizeDiscordCommands(cmds)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(norm)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func parseDiscordCommandsJSON(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return []string{}, nil
	}
	var cmds []string
	if err := json.Unmarshal(raw, &cmds); err != nil {
		return nil, err
	}
	return NormalizeDiscordCommands(cmds)
}

// IsDiscordCommandRestricted reports whether any group grants the given command key.
func (s *Store) IsDiscordCommandRestricted(ctx context.Context, cmdKey string) (bool, error) {
	cmdKey = strings.ToLower(strings.TrimSpace(cmdKey))
	if !knownDiscordCommands[cmdKey] {
		return false, fmt.Errorf("unknown discord command %q", cmdKey)
	}
	payload, err := json.Marshal([]string{cmdKey})
	if err != nil {
		return false, err
	}
	var exists bool
	err = s.DB.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM account_groups WHERE discord_commands @> $1::jsonb
		)
	`, string(payload)).Scan(&exists)
	return exists, err
}

// UserCanUseDiscordCommand checks group-based slash command access.
// When no group grants cmdKey, all users may use it (backward compatible).
func (s *Store) UserCanUseDiscordCommand(ctx context.Context, u User, cmdKey string) (bool, error) {
	restricted, err := s.IsDiscordCommandRestricted(ctx, cmdKey)
	if err != nil {
		return false, err
	}
	if !restricted {
		return true, nil
	}
	payload, err := json.Marshal([]string{strings.ToLower(strings.TrimSpace(cmdKey))})
	if err != nil {
		return false, err
	}
	var ok bool
	err = s.DB.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM account_groups g
			JOIN group_members gm ON gm.group_id = g.id
			WHERE g.discord_commands @> $2::jsonb
			  AND (
			    gm.user_id = $1
			    OR (
			      gm.discord_role_id IS NOT NULL
			      AND EXISTS (
			        SELECT 1 FROM users uu
			        WHERE uu.id = $1
			          AND uu.role_ids_json::jsonb ? gm.discord_role_id
			      )
			    )
			  )
		)
	`, u.ID, string(payload)).Scan(&ok)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return ok, err
}
