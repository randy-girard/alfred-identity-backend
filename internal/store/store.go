package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alfred-identity/web/internal/crypto"
)

type Store struct {
	DB   *sql.DB
	AEAD *crypto.AEAD
	Key  []byte // for blind index
}

type User struct {
	ID             int64
	DiscordID      string
	DisplayName    string
	RoleIDs        []string
	AccessRevoked  bool
}

type DiscordRole struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type AdminUser struct {
	ID             int64    `json:"id"`
	DiscordID      string   `json:"discord_id"`
	DisplayName    string   `json:"display_name"`
	RoleIDs        []string `json:"role_ids"`
	AccessRevoked  bool     `json:"access_revoked"`
	HasActiveToken bool     `json:"has_active_token"`
}

type AdminState struct {
	Users []AdminUser    `json:"users"`
	Roles []DiscordRole  `json:"roles"`
}

type EQAccountMeta struct {
	ID             int64    `json:"id"`
	Username       string   `json:"username"`
	Disabled       bool     `json:"disabled"`
	Elevated       bool     `json:"elevated"`
	RequiredRoleID string   `json:"required_role_id"`             // first role; legacy clients
	RequiredRoleIDs []string `json:"required_role_ids,omitempty"`
	RequiredUserID int64    `json:"required_user_id,omitempty"`   // first user; legacy clients
	RequiredUserIDs []int64 `json:"required_user_ids,omitempty"`
	GroupIDs       []int64  `json:"group_ids,omitempty"`
	Restricted     bool     `json:"restricted"`
	OwnerUserID    int64    `json:"owner_user_id,omitempty"`
	SharedUserIDs  []int64  `json:"shared_user_ids,omitempty"`
	Aliases        []string `json:"aliases"`
	Tags           []string `json:"tags"`
	Characters     []string `json:"characters"`
}

// DirectoryUser is a lightweight SSO user entry for share pickers (all authenticated clients).
type DirectoryUser struct {
	ID          int64  `json:"id"`
	DiscordID   string `json:"discord_id"`
	DisplayName string `json:"display_name"`
}

type FullState struct {
	Accounts []EQAccountMeta `json:"accounts"`
	Online   []OnlineEntry   `json:"online"`
}

type OnlineEntry struct {
	AccountID     int64  `json:"account_id"`
	CharacterName string `json:"character_name"`
}

func (s *Store) UpsertUser(ctx context.Context, discordID, displayName string, roleIDs []string) (User, error) {
	if roleIDs == nil {
		roleIDs = []string{}
	}
	b, _ := json.Marshal(roleIDs)
	var u User
	var raw string
	err := s.DB.QueryRowContext(ctx, `
		INSERT INTO users (discord_id, display_name, role_ids_json)
		VALUES ($1, $2, $3)
		ON CONFLICT (discord_id) DO UPDATE SET
		  display_name = EXCLUDED.display_name,
		  role_ids_json = CASE
		    WHEN EXCLUDED.role_ids_json = '[]' THEN users.role_ids_json
		    ELSE EXCLUDED.role_ids_json
		  END
		RETURNING id, discord_id, display_name, role_ids_json, access_revoked
	`, discordID, displayName, string(b)).Scan(&u.ID, &u.DiscordID, &u.DisplayName, &raw, &u.AccessRevoked)
	if err != nil {
		return User{}, err
	}
	_ = json.Unmarshal([]byte(raw), &u.RoleIDs)
	return u, nil
}

func (s *Store) UserByDiscordID(ctx context.Context, discordID string) (User, error) {
	var u User
	var raw string
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, discord_id, display_name, role_ids_json, access_revoked FROM users WHERE discord_id=$1
	`, discordID).Scan(&u.ID, &u.DiscordID, &u.DisplayName, &raw, &u.AccessRevoked)
	if err != nil {
		return User{}, err
	}
	_ = json.Unmarshal([]byte(raw), &u.RoleIDs)
	return u, nil
}

func (s *Store) UserByID(ctx context.Context, id int64) (User, error) {
	var u User
	var raw string
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, discord_id, display_name, role_ids_json, access_revoked FROM users WHERE id=$1
	`, id).Scan(&u.ID, &u.DiscordID, &u.DisplayName, &raw, &u.AccessRevoked)
	if err != nil {
		return User{}, err
	}
	_ = json.Unmarshal([]byte(raw), &u.RoleIDs)
	return u, nil
}

func (s *Store) SetUserRoles(ctx context.Context, userID int64, roleIDs []string) error {
	if roleIDs == nil {
		roleIDs = []string{}
	}
	// Dedupe + trim
	seen := map[string]bool{}
	out := make([]string, 0, len(roleIDs))
	for _, r := range roleIDs {
		r = strings.TrimSpace(r)
		if r == "" || seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	b, _ := json.Marshal(out)
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET role_ids_json=$2 WHERE id=$1`, userID, string(b))
	return err
}

// ListUsersForRoleSync returns all known Discord users (for bot role cache refresh).
func (s *Store) ListUsersForRoleSync(ctx context.Context) ([]User, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, discord_id, display_name, role_ids_json, access_revoked
		FROM users
		WHERE discord_id <> ''
		ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		var raw string
		if err := rows.Scan(&u.ID, &u.DiscordID, &u.DisplayName, &raw, &u.AccessRevoked); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(raw), &u.RoleIDs)
		if u.RoleIDs == nil {
			u.RoleIDs = []string{}
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) SetUserAccessRevoked(ctx context.Context, userID int64, revoked bool) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE users SET access_revoked=$2 WHERE id=$1`, userID, revoked)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("user not found")
	}
	if revoked {
		// Kill active tokens so reconnects fail immediately.
		_, _ = s.DB.ExecContext(ctx, `
			UPDATE api_tokens SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL
		`, userID)
	}
	return nil
}

func (s *Store) CreateToken(ctx context.Context, userID int64) (raw string, id int64, err error) {
	u, err := s.UserByID(ctx, userID)
	if err != nil {
		return "", 0, err
	}
	if u.AccessRevoked {
		return "", 0, fmt.Errorf("access revoked by an admin; contact a guild admin")
	}
	// One active token per Discord user: revoke any existing active tokens first.
	if _, err := s.DB.ExecContext(ctx, `
		UPDATE api_tokens SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL
	`, userID); err != nil {
		return "", 0, err
	}
	raw = crypto.MustRandToken(40)
	hash := crypto.HashToken(raw)
	cipher, err := s.AEAD.SealString(raw, "api_tokens.token")
	if err != nil {
		return "", 0, err
	}
	err = s.DB.QueryRowContext(ctx, `
		INSERT INTO api_tokens (user_id, token_hash, token_cipher, label) VALUES ($1,$2,$3,'') RETURNING id
	`, userID, hash, cipher).Scan(&id)
	return raw, id, err
}

func (s *Store) RevokeToken(ctx context.Context, userID, tokenID int64) error {
	var res sql.Result
	var err error
	if tokenID > 0 {
		res, err = s.DB.ExecContext(ctx, `
			UPDATE api_tokens SET revoked_at=now() WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL
		`, tokenID, userID)
	} else {
		res, err = s.DB.ExecContext(ctx, `
			UPDATE api_tokens SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL
		`, userID)
	}
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("no active token found")
	}
	return nil
}

type TokenRow struct {
	ID        int64
	Label     string
	Raw       string // decrypted secret when available
	HasSecret bool
	Revoked   bool
	CreatedAt time.Time
	LastUsed  *time.Time
}

// ActiveToken returns the user's single non-revoked token, if any.
func (s *Store) ActiveToken(ctx context.Context, userID int64) (TokenRow, bool, error) {
	rows, err := s.ListTokens(ctx, userID, false)
	if err != nil {
		return TokenRow{}, false, err
	}
	if len(rows) == 0 {
		return TokenRow{}, false, nil
	}
	return rows[0], true, nil
}

