package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alfred-identity/web/internal/store"
)

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	u := currentUser(r)
	role := currentWebRole(r)
	if role == "" {
		role = s.webAccessLevel(r.Context(), u)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"user_id":      u.ID,
		"discord_id":   u.DiscordID,
		"display_name": u.DisplayName,
		"role_ids":     u.RoleIDs,
		"is_admin":     role == webRoleAdmin,
		"web_role":     role,
	})
}

func (s *Server) buildState(ctx context.Context) (map[string]any, error) {
	accounts, err := s.store.ListEQAccountMetas(ctx, nil, true)
	if err != nil {
		return nil, err
	}
	admin, err := s.store.AdminState(ctx)
	if err != nil {
		return nil, err
	}
	online := s.presence.Online()
	sessions := []map[string]any{}
	for _, e := range s.presence.Snapshot() {
		sessions = append(sessions, map[string]any{
			"account_id":     e.AccountID,
			"character_name": e.CharacterName,
			"user_id":        e.UserID,
			"last_seen":      e.LastSeen.UTC().Format(time.RFC3339),
		})
	}
	connections := []map[string]any{}
	for _, c := range s.hub.Connections() {
		connections = append(connections, map[string]any{
			"session_id":     c.SessionID,
			"user_id":        c.UserID,
			"discord_id":     c.DiscordID,
			"display_name":   c.DisplayName,
			"client_version": c.ClientVersion,
			"connected_at":   c.ConnectedAt.UTC().Format(time.RFC3339),
			"is_admin":       c.IsAdmin,
		})
	}
	groups, err := s.store.ListGroupDetails(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":          true,
		"accounts":    accounts,
		"online":      online,
		"sessions":    sessions,
		"connections": connections,
		"users":       admin.Users,
		"roles":       admin.Roles,
		"groups":      groups,
	}, nil
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	st, err := s.buildState(ctx)
	if err != nil {
		s.log.Error("web state", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleAccountsExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	u := currentUser(r)

	var buf strings.Builder
	n, err := s.exportSSOAccountsCSV(ctx, &buf)
	if err != nil {
		s.log.Error("web export accounts csv", "err", err)
		writeErr(w, http.StatusInternalServerError, "export_failed")
		return
	}
	s.store.Audit(ctx, u.ID, "web_export_accounts", fmt.Sprintf("rows=%d", n))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="alfred-sso-accounts.csv"`)
	_, _ = io.WriteString(w, buf.String())
}

func (s *Server) handleAccountsImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	u := currentUser(r)

	var reader io.Reader
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_multipart")
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			writeErr(w, http.StatusBadRequest, "file_required")
			return
		}
		defer f.Close()
		reader = f
	} else {
		reader = r.Body
	}

	res, err := s.importSSOAccountsCSV(ctx, u, reader)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.hub.BroadcastFullState()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"added":   res.Added,
		"updated": res.Updated,
		"errors":  res.Errors,
	})
}

func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	u := currentUser(r)
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Username       string `json:"username"`
			Password       string `json:"password"`
			RequiredRoleID string `json:"required_role_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request")
			return
		}
		username := strings.TrimSpace(body.Username)
		if username == "" || body.Password == "" {
			writeErr(w, http.StatusBadRequest, "username_password_required")
			return
		}
		id, err := s.store.AddEQAccount(ctx, username, body.Password, "")
		if err != nil {
			s.log.Error("web add account", "err", err)
			writeErr(w, http.StatusInternalServerError, "internal")
			return
		}
		if role := strings.TrimSpace(body.RequiredRoleID); role != "" {
			_ = s.store.SetEQRequiredRole(ctx, id, role)
		}
		s.store.AuditAccount(ctx, u.ID, id, "web_add_account", "username="+username)
		s.hub.BroadcastFullState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "account_id": id})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (s *Server) handleAccountSub(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, BasePath+"/api/accounts/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeErr(w, http.StatusNotFound, "not_found")
		return
	}
	accountID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || accountID <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid_account")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	u := currentUser(r)

	if len(parts) == 1 {
		if r.Method == http.MethodPatch || r.Method == http.MethodDelete {
			if s.rejectIfReadonly(w, r) {
				return
			}
		}
		switch r.Method {
		case http.MethodPatch:
			var body struct {
				Password        *string   `json:"password"`
				Disabled        *bool     `json:"disabled"`
				RequiredRoleID  *string   `json:"required_role_id"`
				RequiredRoleIDs *[]string `json:"required_role_ids"`
				RequiredUserID  *int64    `json:"required_user_id"`
				RequiredUserIDs *[]int64  `json:"required_user_ids"`
				GroupIDs        *[]int64  `json:"group_ids"`
				Access          *string   `json:"access"` // optional: "all" clears grants
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeErr(w, http.StatusBadRequest, "bad_request")
				return
			}
			changed := false
			if body.Password != nil && *body.Password != "" {
				if err := s.store.SetEQPassword(ctx, accountID, *body.Password); err != nil {
					writeErr(w, http.StatusInternalServerError, "internal")
					return
				}
				changed = true
			}
			if body.Disabled != nil {
				if err := s.store.SetEQDisabled(ctx, accountID, *body.Disabled); err != nil {
					writeErr(w, http.StatusInternalServerError, "internal")
					return
				}
				changed = true
			}
			setAccess := body.Access != nil || body.RequiredRoleID != nil || body.RequiredRoleIDs != nil ||
				body.RequiredUserID != nil || body.RequiredUserIDs != nil || body.GroupIDs != nil
			if setAccess {
				var roleIDs []string
				var userIDs []int64
				var groupIDs []int64
				if body.Access != nil && strings.EqualFold(strings.TrimSpace(*body.Access), "all") {
					roleIDs, userIDs, groupIDs = nil, nil, nil
				} else {
					cur, err := s.store.LoadEQAccountMeta(ctx, accountID)
					if err == nil {
						roleIDs = append([]string{}, cur.RequiredRoleIDs...)
						userIDs = append([]int64{}, cur.RequiredUserIDs...)
						groupIDs = append([]int64{}, cur.GroupIDs...)
					}
					if body.RequiredRoleIDs != nil {
						roleIDs = *body.RequiredRoleIDs
					} else if body.RequiredRoleID != nil {
						if r := strings.TrimSpace(*body.RequiredRoleID); r != "" {
							roleIDs = []string{r}
						} else {
							roleIDs = nil
						}
					}
					if body.RequiredUserIDs != nil {
						userIDs = *body.RequiredUserIDs
					} else if body.RequiredUserID != nil {
						if *body.RequiredUserID > 0 {
							userIDs = []int64{*body.RequiredUserID}
						} else {
							userIDs = nil
						}
					}
					if body.GroupIDs != nil {
						groupIDs = *body.GroupIDs
					}
				}
				if err := s.store.SetAccountAccess(ctx, accountID, roleIDs, userIDs, groupIDs); err != nil {
					s.log.Error("web set account access", "err", err)
					writeErr(w, http.StatusInternalServerError, "internal")
					return
				}
				changed = true
			}
			if !changed {
				writeErr(w, http.StatusBadRequest, "nothing_to_update")
				return
			}
			s.store.AuditAccount(ctx, u.ID, accountID, "web_update_account", "account="+strconv.FormatInt(accountID, 10))
			s.hub.BroadcastFullState()
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "account_id": accountID})
		case http.MethodDelete:
			if err := s.store.DeleteEQAccount(ctx, accountID); err != nil {
				writeErr(w, http.StatusInternalServerError, "internal")
				return
			}
			s.presence.Clear(accountID)
			s.store.AuditAccount(ctx, u.ID, accountID, "web_remove_account", "id="+strconv.FormatInt(accountID, 10))
			s.hub.BroadcastFullState()
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		default:
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		}
		return
	}

	// aliases / tags — POST add, DELETE remove; characters POST
	if len(parts) >= 2 && (parts[1] == "aliases" || parts[1] == "tags" || parts[1] == "characters") {
		switch r.Method {
		case http.MethodPost, http.MethodDelete:
			if s.rejectIfReadonly(w, r) {
				return
			}
		default:
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
	} else if parts[1] == "shares" {
		writeErr(w, http.StatusForbidden, "shares_managed_in_gui")
		return
	} else if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	switch parts[1] {
	case "aliases":
		var body struct {
			Alias string `json:"alias"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request")
			return
		}
		alias := strings.TrimSpace(body.Alias)
		if alias == "" {
			writeErr(w, http.StatusBadRequest, "alias_required")
			return
		}
		if r.Method == http.MethodDelete {
			if err := s.store.RemoveAlias(ctx, alias, accountID); err != nil {
				if strings.Contains(err.Error(), "not found") {
					writeErr(w, http.StatusNotFound, "not_found")
					return
				}
				writeErr(w, http.StatusInternalServerError, "internal")
				return
			}
			s.store.AuditAccount(ctx, u.ID, accountID, "web_remove_alias", "alias="+alias)
		} else {
			if err := s.store.AddAlias(ctx, alias, accountID); err != nil {
				if strings.Contains(err.Error(), "already used") {
					writeErr(w, http.StatusConflict, "alias_taken")
					return
				}
				writeErr(w, http.StatusInternalServerError, "internal")
				return
			}
			s.store.AuditAccount(ctx, u.ID, accountID, "web_add_alias", "alias="+alias)
		}
		s.hub.BroadcastFullState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case "tags":
		var body struct {
			Tag string `json:"tag"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request")
			return
		}
		tag := strings.TrimSpace(body.Tag)
		if tag == "" {
			writeErr(w, http.StatusBadRequest, "tag_required")
			return
		}
		if r.Method == http.MethodDelete {
			if err := s.store.RemoveTag(ctx, tag, accountID); err != nil {
				if strings.Contains(err.Error(), "not found") {
					writeErr(w, http.StatusNotFound, "not_found")
					return
				}
				writeErr(w, http.StatusInternalServerError, "internal")
				return
			}
			s.store.AuditAccount(ctx, u.ID, accountID, "web_remove_tag", "tag="+tag)
		} else {
			if err := s.store.AddTag(ctx, tag, accountID); err != nil {
				writeErr(w, http.StatusInternalServerError, "internal")
				return
			}
			s.store.AuditAccount(ctx, u.ID, accountID, "web_add_tag", "tag="+tag)
		}
		s.hub.BroadcastFullState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case "characters":
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request")
			return
		}
		name := strings.TrimSpace(body.Name)
		if name == "" {
			writeErr(w, http.StatusBadRequest, "name_required")
			return
		}
		if r.Method == http.MethodDelete {
			if err := s.store.RemoveCharacter(ctx, name, accountID); err != nil {
				if strings.Contains(err.Error(), "not found") {
					writeErr(w, http.StatusNotFound, "not_found")
					return
				}
				writeErr(w, http.StatusInternalServerError, "internal")
				return
			}
			s.store.AuditAccount(ctx, u.ID, accountID, "web_remove_character", "name="+name)
		} else {
			if err := s.store.AddCharacter(ctx, name, accountID); err != nil {
				if strings.Contains(err.Error(), "already used") {
					writeErr(w, http.StatusConflict, "character_taken")
					return
				}
				writeErr(w, http.StatusInternalServerError, "internal")
				return
			}
			s.store.AuditAccount(ctx, u.ID, accountID, "web_add_character", "name="+name)
		}
		s.hub.BroadcastFullState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeErr(w, http.StatusNotFound, "not_found")
	}
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, BasePath+"/api/users/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 2 {
		writeErr(w, http.StatusNotFound, "not_found")
		return
	}
	userID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || userID <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid_user")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	actor := currentUser(r)
	if s.rejectIfReadonly(w, r) {
		return
	}

	switch parts[1] {
	case "access":
		if r.Method != http.MethodPatch {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		var body struct {
			Revoked bool `json:"revoked"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request")
			return
		}
		if body.Revoked && userID == actor.ID {
			writeErr(w, http.StatusBadRequest, "cannot_revoke_self")
			return
		}
		if err := s.store.SetUserAccessRevoked(ctx, userID, body.Revoked); err != nil {
			writeErr(w, http.StatusInternalServerError, "internal")
			return
		}
		if body.Revoked {
			s.hub.DisconnectUser(userID, "access revoked")
		}
		s.store.Audit(ctx, actor.ID, "web_set_user_access", "user="+strconv.FormatInt(userID, 10))
		s.hub.BroadcastFullState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case "roles":
		if r.Method != http.MethodPut {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		var body struct {
			RoleIDs []string `json:"role_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request")
			return
		}
		if err := s.store.SetUserRoles(ctx, userID, body.RoleIDs); err != nil {
			writeErr(w, http.StatusInternalServerError, "internal")
			return
		}
		s.store.Audit(ctx, actor.ID, "web_set_user_roles", "user="+strconv.FormatInt(userID, 10))
		s.hub.BroadcastFullState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeErr(w, http.StatusNotFound, "not_found")
	}
}

