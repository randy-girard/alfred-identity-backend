package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/alfred-identity/web/internal/store"
)

const configBackupVersion = 1

// ConfigBackup is a host-portable snapshot of web-managed SSO configuration.
// Internal DB IDs are not used; Discord IDs and EQ usernames are the keys.
type ConfigBackup struct {
	Version    int                    `json:"version"`
	ExportedAt time.Time              `json:"exported_at"`
	Users      []ConfigBackupUser     `json:"users"`
	Groups     []ConfigBackupGroup    `json:"groups"`
	Accounts   []ConfigBackupAccount  `json:"accounts"`
	Roles      []store.DiscordRole    `json:"discord_roles,omitempty"`
}

type ConfigBackupUser struct {
	DiscordID     string   `json:"discord_id"`
	DisplayName   string   `json:"display_name"`
	RoleIDs       []string `json:"role_ids"`
	AccessRevoked bool     `json:"access_revoked"`
}

type ConfigBackupGroup struct {
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	WebRole          string   `json:"web_role"`
	DiscordCommands  []string `json:"discord_commands,omitempty"`
	MemberDiscordIDs []string `json:"member_discord_ids"`
	MemberRoleIDs    []string `json:"member_role_ids"`
	AccountUsernames []string `json:"account_usernames"`
}

type ConfigBackupAccount struct {
	Username              string   `json:"username"`
	Password              string   `json:"password"`
	Disabled              bool     `json:"disabled"`
	Restricted            bool     `json:"restricted"`
	OwnerDiscordID        string   `json:"owner_discord_id,omitempty"`
	SharedDiscordIDs      []string `json:"shared_discord_ids,omitempty"`
	RequiredRoleIDs       []string `json:"required_role_ids,omitempty"`
	RequiredUserDiscordIDs []string `json:"required_user_discord_ids,omitempty"`
	GroupNames            []string `json:"group_names,omitempty"`
	Aliases               []string `json:"aliases,omitempty"`
	Tags                  []string `json:"tags,omitempty"`
	Characters            []string `json:"characters,omitempty"`
}

type ConfigImportResult struct {
	UsersAdded      int      `json:"users_added"`
	UsersUpdated    int      `json:"users_updated"`
	GroupsAdded     int      `json:"groups_added"`
	GroupsUpdated   int      `json:"groups_updated"`
	AccountsAdded   int      `json:"accounts_added"`
	AccountsUpdated int      `json:"accounts_updated"`
	Errors          []string `json:"errors"`
}