func (s *Store) ListTokens(ctx context.Context, userID int64, includeRevoked bool) ([]TokenRow, error) {
	q := `
		SELECT id, label, token_cipher, revoked_at, last_used_at, created_at
		FROM api_tokens WHERE user_id=$1`
	if !includeRevoked {
		q += ` AND revoked_at IS NULL`
	}
	q += ` ORDER BY id DESC`
	rows, err := s.DB.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TokenRow
	for rows.Next() {
		var t TokenRow
		var cipher []byte
		var revoked, lastUsed sql.NullTime
		if err := rows.Scan(&t.ID, &t.Label, &cipher, &revoked, &lastUsed, &t.CreatedAt); err != nil {
			return nil, err
		}
		t.Revoked = revoked.Valid
		if lastUsed.Valid {
			t.LastUsed = &lastUsed.Time
		}
		if len(cipher) > 0 && s.AEAD != nil {
			if raw, err := s.AEAD.OpenString(cipher, "api_tokens.token"); err == nil {
				t.Raw = raw
				t.HasSecret = true
			}
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) UserByToken(ctx context.Context, rawToken string) (User, int64, error) {
	hash := crypto.HashToken(rawToken)
	var userID, tokenID int64
	err := s.DB.QueryRowContext(ctx, `
		SELECT user_id, id FROM api_tokens WHERE token_hash=$1 AND revoked_at IS NULL
	`, hash).Scan(&userID, &tokenID)
	if err != nil {
		return User{}, 0, err
	}
	u, err := s.UserByID(ctx, userID)
	if err != nil {
		return User{}, 0, err
	}
	if u.AccessRevoked {
		return User{}, 0, fmt.Errorf("access revoked")
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE api_tokens SET last_used_at=now() WHERE id=$1`, tokenID)
	return u, tokenID, nil
}

func (s *Store) CreateGroup(ctx context.Context, name, desc, webRole string, discordCommands []string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, fmt.Errorf("name required")
	}
	webRole, err := NormalizeWebRole(webRole)
	if err != nil {
		return 0, err
	}
	cmdJSON, err := marshalDiscordCommands(discordCommands)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.DB.QueryRowContext(ctx, `
		INSERT INTO account_groups (name, description, web_role, discord_commands) VALUES ($1,$2,$3,$4::jsonb) RETURNING id
	`, name, desc, webRole, cmdJSON).Scan(&id)
	return id, err
}

// FindGroupIDByName looks up a group by name (case-insensitive).
func (s *Store) FindGroupIDByName(ctx context.Context, name string) (int64, bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, false, nil
	}
	var id int64
	err := s.DB.QueryRowContext(ctx, `
		SELECT id FROM account_groups WHERE lower(name)=lower($1) LIMIT 1
	`, name).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

// GetEQAccountMeta loads account metadata (admin/export use; no viewer ACL filter).
func (s *Store) GetEQAccountMeta(ctx context.Context, id int64) (EQAccountMeta, error) {
	return s.loadEQAccountMeta(ctx, id, nil)
}

// NormalizeWebRole accepts "", "admin", "readonly" (case-insensitive). Empty means no web UI grant.
func NormalizeWebRole(role string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "", "none", "off", "disabled":
		return "", nil
	case "admin":
		return "admin", nil
	case "readonly", "read_only", "read-only", "viewer":
		return "readonly", nil
	default:
		return "", fmt.Errorf("web_role must be empty, admin, or readonly")
	}
}

type GroupUser struct {
	ID          int64  `json:"id"`
	DiscordID   string `json:"discord_id"`
	DisplayName string `json:"display_name"`
}

type GroupDetail struct {
	ID               int64       `json:"id"`
	Name             string      `json:"name"`
	Description      string      `json:"description"`
	WebRole          string      `json:"web_role"` // "", "admin", or "readonly"
	DiscordCommands  []string    `json:"discord_commands"`
	Users            []GroupUser `json:"users"`
	UserIDs          []int64     `json:"user_ids"` // legacy / Discord bot
	RoleIDs          []string    `json:"role_ids"`
	AccountIDs       []int64     `json:"account_ids"`
}

func (s *Store) ListGroups(ctx context.Context) ([]map[string]any, error) {
	details, err := s.ListGroupDetails(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(details))
	for _, g := range details {
		out = append(out, map[string]any{
			"id": g.ID, "name": g.Name, "description": g.Description, "web_role": g.WebRole,
			"discord_commands": g.DiscordCommands,
		})
	}
	return out, nil
}

// ListGroupDetails returns groups with member users/roles and linked EQ accounts.
func (s *Store) ListGroupDetails(ctx context.Context) ([]GroupDetail, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, name, description, COALESCE(web_role, ''), COALESCE(discord_commands, '[]'::jsonb)
		FROM account_groups ORDER BY lower(name), id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GroupDetail
	for rows.Next() {
		var g GroupDetail
		var cmdRaw []byte
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.WebRole, &cmdRaw); err != nil {
			return nil, err
		}
		g.DiscordCommands, err = parseDiscordCommandsJSON(cmdRaw)
		if err != nil {
			return nil, err
		}
		g.UserIDs = []int64{}
		g.Users = []GroupUser{}
		g.RoleIDs = []string{}
		g.AccountIDs = []int64{}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		return []GroupDetail{}, nil
	}
	byID := make(map[int64]*GroupDetail, len(out))
	for i := range out {
		byID[out[i].ID] = &out[i]
	}
	mrows, err := s.DB.QueryContext(ctx, `
		SELECT gm.group_id, gm.user_id, COALESCE(gm.discord_role_id, ''),
		       COALESCE(u.discord_id, ''), COALESCE(u.display_name, '')
		FROM group_members gm
		LEFT JOIN users u ON u.id = gm.user_id
		ORDER BY gm.group_id, u.display_name, gm.user_id, gm.discord_role_id
	`)
	if err == nil {
		defer mrows.Close()
		for mrows.Next() {
			var gid int64
			var uid sql.NullInt64
			var roleID, discordID, displayName string
			if err := mrows.Scan(&gid, &uid, &roleID, &discordID, &displayName); err != nil {
				continue
			}
			g := byID[gid]
			if g == nil {
				continue
			}
			if uid.Valid && uid.Int64 > 0 {
				g.UserIDs = append(g.UserIDs, uid.Int64)
				g.Users = append(g.Users, GroupUser{
					ID: uid.Int64, DiscordID: discordID, DisplayName: displayName,
				})
			}
			if roleID != "" {
				g.RoleIDs = append(g.RoleIDs, roleID)
			}
		}
	}
	arows, err := s.DB.QueryContext(ctx, `
		SELECT group_id, eq_account_id FROM account_group_links ORDER BY group_id, eq_account_id
	`)
	if err == nil {
		defer arows.Close()
		for arows.Next() {
			var gid, aid int64
			if err := arows.Scan(&gid, &aid); err != nil {
				continue
			}
			if g := byID[gid]; g != nil {
				g.AccountIDs = append(g.AccountIDs, aid)
			}
		}
	}
	return out, nil
}

// ListGroupsForUser returns groups the user can access via direct membership or Discord role.
func (s *Store) ListGroupsForUser(ctx context.Context, u User) ([]map[string]any, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT DISTINCT g.id, g.name, g.description, COALESCE(g.web_role, '')
		FROM account_groups g
		JOIN group_members gm ON gm.group_id = g.id
		WHERE gm.user_id = $1
		   OR (
		        gm.discord_role_id IS NOT NULL
		        AND EXISTS (
		            SELECT 1 FROM users uu
		            WHERE uu.id = $1
		              AND uu.role_ids_json::jsonb ? gm.discord_role_id
		        )
		   )
		ORDER BY g.name
	`, u.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id int64
		var name, desc, webRole string
		if err := rows.Scan(&id, &name, &desc, &webRole); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "name": name, "description": desc, "web_role": webRole})
	}
	return out, rows.Err()
}

// HighestWebRoleForUser returns "admin", "readonly", or "" from group membership only.
func (s *Store) HighestWebRoleForUser(ctx context.Context, u User) (string, error) {
	groups, err := s.ListGroupsForUser(ctx, u)
	if err != nil {
		return "", err
	}
	highest := ""
	for _, g := range groups {
		role, _ := g["web_role"].(string)
		role, _ = NormalizeWebRole(role)
		if role == "admin" {
			return "admin", nil
		}
		if role == "readonly" {
			highest = "readonly"
		}
	}
	return highest, nil
}

func (s *Store) AddGroupUser(ctx context.Context, groupID, userID int64) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO group_members (group_id, user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, groupID, userID)
	return err
}