func (s *Server) handleGroups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if s.rejectIfReadonly(w, r) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	u := currentUser(r)
	var body struct {
		Name            string   `json:"name"`
		Description     string   `json:"description"`
		WebRole         string   `json:"web_role"`
		DiscordCommands []string `json:"discord_commands"`
		UserIDs         []int64  `json:"user_ids"`
		RoleIDs         []string `json:"role_ids"`
		AccountIDs      []int64  `json:"account_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request")
		return
	}
	id, err := s.store.CreateGroup(ctx, body.Name, body.Description, body.WebRole, body.DiscordCommands)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.UserIDs != nil || body.RoleIDs != nil {
		userIDs, roleIDs := body.UserIDs, body.RoleIDs
		if userIDs == nil {
			userIDs = []int64{}
		}
		if roleIDs == nil {
			roleIDs = []string{}
		}
		if err := s.store.ReplaceGroupMembership(ctx, id, userIDs, roleIDs); err != nil {
			s.log.Error("web create group membership", "err", err, "group_id", id)
			writeErr(w, http.StatusInternalServerError, "internal")
			return
		}
	}
	if body.AccountIDs != nil {
		if err := s.store.ReplaceGroupAccountLinks(ctx, id, body.AccountIDs); err != nil {
			s.log.Error("web create group accounts", "err", err, "group_id", id)
			writeErr(w, http.StatusInternalServerError, "internal")
			return
		}
	}
	s.store.Audit(ctx, u.ID, "web_create_group", "id="+strconv.FormatInt(id, 10))
	s.hub.BroadcastFullState()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func (s *Server) handleGroupSub(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, BasePath+"/api/groups/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeErr(w, http.StatusNotFound, "not_found")
		return
	}
	groupID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || groupID <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid_group")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	u := currentUser(r)

	if len(parts) == 1 {
		if r.Method == http.MethodDelete || r.Method == http.MethodPatch {
			if s.rejectIfReadonly(w, r) {
				return
			}
		}
		switch r.Method {
		case http.MethodDelete:
			if err := s.store.DeleteGroup(ctx, groupID); err != nil {
				if strings.Contains(err.Error(), "not found") {
					writeErr(w, http.StatusNotFound, "not_found")
					return
				}
				writeErr(w, http.StatusInternalServerError, "internal")
				return
			}
			s.store.Audit(ctx, u.ID, "web_delete_group", "id="+strconv.FormatInt(groupID, 10))
			s.hub.BroadcastFullState()
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		case http.MethodPatch:
			var body struct {
				Name            *string   `json:"name"`
				Description     *string   `json:"description"`
				WebRole         *string   `json:"web_role"`
				DiscordCommands *[]string `json:"discord_commands"`
				UserIDs         *[]int64  `json:"user_ids"`
				RoleIDs         *[]string `json:"role_ids"`
				AccountIDs      *[]int64  `json:"account_ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeErr(w, http.StatusBadRequest, "bad_request")
				return
			}
			details, err := s.store.ListGroupDetails(ctx)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, "internal")
				return
			}
			var cur *store.GroupDetail
			for i := range details {
				if details[i].ID == groupID {
					cur = &details[i]
					break
				}
			}
			if cur == nil {
				writeErr(w, http.StatusNotFound, "not_found")
				return
			}
			changed := false
			if body.Name != nil || body.Description != nil || body.WebRole != nil || body.DiscordCommands != nil {
				name, desc, webRole := cur.Name, cur.Description, cur.WebRole
				discordCommands := cur.DiscordCommands
				if body.Name != nil {
					name = *body.Name
				}
				if body.Description != nil {
					desc = *body.Description
				}
				if body.WebRole != nil {
					webRole = *body.WebRole
				}
				if body.DiscordCommands != nil {
					discordCommands = *body.DiscordCommands
				}
				if err := s.store.UpdateGroupMeta(ctx, groupID, name, desc, webRole, discordCommands); err != nil {
					writeErr(w, http.StatusBadRequest, err.Error())
					return
				}
				changed = true
			}
			if body.UserIDs != nil || body.RoleIDs != nil {
				userIDs, roleIDs := cur.UserIDs, cur.RoleIDs
				if body.UserIDs != nil {
					userIDs = *body.UserIDs
				}
				if body.RoleIDs != nil {
					roleIDs = *body.RoleIDs
				}
				if err := s.store.ReplaceGroupMembership(ctx, groupID, userIDs, roleIDs); err != nil {
					s.log.Error("web replace group membership", "err", err)
					writeErr(w, http.StatusInternalServerError, "internal")
					return
				}
				changed = true
			}
			if body.AccountIDs != nil {
				if err := s.store.ReplaceGroupAccountLinks(ctx, groupID, *body.AccountIDs); err != nil {
					s.log.Error("web replace group accounts", "err", err)
					writeErr(w, http.StatusInternalServerError, "internal")
					return
				}
				changed = true
			}
			if !changed {
				writeErr(w, http.StatusBadRequest, "nothing_to_update")
				return
			}
			s.store.Audit(ctx, u.ID, "web_update_group", "id="+strconv.FormatInt(groupID, 10))
			s.hub.BroadcastFullState()
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		default:
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
	}

	switch parts[1] {
	case "users":
		if s.rejectIfReadonly(w, r) {
			return
		}
		var body struct {
			UserID int64 `json:"user_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.UserID <= 0 {
			writeErr(w, http.StatusBadRequest, "bad_request")
			return
		}
		switch r.Method {
		case http.MethodPost:
			if err := s.store.AddGroupUser(ctx, groupID, body.UserID); err != nil {
				writeErr(w, http.StatusInternalServerError, "internal")
				return
			}
			s.store.Audit(ctx, u.ID, "web_group_add_user", fmt.Sprintf("group=%d user=%d", groupID, body.UserID))
		case http.MethodDelete:
			if err := s.store.RemoveGroupUser(ctx, groupID, body.UserID); err != nil {
				writeErr(w, http.StatusInternalServerError, "internal")
				return
			}
			s.store.Audit(ctx, u.ID, "web_group_remove_user", fmt.Sprintf("group=%d user=%d", groupID, body.UserID))
		default:
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
	case "roles":
		if s.rejectIfReadonly(w, r) {
			return
		}
		var body struct {
			RoleID string `json:"role_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request")
			return
		}
		roleID := strings.TrimSpace(body.RoleID)
		if roleID == "" {
			writeErr(w, http.StatusBadRequest, "role_required")
			return
		}
		switch r.Method {
		case http.MethodPost:
			if err := s.store.AddGroupRole(ctx, groupID, roleID); err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			s.store.Audit(ctx, u.ID, "web_group_add_role", roleID)
		case http.MethodDelete:
			if err := s.store.RemoveGroupRole(ctx, groupID, roleID); err != nil {
				writeErr(w, http.StatusInternalServerError, "internal")
				return
			}
			s.store.Audit(ctx, u.ID, "web_group_remove_role", roleID)
		default:
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
	case "accounts":
		if s.rejectIfReadonly(w, r) {
			return
		}
		var body struct {
			AccountID int64 `json:"account_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.AccountID <= 0 {
			writeErr(w, http.StatusBadRequest, "bad_request")
			return
		}
		switch r.Method {
		case http.MethodPost:
			if err := s.store.LinkAccountGroup(ctx, body.AccountID, groupID); err != nil {
				writeErr(w, http.StatusInternalServerError, "internal")
				return
			}
			s.store.AuditAccount(ctx, u.ID, body.AccountID, "web_group_link_account", fmt.Sprintf("group=%d account=%d", groupID, body.AccountID))
		case http.MethodDelete:
			if err := s.store.UnlinkAccountGroup(ctx, body.AccountID, groupID); err != nil {
				writeErr(w, http.StatusInternalServerError, "internal")
				return
			}
			s.store.AuditAccount(ctx, u.ID, body.AccountID, "web_group_unlink_account", fmt.Sprintf("group=%d account=%d", groupID, body.AccountID))
		default:
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
	default:
		writeErr(w, http.StatusNotFound, "not_found")
		return
	}
	s.hub.BroadcastFullState()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	accountID, _ := strconv.ParseInt(q.Get("account_id"), 10, 64)
	userID, _ := strconv.ParseInt(q.Get("user_id"), 10, 64)
	if limit <= 0 {
		limit = 100
	}
	entries, err := s.store.ListAccountAudits(ctx, accountID, userID, limit, offset)
	if err != nil {
		s.log.Error("list audit", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal")
		return
	}
	if entries == nil {
		entries = []store.AuditEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"entries": entries,
		"limit":   limit,
		"offset":  offset,
	})
}

func (s *Server) handleSettingsBackup(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	switch r.Method {
	case http.MethodGet:
		ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
		defer cancel()
		bak, err := s.exportConfigBackup(ctx)
		if err != nil {
			s.log.Error("export config backup", "err", err)
			writeErr(w, http.StatusInternalServerError, "export_failed")
			return
		}
		s.store.Audit(ctx, u.ID, "web_export_config",
			fmt.Sprintf("users=%d groups=%d accounts=%d", len(bak.Users), len(bak.Groups), len(bak.Accounts)))
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="alfred-identity-config.json"`)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(bak)
	case http.MethodPost:
		ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
		defer cancel()
		var reader io.Reader = r.Body
		ct := r.Header.Get("Content-Type")
		if strings.HasPrefix(ct, "multipart/form-data") {
			if err := r.ParseMultipartForm(32 << 20); err != nil {
				writeErr(w, http.StatusBadRequest, "bad_multipart")
				return
			}
			f, _, err := r.FormFile("file")
			if err != nil {
				writeErr(w, http.StatusBadRequest, "file_required")
				return
			}
			defer f.Close()
			reader = f
		}
		res, err := s.importConfigBackup(ctx, reader)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		s.store.Audit(ctx, u.ID, "web_import_config",
			fmt.Sprintf("users=%d/%d groups=%d/%d accounts=%d/%d errors=%d",
				res.UsersAdded, res.UsersUpdated, res.GroupsAdded, res.GroupsUpdated,
				res.AccountsAdded, res.AccountsUpdated, len(res.Errors)))
		s.hub.BroadcastFullState()
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":               true,
			"users_added":      res.UsersAdded,
			"users_updated":    res.UsersUpdated,
			"groups_added":     res.GroupsAdded,
			"groups_updated":   res.GroupsUpdated,
			"accounts_added":   res.AccountsAdded,
			"accounts_updated": res.AccountsUpdated,
			"errors":           res.Errors,
		})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}