func (s *Server) exportConfigBackup(ctx context.Context) (ConfigBackup, error) {
	out := ConfigBackup{
		Version:    configBackupVersion,
		ExportedAt: time.Now().UTC(),
		Users:      []ConfigBackupUser{},
		Groups:     []ConfigBackupGroup{},
		Accounts:   []ConfigBackupAccount{},
		Roles:      []store.DiscordRole{},
	}

	users, err := s.store.ListUsersForRoleSync(ctx)
	if err != nil {
		return out, err
	}
	userByID := map[int64]store.User{}
	for _, u := range users {
		userByID[u.ID] = u
		roles := append([]string(nil), u.RoleIDs...)
		if roles == nil {
			roles = []string{}
		}
		out.Users = append(out.Users, ConfigBackupUser{
			DiscordID:     u.DiscordID,
			DisplayName:   u.DisplayName,
			RoleIDs:       roles,
			AccessRevoked: u.AccessRevoked,
		})
	}

	if roles, err := s.store.ListDiscordRoles(ctx); err == nil {
		out.Roles = roles
	}

	metas, err := s.store.ListEQAccountMetas(ctx, nil, true)
	if err != nil {
		return out, err
	}
	acctNameByID := map[int64]string{}
	for _, m := range metas {
		acctNameByID[m.ID] = m.Username
		username, password, err := s.store.DecryptCredentialsAny(ctx, m.ID)
		if err != nil {
			return out, fmt.Errorf("account %d: %w", m.ID, err)
		}
		row := ConfigBackupAccount{
			Username:               username,
			Password:               password,
			Disabled:               m.Disabled,
			Restricted:             m.Restricted,
			RequiredRoleIDs:        append([]string(nil), m.RequiredRoleIDs...),
			RequiredUserDiscordIDs: []string{},
			GroupNames:             []string{},
			Aliases:                []string{},
			Tags:                   append([]string(nil), m.Tags...),
			Characters:             append([]string(nil), m.Characters...),
			SharedDiscordIDs:       []string{},
		}
		if row.RequiredRoleIDs == nil {
			row.RequiredRoleIDs = []string{}
		}
		if row.Tags == nil {
			row.Tags = []string{}
		}
		if row.Characters == nil {
			row.Characters = []string{}
		}
		for _, al := range m.Aliases {
			al = strings.TrimSpace(al)
			if al == "" || strings.EqualFold(al, username) {
				continue
			}
			row.Aliases = append(row.Aliases, al)
		}
		for _, uid := range m.RequiredUserIDs {
			if u, ok := userByID[uid]; ok && u.DiscordID != "" {
				row.RequiredUserDiscordIDs = append(row.RequiredUserDiscordIDs, u.DiscordID)
			}
		}
		for _, uid := range m.SharedUserIDs {
			if u, ok := userByID[uid]; ok && u.DiscordID != "" {
				row.SharedDiscordIDs = append(row.SharedDiscordIDs, u.DiscordID)
			}
		}
		if m.OwnerUserID > 0 {
			if u, ok := userByID[m.OwnerUserID]; ok {
				row.OwnerDiscordID = u.DiscordID
			}
		}
		out.Accounts = append(out.Accounts, row)
	}

	groups, err := s.store.ListGroupDetails(ctx)
	if err != nil {
		return out, err
	}
	// Fill group names on accounts after we have group details.
	groupNameByID := map[int64]string{}
	for _, g := range groups {
		groupNameByID[g.ID] = g.Name
	}
	for i := range out.Accounts {
		// match by username to metas
		for _, m := range metas {
			if !strings.EqualFold(m.Username, out.Accounts[i].Username) {
				continue
			}
			for _, gid := range m.GroupIDs {
				if n, ok := groupNameByID[gid]; ok {
					out.Accounts[i].GroupNames = append(out.Accounts[i].GroupNames, n)
				}
			}
			break
		}
	}

	for _, g := range groups {
		bg := ConfigBackupGroup{
			Name:             g.Name,
			Description:      g.Description,
			WebRole:          g.WebRole,
			DiscordCommands:  append([]string(nil), g.DiscordCommands...),
			MemberDiscordIDs: []string{},
			MemberRoleIDs:    append([]string(nil), g.RoleIDs...),
			AccountUsernames: []string{},
		}
		if bg.MemberRoleIDs == nil {
			bg.MemberRoleIDs = []string{}
		}
		for _, u := range g.Users {
			if u.DiscordID != "" {
				bg.MemberDiscordIDs = append(bg.MemberDiscordIDs, u.DiscordID)
			}
		}
		for _, aid := range g.AccountIDs {
			if n, ok := acctNameByID[aid]; ok && n != "" {
				bg.AccountUsernames = append(bg.AccountUsernames, n)
			}
		}
		out.Groups = append(out.Groups, bg)
	}

	return out, nil
}

// decodeConfigBackup parses and version-gates a config backup JSON body.
func decodeConfigBackup(r io.Reader) (ConfigBackup, error) {
	var bak ConfigBackup
	if err := json.NewDecoder(r).Decode(&bak); err != nil {
		return ConfigBackup{}, fmt.Errorf("invalid backup JSON: %w", err)
	}
	if bak.Version <= 0 {
		bak.Version = 1
	}
	if bak.Version > configBackupVersion {
		return ConfigBackup{}, fmt.Errorf("unsupported backup version %d (max %d)", bak.Version, configBackupVersion)
	}
	return bak, nil
}