func (s *Store) RemoveGroupUser(ctx context.Context, groupID, userID int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM group_members WHERE group_id=$1 AND user_id=$2`, groupID, userID)
	return err
}

func (s *Store) AddGroupRole(ctx context.Context, groupID int64, roleID string) error {
	roleID = strings.TrimSpace(roleID)
	if roleID == "" {
		return fmt.Errorf("role required")
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO group_members (group_id, discord_role_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, groupID, roleID)
	return err
}

func (s *Store) RemoveGroupRole(ctx context.Context, groupID int64, roleID string) error {
	_, err := s.DB.ExecContext(ctx, `
		DELETE FROM group_members WHERE group_id=$1 AND discord_role_id=$2
	`, groupID, strings.TrimSpace(roleID))
	return err
}

func (s *Store) LinkAccountGroup(ctx context.Context, accountID, groupID int64) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO account_group_links (group_id, eq_account_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, groupID, accountID)
	return err
}

func (s *Store) UnlinkAccountGroup(ctx context.Context, accountID, groupID int64) error {
	_, err := s.DB.ExecContext(ctx, `
		DELETE FROM account_group_links WHERE group_id=$1 AND eq_account_id=$2
	`, groupID, accountID)
	return err
}

// UpdateGroupMeta updates name/description/web_role/discord_commands for a group.
func (s *Store) UpdateGroupMeta(ctx context.Context, groupID int64, name, description, webRole string, discordCommands []string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name required")
	}
	webRole, err := NormalizeWebRole(webRole)
	if err != nil {
		return err
	}
	cmdJSON, err := marshalDiscordCommands(discordCommands)
	if err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `
		UPDATE account_groups SET name=$2, description=$3, web_role=$4, discord_commands=$5::jsonb WHERE id=$1
	`, groupID, name, strings.TrimSpace(description), webRole, cmdJSON)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("group not found")
	}
	return nil
}

// ReplaceGroupMembership sets the group's Discord users and roles (replaces all members).
func (s *Store) ReplaceGroupMembership(ctx context.Context, groupID int64, userIDs []int64, roleIDs []string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM group_members WHERE group_id=$1`, groupID); err != nil {
		return err
	}
	seenUsers := map[int64]struct{}{}
	for _, uid := range userIDs {
		if uid <= 0 {
			continue
		}
		if _, ok := seenUsers[uid]; ok {
			continue
		}
		seenUsers[uid] = struct{}{}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO group_members (group_id, user_id) VALUES ($1,$2)
		`, groupID, uid); err != nil {
			return err
		}
	}
	seenRoles := map[string]struct{}{}
	for _, rid := range roleIDs {
		rid = strings.TrimSpace(rid)
		if rid == "" {
			continue
		}
		if _, ok := seenRoles[rid]; ok {
			continue
		}
		seenRoles[rid] = struct{}{}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO group_members (group_id, discord_role_id) VALUES ($1,$2)
		`, groupID, rid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReplaceGroupAccountLinks sets which EQ accounts are linked to the group.
func (s *Store) ReplaceGroupAccountLinks(ctx context.Context, groupID int64, accountIDs []int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_group_links WHERE group_id=$1`, groupID); err != nil {
		return err
	}
	seen := map[int64]struct{}{}
	for _, aid := range accountIDs {
		if aid <= 0 {
			continue
		}
		if _, ok := seen[aid]; ok {
			continue
		}
		seen[aid] = struct{}{}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO account_group_links (group_id, eq_account_id) VALUES ($1,$2)
		`, groupID, aid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteGroup(ctx context.Context, groupID int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM account_groups WHERE id=$1`, groupID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("group not found")
	}
	return nil
}

func (s *Store) AddEQAccount(ctx context.Context, username, password, notes string) (int64, error) {
	norm := strings.ToLower(strings.TrimSpace(username))
	blind := crypto.BlindIndex(s.Key, norm)
	uc, err := s.AEAD.SealString(username, "eq_accounts.username")
	if err != nil {
		return 0, err
	}
	pc, err := s.AEAD.SealString(password, "eq_accounts.password")
	if err != nil {
		return 0, err
	}
	var nc []byte
	if notes != "" {
		nc, err = s.AEAD.SealString(notes, "eq_accounts.notes")
		if err != nil {
			return 0, err
		}
	}
	var id int64
	err = s.DB.QueryRowContext(ctx, `
		INSERT INTO eq_accounts (username_cipher, username_blind, password_cipher, notes_cipher)
		VALUES ($1,$2,$3,$4) RETURNING id
	`, uc, blind, pc, nc).Scan(&id)
	return id, err
}

// FindEQAccountIDByUsername looks up an account by case-insensitive username (blind index).
func (s *Store) FindEQAccountIDByUsername(ctx context.Context, username string) (int64, bool, error) {
	norm := strings.ToLower(strings.TrimSpace(username))
	if norm == "" {
		return 0, false, nil
	}
	blind := crypto.BlindIndex(s.Key, norm)
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM eq_accounts WHERE username_blind=$1`, blind).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

func (s *Store) SetEQPassword(ctx context.Context, accountID int64, password string) error {
	pc, err := s.AEAD.SealString(password, "eq_accounts.password")
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE eq_accounts SET password_cipher=$2, updated_at=now() WHERE id=$1`, accountID, pc)
	return err
}

func (s *Store) SetEQDisabled(ctx context.Context, accountID int64, disabled bool) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE eq_accounts SET disabled=$2, updated_at=now() WHERE id=$1`, accountID, disabled)
	return err
}

// SetEQRequiredRole sets a single Discord role grant (replaces all role grants). Empty clears roles.
func (s *Store) SetEQRequiredRole(ctx context.Context, accountID int64, roleID string) error {
	roleID = strings.TrimSpace(roleID)
	if roleID == "" {
		return s.ReplaceAccountRoles(ctx, accountID, nil)
	}
	return s.ReplaceAccountRoles(ctx, accountID, []string{roleID})
}

// SetEQRequiredUser sets a single-user access grant (replaces all user grants). userID 0 clears users.
func (s *Store) SetEQRequiredUser(ctx context.Context, accountID, userID int64) error {
	if userID <= 0 {
		return s.ReplaceAccountUsers(ctx, accountID, nil)
	}
	return s.ReplaceAccountUsers(ctx, accountID, []int64{userID})
}

// ReplaceAccountRoles replaces Discord role grants for an account.
func (s *Store) ReplaceAccountRoles(ctx context.Context, accountID int64, roleIDs []string) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM account_access_roles WHERE eq_account_id=$1`, accountID); err != nil {
		return err
	}
	seen := map[string]bool{}
	first := ""
	for _, rid := range roleIDs {
		rid = strings.TrimSpace(rid)
		if rid == "" || seen[rid] {
			continue
		}
		seen[rid] = true
		if first == "" {
			first = rid
		}
		if _, err := s.DB.ExecContext(ctx, `
			INSERT INTO account_access_roles (eq_account_id, discord_role_id) VALUES ($1,$2)
			ON CONFLICT DO NOTHING
		`, accountID, rid); err != nil {
			return err
		}
	}
	_, err := s.DB.ExecContext(ctx, `
		UPDATE eq_accounts SET required_discord_role_id=$2, updated_at=now() WHERE id=$1
	`, accountID, first)
	return err
}

// ReplaceAccountUsers replaces Discord user grants for an account.
func (s *Store) ReplaceAccountUsers(ctx context.Context, accountID int64, userIDs []int64) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM account_access_users WHERE eq_account_id=$1`, accountID); err != nil {
		return err
	}
	seen := map[int64]bool{}
	var first int64
	for _, uid := range userIDs {
		if uid <= 0 || seen[uid] {
			continue
		}
		seen[uid] = true
		if first == 0 {
			first = uid
		}
		if _, err := s.DB.ExecContext(ctx, `
			INSERT INTO account_access_users (eq_account_id, user_id) VALUES ($1,$2)
			ON CONFLICT DO NOTHING
		`, accountID, uid); err != nil {
			return err
		}
	}
	if first <= 0 {
		_, err := s.DB.ExecContext(ctx, `
			UPDATE eq_accounts SET required_user_id=NULL, updated_at=now() WHERE id=$1
		`, accountID)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `
		UPDATE eq_accounts SET required_user_id=$2, updated_at=now() WHERE id=$1
	`, accountID, first)
	return err
}

