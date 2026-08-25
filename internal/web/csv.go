package web

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/alfred-identity/web/internal/store"
)

// SSOCSVRow is one imported EQ account line.
type SSOCSVRow struct {
	Username   string
	Password   string
	Role       string // Discord role id or name (optional)
	Aliases    []string
	Tags       []string
	Characters []string
	Line       int
}

type SSOCSVImportResult struct {
	Added   int      `json:"added"`
	Updated int      `json:"updated"`
	Errors  []string `json:"errors"`
}

func splitMulti(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	sep := ','
	if strings.Contains(s, "|") && !strings.Contains(s, ",") {
		sep = '|'
	}
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == sep || r == ';'
	})
	seen := map[string]bool{}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		key := strings.ToLower(p)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, p)
	}
	return out
}

func parseSSOAccountsCSV(r io.Reader) ([]SSOCSVRow, error) {
	cr := csv.NewReader(r)
	cr.TrimLeadingSpace = true
	cr.FieldsPerRecord = -1
	rows, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("csv: %w", err)
	}
	var out []SSOCSVRow
	for i, row := range rows {
		line := i + 1
		if len(row) == 0 {
			continue
		}
		// Skip header
		if i == 0 {
			h0 := strings.ToLower(strings.TrimSpace(row[0]))
			if h0 == "username" || h0 == "account" || h0 == "name" {
				continue
			}
		}
		for len(row) < 6 {
			row = append(row, "")
		}
		username := strings.TrimSpace(row[0])
		if username == "" {
			continue
		}
		out = append(out, SSOCSVRow{
			Username:   username,
			Password:   row[1], // keep as-is (may have leading/trailing spaces intentionally? trim)
			Role:       strings.TrimSpace(row[2]),
			Aliases:    splitMulti(row[3]),
			Tags:       splitMulti(row[4]),
			Characters: splitMulti(row[5]),
			Line:       line,
		})
		out[len(out)-1].Password = strings.TrimSpace(out[len(out)-1].Password)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no account rows found")
	}
	return out, nil
}

func resolveRoleID(role string, roles []store.DiscordRole) (string, error) {
	role = strings.TrimSpace(role)
	if role == "" {
		return "", nil
	}
	for _, r := range roles {
		if r.ID == role {
			return r.ID, nil
		}
	}
	for _, r := range roles {
		if strings.EqualFold(r.Name, role) {
			return r.ID, nil
		}
	}
	// Allow raw snowflake even if not yet in discord_roles cache.
	if isDiscordSnowflake(role) {
		return role, nil
	}
	return "", fmt.Errorf("unknown role %q (use Discord role id or cached name)", role)
}

func isDiscordSnowflake(s string) bool {
	if len(s) < 17 || len(s) > 20 {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func (s *Server) importSSOAccountsCSV(ctx context.Context, actor store.User, r io.Reader) (SSOCSVImportResult, error) {
	rows, err := parseSSOAccountsCSV(r)
	if err != nil {
		return SSOCSVImportResult{}, err
	}
	roles, err := s.store.ListDiscordRoles(ctx)
	if err != nil {
		return SSOCSVImportResult{}, err
	}
	res := SSOCSVImportResult{Errors: []string{}}
	for _, row := range rows {
		roleID, err := resolveRoleID(row.Role, roles)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("line %d (%s): %v", row.Line, row.Username, err))
			continue
		}
		id, found, err := s.store.FindEQAccountIDByUsername(ctx, row.Username)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("line %d (%s): %v", row.Line, row.Username, err))
			continue
		}
		if !found {
			if row.Password == "" {
				res.Errors = append(res.Errors, fmt.Sprintf("line %d (%s): password required for new account", row.Line, row.Username))
				continue
			}
			id, err = s.store.AddEQAccount(ctx, row.Username, row.Password, "")
			if err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("line %d (%s): create failed: %v", row.Line, row.Username, err))
				continue
			}
			res.Added++
		} else {
			if row.Password != "" {
				if err := s.store.SetEQPassword(ctx, id, row.Password); err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("line %d (%s): password update failed: %v", row.Line, row.Username, err))
					continue
				}
			}
			res.Updated++
		}
		// Always apply role when column provided (including empty → clear).
		// If role column was blank in CSV, clear elevated requirement.
		if err := s.store.SetEQRequiredRole(ctx, id, roleID); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("line %d (%s): role failed: %v", row.Line, row.Username, err))
		}
		for _, al := range row.Aliases {
			if strings.EqualFold(al, row.Username) {
				continue
			}
			if err := s.store.AddAlias(ctx, al, id); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("line %d (%s): alias %q: %v", row.Line, row.Username, al, err))
			}
		}
		for _, tag := range row.Tags {
			if err := s.store.AddTag(ctx, tag, id); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("line %d (%s): tag %q: %v", row.Line, row.Username, tag, err))
			}
		}
		for _, ch := range row.Characters {
			if err := s.store.AddCharacter(ctx, ch, id); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("line %d (%s): character %q: %v", row.Line, row.Username, ch, err))
			}
		}
	}
	s.store.Audit(ctx, actor.ID, "web_import_accounts",
		fmt.Sprintf("added=%d updated=%d errors=%d", res.Added, res.Updated, len(res.Errors)))
	return res, nil
}

func joinMulti(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ",")
}

func roleLabel(roleID string, roles []store.DiscordRole) string {
	roleID = strings.TrimSpace(roleID)
	if roleID == "" {
		return ""
	}
	for _, r := range roles {
		if r.ID == roleID {
			if n := strings.TrimSpace(r.Name); n != "" {
				return n
			}
			return roleID
		}
	}
	return roleID
}

// exportSSOAccountsCSV writes username,password,role,aliases,tags,characters rows
// matching the import format. Skips restricted (private share) accounts.
// When includePasswords is false, the password column is left blank.
func (s *Server) exportSSOAccountsCSV(ctx context.Context, w io.Writer, includePasswords bool) (int, error) {
	metas, err := s.store.ListEQAccountMetas(ctx, nil, true)
	if err != nil {
		return 0, err
	}
	roles, err := s.store.ListDiscordRoles(ctx)
	if err != nil {
		return 0, err
	}
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"username", "password", "role", "aliases", "tags", "characters"}); err != nil {
		return 0, err
	}
	n := 0
	for _, m := range metas {
		if m.Restricted {
			continue
		}
		username := m.Username
		password := ""
		if includePasswords {
			u, p, err := s.store.DecryptCredentialsAny(ctx, m.ID)
			if err != nil {
				// Skip rows sealed under another key (or corrupt); do not abort the whole export.
				continue
			}
			username, password = u, p
		}
		role := ""
		if len(m.RequiredRoleIDs) > 0 {
			role = roleLabel(m.RequiredRoleIDs[0], roles)
		} else if m.RequiredRoleID != "" {
			role = roleLabel(m.RequiredRoleID, roles)
		}
		aliases := make([]string, 0, len(m.Aliases))
		for _, a := range m.Aliases {
			a = strings.TrimSpace(a)
			if a == "" || strings.EqualFold(a, username) {
				continue
			}
			aliases = append(aliases, a)
		}
		if err := cw.Write([]string{
			username,
			password,
			role,
			joinMulti(aliases),
			joinMulti(m.Tags),
			joinMulti(m.Characters),
		}); err != nil {
			return n, err
		}
		n++
	}
	cw.Flush()
	return n, cw.Error()
}