func (s *Server) importConfigBackup(ctx context.Context, r io.Reader) (ConfigImportResult, error) {
	res := ConfigImportResult{Errors: []string{}}
	bak, err := decodeConfigBackup(r)
	if err != nil {
		return res, err
	}

	if len(bak.Roles) > 0 {
		_ = s.store.UpsertDiscordRoles(ctx, bak.Roles)
	}

	discordToUser := map[string]store.User{}
	for i, row := range bak.Users {
		did := strings.TrimSpace(row.DiscordID)
		if did == "" {
			res.Errors = append(res.Errors, fmt.Sprintf("users[%d]: discord_id required", i))
			continue
		}
		roles := row.RoleIDs
		if roles == nil {
			roles = []string{}
		}
		existing, err := s.store.UserByDiscordID(ctx, did)
		isNew := err != nil
		u, err := s.store.UpsertUser(ctx, did, row.DisplayName, roles)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("user %s: %v", did, err))
			continue
		}
		_ = s.store.SetUserRoles(ctx, u.ID, roles)
		u.RoleIDs = roles
		if isNew {
			res.UsersAdded++
		} else {
			res.UsersUpdated++
			_ = existing
		}
		if err := s.store.SetUserAccessRevoked(ctx, u.ID, row.AccessRevoked); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("user %s revoke: %v", did, err))
		}
		u.AccessRevoked = row.AccessRevoked
		discordToUser[did] = u
	}
	// Ensure map includes any users that already existed but weren't in the backup file.
	if all, err := s.store.ListUsersForRoleSync(ctx); err == nil {
		for _, u := range all {
			if _, ok := discordToUser[u.DiscordID]; !ok {
				discordToUser[u.DiscordID] = u
			}
		}
	}

	// Accounts before groups so group account links resolve.
	for i, row := range bak.Accounts {
		username := strings.TrimSpace(row.Username)
		if username == "" {
			res.Errors = append(res.Errors, fmt.Sprintf("accounts[%d]: username required", i))
			continue
		}
		id, found, err := s.store.FindEQAccountIDByUsername(ctx, username)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("account %s: %v", username, err))
			continue
		}

		if row.Restricted {
			ownerDID := strings.TrimSpace(row.OwnerDiscordID)
			owner, ok := discordToUser[ownerDID]
			if !ok || ownerDID == "" {
				res.Errors = append(res.Errors, fmt.Sprintf("account %s: owner_discord_id required for restricted share", username))
				continue
			}
			shareIDs := make([]int64, 0, len(row.SharedDiscordIDs))
			for _, sd := range row.SharedDiscordIDs {
				if u, ok := discordToUser[strings.TrimSpace(sd)]; ok {
					shareIDs = append(shareIDs, u.ID)
				}
			}
			pw := row.Password
			if found && pw == "" {
				// keep existing password
			} else if !found && pw == "" {
				res.Errors = append(res.Errors, fmt.Sprintf("account %s: password required for new restricted account", username))
				continue
			}
			newID, err := s.store.ShareLocalAccount(ctx, owner, username, pw, row.Aliases, shareIDs)
			if err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("account %s share: %v", username, err))
				continue
			}
			id = newID
			if found {
				res.AccountsUpdated++
			} else {
				res.AccountsAdded++
			}
		} else {
			if !found {
				if strings.TrimSpace(row.Password) == "" {
					res.Errors = append(res.Errors, fmt.Sprintf("account %s: password required for new account", username))
					continue
				}
				id, err = s.store.AddEQAccount(ctx, username, row.Password, "")
				if err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("account %s create: %v", username, err))
					continue
				}
				res.AccountsAdded++
			} else {
				if strings.TrimSpace(row.Password) != "" {
					if err := s.store.SetEQPassword(ctx, id, row.Password); err != nil {
						res.Errors = append(res.Errors, fmt.Sprintf("account %s password: %v", username, err))
						continue
					}
				}
				res.AccountsUpdated++
			}
			_ = s.store.SetEQDisabled(ctx, id, row.Disabled)

			userIDs := make([]int64, 0, len(row.RequiredUserDiscordIDs))
			for _, d := range row.RequiredUserDiscordIDs {
				if u, ok := discordToUser[strings.TrimSpace(d)]; ok {
					userIDs = append(userIDs, u.ID)
				}
			}
			// Group links applied after groups exist; clear for now then set with roles/users.
			if err := s.store.SetAccountAccess(ctx, id, row.RequiredRoleIDs, userIDs, nil); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("account %s access: %v", username, err))
			}
		}

		if err := s.replaceAccountAliases(ctx, id, username, row.Aliases); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("account %s aliases: %v", username, err))
		}
		if err := s.replaceAccountTags(ctx, id, row.Tags); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("account %s tags: %v", username, err))
		}
		if err := s.replaceAccountCharacters(ctx, id, row.Characters); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("account %s characters: %v", username, err))
		}
		_ = id
	}

	// Groups
	groupIDByName := map[string]int64{}
	for i, row := range bak.Groups {
		name := strings.TrimSpace(row.Name)
		if name == "" {
			res.Errors = append(res.Errors, fmt.Sprintf("groups[%d]: name required", i))
			continue
		}
		gid, found, err := s.store.FindGroupIDByName(ctx, name)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("group %s: %v", name, err))
			continue
		}
		webRole := row.WebRole
		if !found {
			gid, err = s.store.CreateGroup(ctx, name, row.Description, webRole, row.DiscordCommands)
			if err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("group %s create: %v", name, err))
				continue
			}
			res.GroupsAdded++
		} else {
			if err := s.store.UpdateGroupMeta(ctx, gid, name, row.Description, webRole, row.DiscordCommands); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("group %s update: %v", name, err))
				continue
			}
			res.GroupsUpdated++
		}
		groupIDByName[strings.ToLower(name)] = gid

		memberUserIDs := make([]int64, 0, len(row.MemberDiscordIDs))
		for _, d := range row.MemberDiscordIDs {
			if u, ok := discordToUser[strings.TrimSpace(d)]; ok {
				memberUserIDs = append(memberUserIDs, u.ID)
			}
		}
		if err := s.store.ReplaceGroupMembership(ctx, gid, memberUserIDs, row.MemberRoleIDs); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("group %s members: %v", name, err))
		}
		acctIDs := make([]int64, 0, len(row.AccountUsernames))
		for _, uname := range row.AccountUsernames {
			id, ok, err := s.store.FindEQAccountIDByUsername(ctx, uname)
			if err != nil || !ok {
				continue
			}
			acctIDs = append(acctIDs, id)
		}
		if err := s.store.ReplaceGroupAccountLinks(ctx, gid, acctIDs); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("group %s accounts: %v", name, err))
		}
	}

	// Apply account → group grants from account.group_names (non-restricted).
	for _, row := range bak.Accounts {
		if row.Restricted || len(row.GroupNames) == 0 {
			continue
		}
		id, ok, err := s.store.FindEQAccountIDByUsername(ctx, row.Username)
		if err != nil || !ok {
			continue
		}
		gids := make([]int64, 0, len(row.GroupNames))
		for _, gn := range row.GroupNames {
			if gid, ok := groupIDByName[strings.ToLower(strings.TrimSpace(gn))]; ok {
				gids = append(gids, gid)
			}
		}
		userIDs := make([]int64, 0, len(row.RequiredUserDiscordIDs))
		for _, d := range row.RequiredUserDiscordIDs {
			if u, ok := discordToUser[strings.TrimSpace(d)]; ok {
				userIDs = append(userIDs, u.ID)
			}
		}
		_ = s.store.SetAccountAccess(ctx, id, row.RequiredRoleIDs, userIDs, gids)
	}

	return res, nil
}