// ReplaceAccountGroups replaces which access groups may use an account.
func (s *Store) ReplaceAccountGroups(ctx context.Context, accountID int64, groupIDs []int64) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM account_group_links WHERE eq_account_id=$1`, accountID); err != nil {
		return err
	}
	seen := map[int64]bool{}
	for _, gid := range groupIDs {
		if gid <= 0 || seen[gid] {
			continue
		}
		seen[gid] = true
		if _, err := s.DB.ExecContext(ctx, `
			INSERT INTO account_group_links (group_id, eq_account_id) VALUES ($1,$2)
			ON CONFLICT DO NOTHING
		`, gid, accountID); err != nil {
			return err
		}
	}
	return nil
}

// SetAccountAccess sets SSO access grants for a non-restricted account.
// Empty roleIDs, userIDs, and groupIDs means available to all authenticated SSO users.
// When any grant is set, the user must satisfy at least one (role OR user OR group membership).
func (s *Store) SetAccountAccess(ctx context.Context, accountID int64, roleIDs []string, userIDs []int64, groupIDs []int64) error {
	if err := s.ReplaceAccountRoles(ctx, accountID, roleIDs); err != nil {
		return err
	}
	if err := s.ReplaceAccountUsers(ctx, accountID, userIDs); err != nil {
		return err
	}
	return s.ReplaceAccountGroups(ctx, accountID, groupIDs)
}

func (s *Store) AddAlias(ctx context.Context, alias string, accountID int64) error {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return fmt.Errorf("alias required")
	}
	var existing int64
	err := s.DB.QueryRowContext(ctx, `
		SELECT eq_account_id FROM aliases WHERE lower(alias)=lower($1) LIMIT 1
	`, alias).Scan(&existing)
	if err == nil {
		if existing == accountID {
			return nil
		}
		return fmt.Errorf("alias already used by another account")
	}
	if err != sql.ErrNoRows {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO aliases (alias, eq_account_id) VALUES ($1,$2)`, alias, accountID)
	return err
}

func (s *Store) RemoveAlias(ctx context.Context, alias string, accountID int64) error {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return fmt.Errorf("alias required")
	}
	res, err := s.DB.ExecContext(ctx, `
		DELETE FROM aliases WHERE eq_account_id=$1 AND lower(alias)=lower($2)
	`, accountID, alias)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("alias not found on account")
	}
	return nil
}

func (s *Store) AddTag(ctx context.Context, tag string, accountID int64) error {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return fmt.Errorf("tag required")
	}
	var n int
	err := s.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM account_tags WHERE lower(tag)=lower($1) AND eq_account_id=$2
	`, tag, accountID).Scan(&n)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO account_tags (tag, eq_account_id) VALUES ($1,$2)`, tag, accountID)
	return err
}

func (s *Store) RemoveTag(ctx context.Context, tag string, accountID int64) error {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return fmt.Errorf("tag required")
	}
	res, err := s.DB.ExecContext(ctx, `
		DELETE FROM account_tags WHERE eq_account_id=$1 AND lower(tag)=lower($2)
	`, accountID, tag)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("tag not found on account")
	}
	return nil
}

func (s *Store) AddCharacter(ctx context.Context, name string, accountID int64) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("character name required")
	}
	var existing int64
	err := s.DB.QueryRowContext(ctx, `
		SELECT eq_account_id FROM characters WHERE lower(name)=lower($1) LIMIT 1
	`, name).Scan(&existing)
	if err == nil {
		if existing == accountID {
			return nil
		}
		return fmt.Errorf("character already used by another account")
	}
	if err != sql.ErrNoRows {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO characters (name, eq_account_id) VALUES ($1,$2)`, name, accountID)
	return err
}

func (s *Store) RemoveCharacter(ctx context.Context, name string, accountID int64) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("character name required")
	}
	res, err := s.DB.ExecContext(ctx, `
		DELETE FROM characters WHERE eq_account_id=$1 AND lower(name)=lower($2)
	`, accountID, name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("character not found on account")
	}
	return nil
}

// CharacterListing is a character row for Discord / admin listings.
type CharacterListing struct {
	Name      string
	AccountID int64
	Username  string
}

// ListEQAccountMetas returns account metadata. If includeDisabled is false, only enabled accounts.
// When forUser is non-nil, only accounts that user may use (same rules as AllowedAccountIDs).
// When forUser is nil, returns all accounts (admin listing).
func (s *Store) ListEQAccountMetas(ctx context.Context, forUser *User, includeDisabled bool) ([]EQAccountMeta, error) {
	var ids []int64
	var err error
	if forUser != nil {
		ids, err = s.AllowedAccountIDs(ctx, *forUser)
		if err != nil {
			return nil, err
		}
	} else {
		q := `SELECT id FROM eq_accounts`
		if !includeDisabled {
			q += ` WHERE disabled = false`
		}
		q += ` ORDER BY id`
		rows, qerr := s.DB.QueryContext(ctx, q)
		if qerr != nil {
			return nil, qerr
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	out := make([]EQAccountMeta, 0, len(ids))
	for _, id := range ids {
		meta, err := s.loadEQAccountMeta(ctx, id, forUser)
		if err != nil {
			continue
		}
		out = append(out, meta)
	}
	sort.Slice(out, func(i, j int) bool {
		ni := strings.ToLower(out[i].Username)
		nj := strings.ToLower(out[j].Username)
		if ni == nj {
			return out[i].ID < out[j].ID
		}
		if ni == "" {
			return false
		}
		if nj == "" {
			return true
		}
		return ni < nj
	})
	return out, nil
}

func (s *Store) LoadEQAccountMeta(ctx context.Context, id int64) (EQAccountMeta, error) {
	return s.loadEQAccountMeta(ctx, id, nil)
}

// LoadEQAccountMetaForViewer loads account metadata with share recipient lists visible only to the owner.
func (s *Store) LoadEQAccountMetaForViewer(ctx context.Context, id int64, viewer User) (EQAccountMeta, error) {
	return s.loadEQAccountMeta(ctx, id, &viewer)
}

func (s *Store) loadEQAccountMeta(ctx context.Context, id int64, viewer *User) (EQAccountMeta, error) {
	var disabled, restricted bool
	var reqRole string
	var ownerID, reqUser sql.NullInt64
	var uc []byte
	if err := s.DB.QueryRowContext(ctx, `
		SELECT disabled, required_discord_role_id, username_cipher,
		       COALESCE(restricted, false), owner_user_id, required_user_id
		FROM eq_accounts WHERE id=$1
	`, id).Scan(&disabled, &reqRole, &uc, &restricted, &ownerID, &reqUser); err != nil {
		return EQAccountMeta{}, err
	}
	meta := EQAccountMeta{
		ID: id, Disabled: disabled, Elevated: reqRole != "" || (reqUser.Valid && reqUser.Int64 > 0),
		RequiredRoleID: reqRole, Restricted: restricted,
	}
	if ownerID.Valid {
		meta.OwnerUserID = ownerID.Int64
	}
	if reqUser.Valid {
		meta.RequiredUserID = reqUser.Int64
	}
	if len(uc) > 0 && s.AEAD != nil {
		if name, err := s.AEAD.OpenString(uc, "eq_accounts.username"); err == nil {
			meta.Username = name
		}
	}
	s.fillAccountLists(ctx, &meta)
	meta.RequiredRoleIDs = s.listAccountRoleIDs(ctx, id)
	if meta.RequiredRoleIDs == nil {
		meta.RequiredRoleIDs = []string{}
	}
	if len(meta.RequiredRoleIDs) > 0 {
		meta.RequiredRoleID = meta.RequiredRoleIDs[0]
	} else {
		meta.RequiredRoleID = ""
	}
	meta.RequiredUserIDs = s.listAccountUserIDs(ctx, id)
	if meta.RequiredUserIDs == nil {
		meta.RequiredUserIDs = []int64{}
	}
	if len(meta.RequiredUserIDs) > 0 {
		meta.RequiredUserID = meta.RequiredUserIDs[0]
	} else {
		meta.RequiredUserID = 0
	}
	meta.GroupIDs = s.listAccountGroupIDs(ctx, id)
	if meta.GroupIDs == nil {
		meta.GroupIDs = []int64{}
	}
	meta.Elevated = len(meta.RequiredRoleIDs) > 0 || len(meta.RequiredUserIDs) > 0 || len(meta.GroupIDs) > 0
	showShares := viewer == nil || (meta.OwnerUserID > 0 && viewer.ID == meta.OwnerUserID)
	if showShares && restricted {
		meta.SharedUserIDs = s.listShareUserIDs(ctx, id)
	}
	return meta, nil
}

func (s *Store) listAccountRoleIDs(ctx context.Context, accountID int64) []string {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT discord_role_id FROM account_access_roles WHERE eq_account_id=$1 ORDER BY discord_role_id
	`, accountID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		out = append(out, id)
	}
	return out
}

func (s *Store) listAccountUserIDs(ctx context.Context, accountID int64) []int64 {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT user_id FROM account_access_users WHERE eq_account_id=$1 ORDER BY user_id
	`, accountID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			continue
		}
		out = append(out, id)
	}
	return out
}

func (s *Store) listAccountGroupIDs(ctx context.Context, accountID int64) []int64 {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT group_id FROM account_group_links WHERE eq_account_id=$1 ORDER BY group_id
	`, accountID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return out
		}
		out = append(out, id)
	}
	return out
}

func (s *Store) listShareUserIDs(ctx context.Context, accountID int64) []int64 {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT user_id FROM account_shares WHERE eq_account_id=$1 ORDER BY user_id
	`, accountID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return out
		}
		out = append(out, id)
	}
	return out
}

func (s *Store) fillAccountLists(ctx context.Context, meta *EQAccountMeta) {
	arows, _ := s.DB.QueryContext(ctx, `SELECT alias FROM aliases WHERE eq_account_id=$1 ORDER BY lower(alias), alias`, meta.ID)
	if arows != nil {
		for arows.Next() {
			var a string
			_ = arows.Scan(&a)
			meta.Aliases = append(meta.Aliases, a)
		}
		arows.Close()
	}
	trows, _ := s.DB.QueryContext(ctx, `SELECT tag FROM account_tags WHERE eq_account_id=$1 ORDER BY lower(tag), tag`, meta.ID)
	if trows != nil {
		for trows.Next() {
			var t string
			_ = trows.Scan(&t)
			meta.Tags = append(meta.Tags, t)
		}
		trows.Close()
	}
	crows, _ := s.DB.QueryContext(ctx, `SELECT name FROM characters WHERE eq_account_id=$1 ORDER BY name`, meta.ID)
	if crows != nil {
		for crows.Next() {
			var n string
			_ = crows.Scan(&n)
			meta.Characters = append(meta.Characters, n)
		}
		crows.Close()
	}
	if meta.Aliases == nil {
		meta.Aliases = []string{}
	}
	if meta.Tags == nil {
		meta.Tags = []string{}
	}
	if meta.Characters == nil {
		meta.Characters = []string{}
	}
}

// ListCharacters returns characters, optionally filtered by accountID (0 = all).
// Usernames are decrypted when available. Sorted by character name.
func (s *Store) ListCharacters(ctx context.Context, accountID int64) ([]CharacterListing, error) {
	q := `
		SELECT c.name, c.eq_account_id, a.username_cipher
		FROM characters c
		JOIN eq_accounts a ON a.id = c.eq_account_id
	`
	args := []any{}
	if accountID > 0 {
		q += ` WHERE c.eq_account_id = $1`
		args = append(args, accountID)
	}
	q += ` ORDER BY lower(c.name)`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CharacterListing
	for rows.Next() {
		var cl CharacterListing
		var uc []byte
		if err := rows.Scan(&cl.Name, &cl.AccountID, &uc); err != nil {
			return nil, err
		}
		if len(uc) > 0 && s.AEAD != nil {
			if name, err := s.AEAD.OpenString(uc, "eq_accounts.username"); err == nil {
				cl.Username = name
			}
		}
		out = append(out, cl)
	}
	return out, rows.Err()
}

func (s *Store) AccountIDByCharacter(ctx context.Context, name string) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT eq_account_id FROM characters WHERE lower(name)=lower($1)`, name).Scan(&id)
	return id, err
}

func (s *Store) DecryptCredentials(ctx context.Context, accountID int64) (username, password string, err error) {
	return s.decryptCredentials(ctx, accountID, false)
}

// DecryptCredentialsAny decrypts credentials even when the account is disabled (admin export).
func (s *Store) DecryptCredentialsAny(ctx context.Context, accountID int64) (username, password string, err error) {
	return s.decryptCredentials(ctx, accountID, true)
}

func (s *Store) decryptCredentials(ctx context.Context, accountID int64, includeDisabled bool) (username, password string, err error) {
	var uc, pc []byte
	q := `SELECT username_cipher, password_cipher FROM eq_accounts WHERE id=$1`
	if !includeDisabled {
		q += ` AND disabled=false`
	}
	err = s.DB.QueryRowContext(ctx, q, accountID).Scan(&uc, &pc)
	if err != nil {
		return "", "", err
	}
	username, err = s.AEAD.OpenString(uc, "eq_accounts.username")
	if err != nil {
		return "", "", err
	}
	password, err = s.AEAD.OpenString(pc, "eq_accounts.password")
	return username, password, err
}