func (s *Server) replaceAccountAliases(ctx context.Context, accountID int64, username string, aliases []string) error {
	meta, err := s.store.GetEQAccountMeta(ctx, accountID)
	if err == nil {
		for _, al := range meta.Aliases {
			if strings.EqualFold(al, username) {
				continue
			}
			_ = s.store.RemoveAlias(ctx, al, accountID)
		}
	}
	for _, al := range aliases {
		al = strings.TrimSpace(al)
		if al == "" || strings.EqualFold(al, username) {
			continue
		}
		if err := s.store.AddAlias(ctx, al, accountID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) replaceAccountTags(ctx context.Context, accountID int64, tags []string) error {
	meta, err := s.store.GetEQAccountMeta(ctx, accountID)
	if err == nil {
		for _, t := range meta.Tags {
			_ = s.store.RemoveTag(ctx, t, accountID)
		}
	}
	for _, t := range tags {
		if err := s.store.AddTag(ctx, t, accountID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) replaceAccountCharacters(ctx context.Context, accountID int64, chars []string) error {
	meta, err := s.store.GetEQAccountMeta(ctx, accountID)
	if err == nil {
		for _, c := range meta.Characters {
			_ = s.store.RemoveCharacter(ctx, c, accountID)
		}
	}
	for _, c := range chars {
		if err := s.store.AddCharacter(ctx, c, accountID); err != nil {
			return err
		}
	}
	return nil
}