// AllowedAccountIDs returns EQ accounts the user may use.
// Restricted accounts: owner, explicit user shares, and any linked Discord role or access group.
// Otherwise, if the account has no role/user/group grants → any authenticated SSO user ("all").
// If any grant is set, the user must match at least one: required role, required user, or membership
// in a linked access group (direct user membership or holding a Discord role on the group).
func (s *Store) AllowedAccountIDs(ctx context.Context, u User) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT a.id
		FROM eq_accounts a
		WHERE a.disabled = false
		  AND (
		    (
		      a.restricted = true
		      AND (
		        a.owner_user_id = $1
		        OR EXISTS (
		            SELECT 1 FROM account_shares sh
		            WHERE sh.eq_account_id = a.id AND sh.user_id = $1
		        )
		        OR EXISTS (
		            SELECT 1 FROM account_access_roles ar
		            WHERE ar.eq_account_id = a.id
		              AND EXISTS (
		                  SELECT 1 FROM users uu
		                  WHERE uu.id = $1
		                    AND uu.role_ids_json::jsonb ? ar.discord_role_id
		              )
		        )
		        OR EXISTS (
		            SELECT 1
		            FROM account_group_links gl
		            JOIN group_members gm ON gm.group_id = gl.group_id
		            WHERE gl.eq_account_id = a.id
		              AND (
		                gm.user_id = $1
		                OR (
		                  gm.discord_role_id IS NOT NULL AND gm.discord_role_id <> ''
		                  AND EXISTS (
		                      SELECT 1 FROM users uu
		                      WHERE uu.id = $1
		                        AND uu.role_ids_json::jsonb ? gm.discord_role_id
		                  )
		                )
		              )
		        )
		      )
		    )
		    OR (
		      COALESCE(a.restricted, false) = false
		      AND (
		        (
		          NOT EXISTS (SELECT 1 FROM account_access_roles ar WHERE ar.eq_account_id = a.id)
		          AND NOT EXISTS (SELECT 1 FROM account_access_users au WHERE au.eq_account_id = a.id)
		          AND NOT EXISTS (SELECT 1 FROM account_group_links gl WHERE gl.eq_account_id = a.id)
		        )
		        OR EXISTS (
		            SELECT 1 FROM account_access_roles ar
		            WHERE ar.eq_account_id = a.id
		              AND EXISTS (
		                  SELECT 1 FROM users uu
		                  WHERE uu.id = $1
		                    AND uu.role_ids_json::jsonb ? ar.discord_role_id
		              )
		        )
		        OR EXISTS (
		            SELECT 1 FROM account_access_users au
		            WHERE au.eq_account_id = a.id AND au.user_id = $1
		        )
		        OR EXISTS (
		            SELECT 1
		            FROM account_group_links gl
		            JOIN group_members gm ON gm.group_id = gl.group_id
		            WHERE gl.eq_account_id = a.id
		              AND (
		                gm.user_id = $1
		                OR (
		                  gm.discord_role_id IS NOT NULL AND gm.discord_role_id <> ''
		                  AND EXISTS (
		                      SELECT 1 FROM users uu
		                      WHERE uu.id = $1
		                        AND uu.role_ids_json::jsonb ? gm.discord_role_id
		                  )
		                )
		              )
		        )
		      )
		    )
		  )
		ORDER BY a.id
	`, u.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) ResolveLoginCandidates(ctx context.Context, u User, name string) ([]LoginCandidate, error) {
	allowed, err := s.AllowedAccountIDs(ctx, u)
	if err != nil {
		return nil, err
	}
	if len(allowed) == 0 {
		return nil, nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	blind := crypto.BlindIndex(s.Key, strings.ToLower(name))
	args := make([]any, 0, len(allowed)+2)
	args = append(args, name, blind)
	ph := make([]string, len(allowed))
	for i, id := range allowed {
		args = append(args, id)
		ph[i] = fmt.Sprintf("$%d", i+3)
	}
	q := fmt.Sprintf(`
		SELECT DISTINCT a.id,
		  (a.username_blind = $2) AS by_user,
		  EXISTS (SELECT 1 FROM account_tags t WHERE t.eq_account_id=a.id AND lower(t.tag)=lower($1)) AS by_tag,
		  EXISTS (SELECT 1 FROM aliases al WHERE al.eq_account_id=a.id AND lower(al.alias)=lower($1)) AS by_alias,
		  EXISTS (SELECT 1 FROM characters c WHERE c.eq_account_id=a.id AND lower(c.name)=lower($1)) AS by_char
		FROM eq_accounts a
		WHERE a.disabled = false
		  AND a.id IN (%s)
		  AND (
		    a.username_blind = $2
		    OR EXISTS (SELECT 1 FROM account_tags t WHERE t.eq_account_id=a.id AND lower(t.tag)=lower($1))
		    OR EXISTS (SELECT 1 FROM aliases al WHERE al.eq_account_id=a.id AND lower(al.alias)=lower($1))
		    OR EXISTS (SELECT 1 FROM characters c WHERE c.eq_account_id=a.id AND lower(c.name)=lower($1))
		  )
		ORDER BY a.id
	`, strings.Join(ph, ","))
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LoginCandidate
	for rows.Next() {
		var c LoginCandidate
		var byUser, byTag, byAlias, byChar bool
		if err := rows.Scan(&c.ID, &byUser, &byTag, &byAlias, &byChar); err != nil {
			return nil, err
		}
		c.ByUser = byUser
		c.ByTag = byTag
		c.ByAlias = byAlias
		c.ByCharacter = byChar
		out = append(out, c)
	}
	return out, rows.Err()
}

// LoginCandidate is an EQ account that matched a login_auth username/tag/alias/character.
type LoginCandidate struct {
	ID          int64
	ByUser      bool
	ByTag       bool
	ByAlias     bool
	ByCharacter bool
}

// Direct reports a concrete identity match (not tag-pool rotation).
func (c LoginCandidate) Direct() bool {
	return c.ByUser || c.ByAlias || c.ByCharacter
}

func (s *Store) FullStateForUser(ctx context.Context, u User, online []OnlineEntry) (FullState, error) {
	ids, err := s.AllowedAccountIDs(ctx, u)
	if err != nil {
		return FullState{}, err
	}
	fs := FullState{Accounts: []EQAccountMeta{}, Online: online}
	if len(ids) == 0 {
		return fs, nil
	}
	for _, id := range ids {
		meta, err := s.loadEQAccountMeta(ctx, id, &u)
		if err != nil {
			continue
		}
		fs.Accounts = append(fs.Accounts, meta)
	}
	sort.Slice(fs.Accounts, func(i, j int) bool {
		ni := strings.ToLower(fs.Accounts[i].Username)
		nj := strings.ToLower(fs.Accounts[j].Username)
		if ni == nj {
			return fs.Accounts[i].ID < fs.Accounts[j].ID
		}
		if ni == "" {
			return false
		}
		if nj == "" {
			return true
		}
		return ni < nj
	})
	return fs, nil
}

func (s *Store) Audit(ctx context.Context, userID int64, action string, detail string) {
	s.AuditAccount(ctx, userID, 0, action, detail)
}

// AuditAccount records an audit event optionally tied to an EQ account.
func (s *Store) AuditAccount(ctx context.Context, userID, accountID int64, action, detail string) {
	var detailCipher []byte
	if detail != "" && s.AEAD != nil {
		detailCipher, _ = s.AEAD.SealString(detail, "audit_log.detail")
	}
	if accountID > 0 {
		_, _ = s.DB.ExecContext(ctx, `
			INSERT INTO audit_log (user_id, eq_account_id, action, detail_cipher) VALUES ($1,$2,$3,$4)
		`, userID, accountID, action, detailCipher)
		return
	}
	_, _ = s.DB.ExecContext(ctx, `
		INSERT INTO audit_log (user_id, action, detail_cipher) VALUES ($1,$2,$3)
	`, userID, action, detailCipher)
}

type AuditEntry struct {
	ID              int64     `json:"id"`
	CreatedAt       time.Time `json:"created_at"`
	UserID          int64     `json:"user_id,omitempty"`
	ActorName       string    `json:"actor_name,omitempty"`
	ActorDiscordID  string    `json:"actor_discord_id,omitempty"`
	AccountID       int64     `json:"account_id,omitempty"`
	AccountUsername string    `json:"account_username,omitempty"`
	Action          string    `json:"action"`
	Detail          string    `json:"detail,omitempty"`
}

// ListAccountAudits returns recent audit events related to EQ accounts.
// Optional accountID and/or userID (actor) filters narrow the result.
func (s *Store) ListAccountAudits(ctx context.Context, accountID, userID int64, limit, offset int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	q := `
		SELECT a.id, a.created_at, COALESCE(a.user_id, 0), COALESCE(u.discord_id, ''), COALESCE(u.display_name, ''),
		       COALESCE(a.eq_account_id, 0), a.action, a.detail_cipher
		FROM audit_log a
		LEFT JOIN users u ON u.id = a.user_id
		WHERE (
		    a.eq_account_id IS NOT NULL
		    OR a.action ILIKE '%account%'
		    OR a.action ILIKE '%alias%'
		    OR a.action ILIKE '%tag%'
		    OR a.action ILIKE '%character%'
		    OR a.action ILIKE '%share%'
		    OR a.action ILIKE '%import%'
		    OR a.action ILIKE '%config%'
		    OR a.action = 'login_auth'
		)`
	args := []any{}
	argN := 1
	if accountID > 0 {
		q += fmt.Sprintf(` AND a.eq_account_id = $%d`, argN)
		args = append(args, accountID)
		argN++
	}
	if userID > 0 {
		q += fmt.Sprintf(` AND a.user_id = $%d`, argN)
		args = append(args, userID)
		argN++
	}
	q += fmt.Sprintf(` ORDER BY a.created_at DESC, a.id DESC LIMIT $%d OFFSET $%d`, argN, argN+1)
	args = append(args, limit, offset)

	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]AuditEntry, 0, limit)
	acctIDs := map[int64]struct{}{}
	for rows.Next() {
		var e AuditEntry
		var detailCipher []byte
		if err := rows.Scan(&e.ID, &e.CreatedAt, &e.UserID, &e.ActorDiscordID, &e.ActorName, &e.AccountID, &e.Action, &detailCipher); err != nil {
			return nil, err
		}
		if len(detailCipher) > 0 && s.AEAD != nil {
			if plain, err := s.AEAD.OpenString(detailCipher, "audit_log.detail"); err == nil {
				e.Detail = plain
			}
		}
		if e.AccountID > 0 {
			acctIDs[e.AccountID] = struct{}{}
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	names := map[int64]string{}
	for id := range acctIDs {
		meta, err := s.loadEQAccountMeta(ctx, id, nil)
		if err == nil && meta.Username != "" {
			names[id] = meta.Username
		}
	}
	for i := range out {
		if name, ok := names[out[i].AccountID]; ok {
			out[i].AccountUsername = name
		}
	}
	return out, nil
}

// ShareLoginEntry is a login_auth event on a restricted account the viewer owns.
type ShareLoginEntry struct {
	ID               int64     `json:"id"`
	CreatedAt        time.Time `json:"created_at"`
	UserID           int64     `json:"user_id"`
	ActorName        string    `json:"actor_name,omitempty"`
	ActorDiscordID   string    `json:"actor_discord_id,omitempty"`
	AccountID        int64     `json:"account_id"`
	AccountUsername  string    `json:"account_username,omitempty"`
	Detail           string    `json:"detail,omitempty"` // typed login name
	ActorIsOwner     bool      `json:"actor_is_owner"`
}

// ListOwnedRestrictedAccountIDs returns restricted EQ account IDs owned by ownerID.
func (s *Store) ListOwnedRestrictedAccountIDs(ctx context.Context, ownerID int64) ([]int64, error) {
	if ownerID <= 0 {
		return nil, nil
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id FROM eq_accounts
		WHERE restricted = true AND owner_user_id = $1
		ORDER BY id
	`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ListOwnedShareLogins returns recent SSO login_auth events for restricted accounts owned by ownerID.
func (s *Store) ListOwnedShareLogins(ctx context.Context, ownerID int64, limit int) ([]ShareLoginEntry, error) {
	if ownerID <= 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT a.id, a.created_at, COALESCE(a.user_id, 0), COALESCE(u.discord_id, ''), COALESCE(u.display_name, ''),
		       COALESCE(a.eq_account_id, 0), a.detail_cipher, ea.owner_user_id
		FROM audit_log a
		JOIN eq_accounts ea ON ea.id = a.eq_account_id
		LEFT JOIN users u ON u.id = a.user_id
		WHERE a.action = 'login_auth'
		  AND ea.restricted = true
		  AND ea.owner_user_id = $1
		ORDER BY a.created_at DESC, a.id DESC
		LIMIT $2
	`, ownerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ShareLoginEntry, 0, limit)
	acctIDs := map[int64]struct{}{}
	for rows.Next() {
		var e ShareLoginEntry
		var detailCipher []byte
		var acctOwner int64
		if err := rows.Scan(&e.ID, &e.CreatedAt, &e.UserID, &e.ActorDiscordID, &e.ActorName, &e.AccountID, &detailCipher, &acctOwner); err != nil {
			return nil, err
		}
		if len(detailCipher) > 0 && s.AEAD != nil {
			if plain, err := s.AEAD.OpenString(detailCipher, "audit_log.detail"); err == nil {
				e.Detail = plain
			}
		}
		e.ActorIsOwner = e.UserID > 0 && e.UserID == acctOwner
		if e.AccountID > 0 {
			acctIDs[e.AccountID] = struct{}{}
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	names := map[int64]string{}
	for id := range acctIDs {
		meta, err := s.loadEQAccountMeta(ctx, id, nil)
		if err == nil && meta.Username != "" {
			names[id] = meta.Username
		}
	}
	for i := range out {
		if name, ok := names[out[i].AccountID]; ok {
			out[i].AccountUsername = name
		}
	}
	return out, nil
}

// ShareOnlineEntry is live presence on a restricted account the viewer owns.
type ShareOnlineEntry struct {
	AccountID       int64     `json:"account_id"`
	AccountUsername string    `json:"account_username,omitempty"`
	CharacterName   string    `json:"character_name,omitempty"`
	UserID          int64     `json:"user_id"`
	UserDisplayName string    `json:"user_display_name,omitempty"`
	UserDiscordID   string    `json:"user_discord_id,omitempty"`
	ActorIsOwner    bool      `json:"actor_is_owner"`
	LastSeen        time.Time `json:"last_seen"`
}

// ShareActivity summarizes use of restricted accounts owned by the viewer.
type ShareActivity struct {
	Logins []ShareLoginEntry  `json:"logins"`
	Online []ShareOnlineEntry `json:"online"`
}

// PresenceHint is a live presence row used to build ShareOnlineEntry (avoids importing presence).
type PresenceHint struct {
	AccountID     int64
	CharacterName string
	UserID        int64
	LastSeen      time.Time
}

// BuildShareOnline maps presence rows onto owned restricted accounts.
func (s *Store) BuildShareOnline(ctx context.Context, ownerID int64, rows []PresenceHint) ([]ShareOnlineEntry, error) {
	if ownerID <= 0 || len(rows) == 0 {
		return []ShareOnlineEntry{}, nil
	}
	owned, err := s.ListOwnedRestrictedAccountIDs(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	ownedSet := map[int64]struct{}{}
	for _, id := range owned {
		ownedSet[id] = struct{}{}
	}
	out := make([]ShareOnlineEntry, 0)
	for _, r := range rows {
		if _, ok := ownedSet[r.AccountID]; !ok {
			continue
		}
		e := ShareOnlineEntry{
			AccountID:     r.AccountID,
			CharacterName: r.CharacterName,
			UserID:        r.UserID,
			ActorIsOwner:  r.UserID > 0 && r.UserID == ownerID,
			LastSeen:      r.LastSeen,
		}
		if meta, err := s.loadEQAccountMeta(ctx, r.AccountID, nil); err == nil {
			e.AccountUsername = meta.Username
		}
		if r.UserID > 0 {
			if u, err := s.UserByID(ctx, r.UserID); err == nil {
				e.UserDisplayName = u.DisplayName
				e.UserDiscordID = u.DiscordID
			}
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].AccountUsername) < strings.ToLower(out[j].AccountUsername)
	})
	return out, nil
}

func (s *Store) DeleteEQAccount(ctx context.Context, accountID int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM eq_accounts WHERE id=$1`, accountID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("account not found")
	}
	return nil
}

func (s *Store) UpsertDiscordRoles(ctx context.Context, roles []DiscordRole) error {
	for _, r := range roles {
		id := strings.TrimSpace(r.ID)
		if id == "" {
			continue
		}
		name := strings.TrimSpace(r.Name)
		_, err := s.DB.ExecContext(ctx, `
			INSERT INTO discord_roles (role_id, name, updated_at) VALUES ($1,$2,now())
			ON CONFLICT (role_id) DO UPDATE SET
			  name = CASE WHEN EXCLUDED.name = '' THEN discord_roles.name ELSE EXCLUDED.name END,
			  updated_at = now()
		`, id, name)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ListDiscordRoles(ctx context.Context) ([]DiscordRole, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT role_id, name FROM discord_roles ORDER BY lower(name), role_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DiscordRole
	seen := map[string]bool{}
	for rows.Next() {
		var r DiscordRole
		if err := rows.Scan(&r.ID, &r.Name); err != nil {
			return nil, err
		}
		seen[r.ID] = true
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Also surface role IDs referenced in users / groups / elevated accounts but missing names.
	extra, err := s.DB.QueryContext(ctx, `
		SELECT DISTINCT role_id FROM (
		  SELECT jsonb_array_elements_text(role_ids_json::jsonb) AS role_id FROM users
		  UNION
		  SELECT discord_role_id FROM group_members WHERE discord_role_id IS NOT NULL AND discord_role_id <> ''
		  UNION
		  SELECT required_discord_role_id FROM eq_accounts WHERE required_discord_role_id <> ''
		) x
		WHERE role_id IS NOT NULL AND role_id <> ''
		ORDER BY role_id
	`)
	if err == nil {
		defer extra.Close()
		for extra.Next() {
			var id string
			if err := extra.Scan(&id); err != nil {
				continue
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, DiscordRole{ID: id, Name: ""})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		ni, nj := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if ni == "" && nj != "" {
			return false
		}
		if nj == "" && ni != "" {
			return true
		}
		if ni != nj {
			return ni < nj
		}
		return out[i].ID < out[j].ID
	})
	if out == nil {
		out = []DiscordRole{}
	}
	return out, nil
}

// ListSSOUsers returns users who have (or had) an SSO token.
func (s *Store) ListSSOUsers(ctx context.Context) ([]AdminUser, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT u.id, u.discord_id, u.display_name, u.role_ids_json, u.access_revoked,
		       EXISTS (
		         SELECT 1 FROM api_tokens t
		         WHERE t.user_id = u.id AND t.revoked_at IS NULL
		       ) AS has_active_token
		FROM users u
		WHERE EXISTS (SELECT 1 FROM api_tokens t WHERE t.user_id = u.id)
		ORDER BY lower(u.display_name), u.discord_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminUser
	for rows.Next() {
		var a AdminUser
		var raw string
		if err := rows.Scan(&a.ID, &a.DiscordID, &a.DisplayName, &raw, &a.AccessRevoked, &a.HasActiveToken); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(raw), &a.RoleIDs)
		if a.RoleIDs == nil {
			a.RoleIDs = []string{}
		}
		out = append(out, a)
	}
	if out == nil {
		out = []AdminUser{}
	}
	return out, rows.Err()
}

func (s *Store) AdminState(ctx context.Context) (AdminState, error) {
	users, err := s.ListSSOUsers(ctx)
	if err != nil {
		return AdminState{}, err
	}
	roles, err := s.ListDiscordRoles(ctx)
	if err != nil {
		return AdminState{}, err
	}
	return AdminState{Users: users, Roles: roles}, nil
}

// ListDirectoryUsers returns non-revoked SSO users for share pickers.
func (s *Store) ListDirectoryUsers(ctx context.Context) ([]DirectoryUser, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, discord_id, display_name
		FROM users
		WHERE access_revoked = false
		  AND EXISTS (SELECT 1 FROM api_tokens t WHERE t.user_id = users.id)
		ORDER BY lower(display_name), discord_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DirectoryUser
	for rows.Next() {
		var d DirectoryUser
		if err := rows.Scan(&d.ID, &d.DiscordID, &d.DisplayName); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if out == nil {
		out = []DirectoryUser{}
	}
	return out, rows.Err()
}

func (s *Store) replaceAccountShares(ctx context.Context, accountID int64, userIDs []int64) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM account_shares WHERE eq_account_id=$1`, accountID); err != nil {
		return err
	}
	seen := map[int64]bool{}
	for _, uid := range userIDs {
		if uid <= 0 || seen[uid] {
			continue
		}
		seen[uid] = true
		var revoked bool
		err := s.DB.QueryRowContext(ctx, `SELECT access_revoked FROM users WHERE id=$1`, uid).Scan(&revoked)
		if err == sql.ErrNoRows {
			return fmt.Errorf("user not found: %d", uid)
		}
		if err != nil {
			return err
		}
		if revoked {
			return fmt.Errorf("user access revoked: %d", uid)
		}
		if _, err := s.DB.ExecContext(ctx, `
			INSERT INTO account_shares (eq_account_id, user_id) VALUES ($1,$2)
		`, accountID, uid); err != nil {
			return err
		}
	}
	return nil
}

// SetRestrictedAccountShares replaces who can use a restricted (private share) account.
// The owner is never listed as a share recipient; they always retain access.
func (s *Store) SetRestrictedAccountShares(ctx context.Context, accountID int64, shareUserIDs []int64) error {
	var restricted bool
	var ownerID sql.NullInt64
	if err := s.DB.QueryRowContext(ctx, `
		SELECT COALESCE(restricted, false), owner_user_id FROM eq_accounts WHERE id=$1
	`, accountID).Scan(&restricted, &ownerID); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("account not found")
		}
		return err
	}
	if !restricted {
		return fmt.Errorf("account is not a private share")
	}
	clean := make([]int64, 0, len(shareUserIDs))
	seen := map[int64]bool{}
	for _, uid := range shareUserIDs {
		if uid <= 0 || seen[uid] {
			continue
		}
		if ownerID.Valid && uid == ownerID.Int64 {
			continue
		}
		seen[uid] = true
		clean = append(clean, uid)
	}
	return s.replaceAccountShares(ctx, accountID, clean)
}

// DiffNewShareRecipients returns user IDs in incoming that were not in previous.
func DiffNewShareRecipients(previous, incoming []int64) []int64 {
	prev := make(map[int64]bool, len(previous))
	for _, id := range previous {
		prev[id] = true
	}
	added := make([]int64, 0, len(incoming))
	for _, id := range incoming {
		if !prev[id] {
			added = append(added, id)
		}
	}
	return added
}

// ShareLocalAccount publishes or updates a restricted EQ account owned by the caller and sets shares.
// Password is required when creating; optional (empty) when updating an owned account.
// The returned slice lists share recipients newly granted direct user access (not present before this call).
func (s *Store) ShareLocalAccount(ctx context.Context, owner User, username, password string, aliases []string, shareUserIDs []int64, shareRoleIDs []string, shareGroupIDs []int64) (int64, []int64, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return 0, nil, fmt.Errorf("username required")
	}
	cleanShares := make([]int64, 0, len(shareUserIDs))
	seenUser := map[int64]bool{}
	for _, uid := range shareUserIDs {
		if uid <= 0 || uid == owner.ID || seenUser[uid] {
			continue
		}
		seenUser[uid] = true
		cleanShares = append(cleanShares, uid)
	}
	cleanRoles := make([]string, 0, len(shareRoleIDs))
	seenRole := map[string]bool{}
	for _, rid := range shareRoleIDs {
		rid = strings.TrimSpace(rid)
		if rid == "" || seenRole[rid] {
			continue
		}
		seenRole[rid] = true
		cleanRoles = append(cleanRoles, rid)
	}
	cleanGroups := make([]int64, 0, len(shareGroupIDs))
	seenGroup := map[int64]bool{}
	for _, gid := range shareGroupIDs {
		if gid <= 0 || seenGroup[gid] {
			continue
		}
		seenGroup[gid] = true
		cleanGroups = append(cleanGroups, gid)
	}

	id, ok, err := s.FindEQAccountIDByUsername(ctx, username)
	if err != nil {
		return 0, nil, err
	}
	var previousShares []int64
	if ok {
		previousShares = s.listShareUserIDs(ctx, id)
		var restricted bool
		var ownerID sql.NullInt64
		if err := s.DB.QueryRowContext(ctx, `
			SELECT COALESCE(restricted, false), owner_user_id FROM eq_accounts WHERE id=$1
		`, id).Scan(&restricted, &ownerID); err != nil {
			return 0, nil, err
		}
		if !restricted || !ownerID.Valid || ownerID.Int64 != owner.ID {
			return 0, nil, fmt.Errorf("account already exists on SSO")
		}
		if strings.TrimSpace(password) != "" {
			if err := s.SetEQPassword(ctx, id, password); err != nil {
				return 0, nil, err
			}
		}
		if _, err := s.DB.ExecContext(ctx, `
			UPDATE eq_accounts SET restricted=true, owner_user_id=$2, required_discord_role_id='', required_user_id=NULL, updated_at=now()
			WHERE id=$1
		`, id, owner.ID); err != nil {
			return 0, nil, err
		}
		_ = s.ReplaceAccountUsers(ctx, id, nil)
	} else {
		if strings.TrimSpace(password) == "" {
			return 0, nil, fmt.Errorf("password required")
		}
		id, err = s.AddEQAccount(ctx, username, password, "")
		if err != nil {
			return 0, nil, err
		}
		if _, err := s.DB.ExecContext(ctx, `
			UPDATE eq_accounts SET restricted=true, owner_user_id=$2, required_discord_role_id='', required_user_id=NULL, updated_at=now()
			WHERE id=$1
		`, id, owner.ID); err != nil {
			return 0, nil, err
		}
	}

	for _, al := range aliases {
		al = strings.TrimSpace(al)
		if al == "" || strings.EqualFold(al, username) {
			continue
		}
		_ = s.AddAlias(ctx, al, id)
	}
	if err := s.replaceAccountShares(ctx, id, cleanShares); err != nil {
		return 0, nil, err
	}
	if err := s.ReplaceAccountRoles(ctx, id, cleanRoles); err != nil {
		return 0, nil, err
	}
	if err := s.ReplaceAccountGroups(ctx, id, cleanGroups); err != nil {
		return 0, nil, err
	}
	return id, DiffNewShareRecipients(previousShares, cleanShares), nil
}

// UnshareLocalAccount removes a restricted account owned by the caller from SSO.
func (s *Store) UnshareLocalAccount(ctx context.Context, owner User, username string) error {
	id, ok, err := s.FindEQAccountIDByUsername(ctx, username)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("not shared")
	}
	var restricted bool
	var ownerID sql.NullInt64
	if err := s.DB.QueryRowContext(ctx, `
		SELECT COALESCE(restricted, false), owner_user_id FROM eq_accounts WHERE id=$1
	`, id).Scan(&restricted, &ownerID); err != nil {
		return err
	}
	if !restricted || !ownerID.Valid || ownerID.Int64 != owner.ID {
		return fmt.Errorf("not your shared account")
	}
	return s.DeleteEQAccount(ctx, id)
}
