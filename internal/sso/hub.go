package sso

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/alfred-identity/web/internal/crypto"
	"github.com/alfred-identity/web/internal/presence"
	"github.com/alfred-identity/web/internal/store"
	"golang.org/x/time/rate"
)

const DefaultProtocolVersion = 1

const (
	maxUsernameLen  = 64
	maxPasswordLen  = 128
	maxAliasLen     = 64
	maxTagLen       = 64
	maxCharacterLen = 64
)

type Hub struct {
	Store             *store.Store
	Presence          *presence.Tracker
	ProtocolVersion   int
	AdminRoleID       string
	BootstrapAdminIDs []string
	LoginAuthLimiter  *rate.Limiter
	perToken          sync.Map // userID -> *rate.Limiter
	ratePerMin        int
	Log               *slog.Logger
	ShareNotifier     AccountShareNotifier

	clientsMu sync.Mutex
	clients   map[string]*wsClient
	stateListeners []func()
}

// AccountShareNotifier sends optional notifications when private shares are granted.
type AccountShareNotifier interface {
	NotifyAccountShared(ctx context.Context, owner store.User, accountUsername string, aliases []string, newRecipientUserIDs []int64)
}

type wsClient struct {
	id            string
	conn          *websocket.Conn
	user          store.User
	clientVersion string
	connectedAt   time.Time
	writeMu       sync.Mutex
}

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // origin; TLS terminated externally
	})
	if err != nil {
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	ctx := r.Context()
	log := h.Log
	if log == nil {
		log = slog.Default()
	}

	var (
		user   store.User
		authed bool
		client *wsClient
	)
	defer func() {
		if client != nil {
			h.unregister(client.id)
		}
	}()

	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var tip struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(data, &tip); err != nil {
			_ = writeJSON(ctx, c, nil, map[string]any{"type": "error", "message": "invalid json"})
			continue
		}
		switch tip.Type {
		case "auth":
			var msg struct {
				Type            string `json:"type"`
				Token           string `json:"token"`
				ProtocolVersion int    `json:"protocol_version"`
				ClientVersion   string `json:"client_version"`
			}
			if err := json.Unmarshal(data, &msg); err != nil {
				_ = writeJSON(ctx, c, nil, map[string]any{"type": "error", "message": "bad auth"})
				continue
			}
			if msg.ProtocolVersion != h.ProtocolVersion {
				_ = writeJSON(ctx, c, nil, map[string]any{
					"type": "error", "message": "unsupported protocol_version",
					"want": h.ProtocolVersion, "got": msg.ProtocolVersion,
				})
				_ = c.Close(websocket.StatusPolicyViolation, "protocol")
				return
			}
			u, _, err := h.Store.UserByToken(ctx, msg.Token)
			if err != nil {
				log.Info("ws auth failed", "err", err)
				_ = writeJSON(ctx, c, nil, map[string]any{"type": "error", "message": "unauthorized"})
				_ = c.Close(websocket.StatusPolicyViolation, "auth")
				return
			}
			user = u
			authed = true
			if client != nil {
				h.unregister(client.id)
			}
			client = h.register(c, user, strings.TrimSpace(msg.ClientVersion))
			log.Info("ws auth ok", "user_id", user.ID, "discord_id", user.DiscordID, "client_version", msg.ClientVersion, "is_admin", h.userIsAdmin(user))
			_ = h.sendFullState(ctx, client)
			h.Store.Audit(ctx, user.ID, "ws_auth", msg.ClientVersion)

		case "get_state":
			if !authed || client == nil {
				_ = writeJSON(ctx, c, nil, map[string]any{"type": "error", "message": "unauthorized"})
				continue
			}
			// Refresh roles from DB so admin flag stays current after Discord resync.
			if u, err := h.Store.UserByID(ctx, user.ID); err == nil {
				user = u
				client.user = u
			}
			_ = h.sendFullState(ctx, client)

		case "login_auth":
			if !authed {
				_ = writeJSON(ctx, c, nil, map[string]any{"type": "error", "message": "unauthorized"})
				continue
			}
			var msg struct {
				Type      string `json:"type"`
				RequestID string `json:"request_id"`
				Username  string `json:"username"`
			}
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			lim := h.limiterFor(user.ID)
			if !lim.Allow() {
				_ = writeJSON(ctx, c, client, map[string]any{
					"type": "login_auth_response", "request_id": msg.RequestID, "error": "rate_limited",
				})
				continue
			}
			cands, err := h.Store.ResolveLoginCandidates(ctx, user, msg.Username)
			if err != nil || len(cands) == 0 {
				_ = writeJSON(ctx, c, client, map[string]any{
					"type": "login_auth_response", "request_id": msg.RequestID, "error": "not_found",
				})
				continue
			}
			chosen := pickLoginCandidate(cands, h.Presence)
			if chosen == 0 {
				_ = writeJSON(ctx, c, client, map[string]any{
					"type": "login_auth_response", "request_id": msg.RequestID, "error": "all_busy",
				})
				continue
			}
			realUser, password, err := h.Store.DecryptCredentials(ctx, chosen)
			if err != nil {
				_ = writeJSON(ctx, c, client, map[string]any{
					"type": "login_auth_response", "request_id": msg.RequestID, "error": "internal",
				})
				continue
			}
			blob, err := crypto.PackCredentials(realUser, password)
			password = ""
			if err != nil {
				_ = writeJSON(ctx, c, client, map[string]any{
					"type": "login_auth_response", "request_id": msg.RequestID, "error": "internal",
				})
				continue
			}
			_ = writeJSON(ctx, c, client, map[string]any{
				"type": "login_auth_response", "request_id": msg.RequestID,
				"real_user": realUser, "encrypted_credentials": base64.StdEncoding.EncodeToString(blob),
				"account_id": chosen,
			})
			h.Store.AuditAccount(ctx, user.ID, chosen, "login_auth", msg.Username)
			// Owners watching share activity get an updated full_state.
			h.broadcastFullState()

		case "heartbeat":
			if !authed {
				continue
			}
			var msg struct {
				Type          string `json:"type"`
				CharacterName string `json:"character_name"`
				Offline       bool   `json:"offline"`
			}
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			acctID, err := h.Store.AccountIDByCharacter(ctx, msg.CharacterName)
			if err != nil {
				log.Debug("heartbeat unknown character", "character", msg.CharacterName, "err", err)
				continue
			}
			allowed, err := h.Store.AllowedAccountIDs(ctx, user)
			if err != nil {
				continue
			}
			ok := false
			for _, id := range allowed {
				if id == acctID {
					ok = true
					break
				}
			}
			if !ok {
				log.Debug("heartbeat account not allowed", "character", msg.CharacterName, "account_id", acctID, "user_id", user.ID)
				continue
			}
			if msg.Offline {
				h.Presence.Clear(acctID)
			} else {
				h.Presence.Touch(acctID, msg.CharacterName, user.ID)
			}
			h.notifyStateListeners()

		case "share_account", "unshare_account":
			h.handleShare(ctx, c, client, &user, &authed, tip.Type, data)

		case "admin_add_account", "admin_add_alias", "admin_add_tag", "admin_add_character",
			"admin_remove_alias", "admin_remove_tag", "admin_remove_character",
			"admin_remove_account", "admin_update_account", "admin_set_user_access", "admin_set_user_roles":
			h.handleAdmin(ctx, c, client, &user, &authed, tip.Type, data)

		case "pong":
		case "ping":
			if !authed {
				continue
			}
			_ = writeJSON(ctx, c, client, map[string]any{"type": "pong"})
		default:
			_ = writeJSON(ctx, c, client, map[string]any{"type": "error", "message": "unknown type"})
		}
	}
}

func (h *Hub) handleShare(ctx context.Context, c *websocket.Conn, client *wsClient, user *store.User, authed *bool, typ string, data []byte) {
	reqID := ""
	fail := func(errCode string) {
		_ = writeJSON(ctx, c, client, map[string]any{
			"type": "share_result", "request_id": reqID, "ok": false, "error": errCode,
		})
	}
	var tip struct {
		RequestID string `json:"request_id"`
	}
	_ = json.Unmarshal(data, &tip)
	reqID = tip.RequestID
	if reqID == "" {
		reqID = uuid.NewString()
	}
	if !*authed || client == nil {
		fail("unauthorized")
		return
	}
	if u, err := h.Store.UserByID(ctx, user.ID); err == nil {
		*user = u
		client.user = u
	}
	if user.AccessRevoked {
		fail("access_revoked")
		return
	}

	switch typ {
	case "share_account":
		var msg struct {
			RequestID string   `json:"request_id"`
			Username  string   `json:"username"`
			Password  string   `json:"password"`
			Aliases   []string `json:"aliases"`
			UserIDs   []int64  `json:"user_ids"`
			RoleIDs   []string `json:"role_ids"`
			GroupIDs  []int64  `json:"group_ids"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			fail("bad_request")
			return
		}
		username := strings.TrimSpace(msg.Username)
		if err := validateUsername(username); err != nil {
			fail(err.Error())
			return
		}
		if pw := strings.TrimSpace(msg.Password); pw != "" {
			if err := validatePassword(pw); err != nil {
				fail(err.Error())
				return
			}
		}
		id, newRecipients, err := h.Store.ShareLocalAccount(ctx, *user, username, msg.Password, msg.Aliases, msg.UserIDs, msg.RoleIDs, msg.GroupIDs)
		if err != nil {
			h.Log.Error("share_account", "err", err)
			errMsg := err.Error()
			switch {
			case strings.Contains(errMsg, "already exists"):
				fail("account_exists")
			case strings.Contains(errMsg, "password required"):
				fail("password_required")
			case strings.Contains(errMsg, "user not found"):
				fail("user_not_found")
			case strings.Contains(errMsg, "access revoked"):
				fail("user_revoked")
			default:
				fail("internal")
			}
			return
		}
		h.Store.AuditAccount(ctx, user.ID, id, "share_account", fmt.Sprintf("account=%d users=%v roles=%v groups=%v", id, msg.UserIDs, msg.RoleIDs, msg.GroupIDs))
		if h.ShareNotifier != nil && len(newRecipients) > 0 {
			go h.ShareNotifier.NotifyAccountShared(context.Background(), *user, username, msg.Aliases, newRecipients)
		}
		_ = writeJSON(ctx, c, client, map[string]any{
			"type": "share_result", "request_id": reqID, "ok": true, "account_id": id,
		})
		h.broadcastFullState()

	case "unshare_account":
		var msg struct {
			RequestID string `json:"request_id"`
			Username  string `json:"username"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			fail("bad_request")
			return
		}
		username := strings.TrimSpace(msg.Username)
		if err := validateUsername(username); err != nil {
			fail(err.Error())
			return
		}
		if err := h.Store.UnshareLocalAccount(ctx, *user, username); err != nil {
			h.Log.Error("unshare_account", "err", err)
			errMsg := err.Error()
			switch {
			case strings.Contains(errMsg, "not shared"):
				fail("not_shared")
			case strings.Contains(errMsg, "not your"):
				fail("forbidden")
			default:
				fail("internal")
			}
			return
		}
		h.Store.Audit(ctx, user.ID, "unshare_account", "username="+username)
		_ = writeJSON(ctx, c, client, map[string]any{
			"type": "share_result", "request_id": reqID, "ok": true,
		})
		h.broadcastFullState()

	default:
		fail("unknown")
	}
}

func (h *Hub) handleAdmin(ctx context.Context, c *websocket.Conn, client *wsClient, user *store.User, authed *bool, typ string, data []byte) {
	reqID := ""
	fail := func(errCode string) {
		_ = writeJSON(ctx, c, client, map[string]any{
			"type": "admin_result", "request_id": reqID, "ok": false, "error": errCode,
		})
	}
	var tip struct {
		RequestID string `json:"request_id"`
	}
	_ = json.Unmarshal(data, &tip)
	reqID = tip.RequestID
	if reqID == "" {
		reqID = uuid.NewString()
	}
	if !*authed || client == nil {
		fail("unauthorized")
		return
	}
	// Fresh roles from DB (Discord resync may have updated them).
	if u, err := h.Store.UserByID(ctx, user.ID); err == nil {
		*user = u
		client.user = u
	}
	if !h.userIsAdmin(*user) {
		h.Log.Info("ws admin denied", "user_id", user.ID, "discord_id", user.DiscordID, "op", typ)
		fail("forbidden")
		return
	}
	if !h.limiterFor(user.ID).Allow() {
		fail("rate_limited")
		return
	}

	switch typ {
	case "admin_add_account":
		var msg struct {
			RequestID      string  `json:"request_id"`
			Username       string  `json:"username"`
			Password       string  `json:"password"`
			RequiredRoleID *string `json:"required_role_id"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			fail("bad_request")
			return
		}
		username := strings.TrimSpace(msg.Username)
		password := msg.Password
		if err := validateUsername(username); err != nil {
			fail(err.Error())
			return
		}
		if err := validatePassword(password); err != nil {
			fail(err.Error())
			return
		}
		id, err := h.Store.AddEQAccount(ctx, username, password, "")
		password = ""
		if err != nil {
			h.Log.Error("admin_add_account", "err", err)
			fail("internal")
			return
		}
		if msg.RequiredRoleID != nil {
			roleID := strings.TrimSpace(*msg.RequiredRoleID)
			if err := h.Store.SetEQRequiredRole(ctx, id, roleID); err != nil {
				h.Log.Error("admin_add_account set role", "err", err)
				fail("internal")
				return
			}
		}
		h.Store.AuditAccount(ctx, user.ID, id, "admin_add_account", fmt.Sprintf("id=%d", id))
		_ = writeJSON(ctx, c, client, map[string]any{
			"type": "admin_result", "request_id": reqID, "ok": true, "account_id": id,
		})
		h.broadcastFullState()

	case "admin_update_account":
		var msg struct {
			RequestID      string  `json:"request_id"`
			AccountID      int64   `json:"account_id"`
			Password       *string `json:"password"`
			Disabled       *bool   `json:"disabled"`
			RequiredRoleID *string `json:"required_role_id"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			fail("bad_request")
			return
		}
		if msg.AccountID <= 0 {
			fail("invalid_account")
			return
		}
		changed := false
		if msg.Password != nil {
			pw := *msg.Password
			if pw != "" {
				if err := validatePassword(pw); err != nil {
					fail(err.Error())
					return
				}
				if err := h.Store.SetEQPassword(ctx, msg.AccountID, pw); err != nil {
					h.Log.Error("admin_update_account password", "err", err)
					fail("internal")
					return
				}
				changed = true
			}
		}
		if msg.Disabled != nil {
			if err := h.Store.SetEQDisabled(ctx, msg.AccountID, *msg.Disabled); err != nil {
				h.Log.Error("admin_update_account disabled", "err", err)
				fail("internal")
				return
			}
			changed = true
		}
		if msg.RequiredRoleID != nil {
			roleID := strings.TrimSpace(*msg.RequiredRoleID)
			if err := h.Store.SetEQRequiredRole(ctx, msg.AccountID, roleID); err != nil {
				h.Log.Error("admin_update_account role", "err", err)
				fail("internal")
				return
			}
			changed = true
		}
		if !changed {
			fail("nothing_to_update")
			return
		}
		h.Store.AuditAccount(ctx, user.ID, msg.AccountID, "admin_update_account", fmt.Sprintf("account=%d", msg.AccountID))
		_ = writeJSON(ctx, c, client, map[string]any{
			"type": "admin_result", "request_id": reqID, "ok": true, "account_id": msg.AccountID,
		})
		h.broadcastFullState()

	case "admin_add_alias":
		var msg struct {
			RequestID string `json:"request_id"`
			Alias     string `json:"alias"`
			AccountID int64  `json:"account_id"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			fail("bad_request")
			return
		}
		alias := strings.TrimSpace(msg.Alias)
		if err := validateAlias(alias); err != nil {
			fail(err.Error())
			return
		}
		if msg.AccountID <= 0 {
			fail("invalid_account")
			return
		}
		if err := h.Store.AddAlias(ctx, alias, msg.AccountID); err != nil {
			h.Log.Error("admin_add_alias", "err", err)
			msg := err.Error()
			if strings.Contains(msg, "already used") {
				fail("alias_taken")
				return
			}
			fail("internal")
			return
		}
		h.Store.AuditAccount(ctx, user.ID, msg.AccountID, "admin_add_alias", fmt.Sprintf("alias=%s account=%d", alias, msg.AccountID))
		_ = writeJSON(ctx, c, client, map[string]any{
			"type": "admin_result", "request_id": reqID, "ok": true, "account_id": msg.AccountID,
		})
		h.broadcastFullState()

	case "admin_add_tag":
		var msg struct {
			RequestID string `json:"request_id"`
			Tag       string `json:"tag"`
			AccountID int64  `json:"account_id"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			fail("bad_request")
			return
		}
		tag := strings.TrimSpace(msg.Tag)
		if err := validateTag(tag); err != nil {
			fail(err.Error())
			return
		}
		if msg.AccountID <= 0 {
			fail("invalid_account")
			return
		}
		if err := h.Store.AddTag(ctx, tag, msg.AccountID); err != nil {
			h.Log.Error("admin_add_tag", "err", err)
			fail("internal")
			return
		}
		h.Store.AuditAccount(ctx, user.ID, msg.AccountID, "admin_add_tag", fmt.Sprintf("tag=%s account=%d", tag, msg.AccountID))
		_ = writeJSON(ctx, c, client, map[string]any{
			"type": "admin_result", "request_id": reqID, "ok": true, "account_id": msg.AccountID,
		})
		h.broadcastFullState()

	case "admin_remove_alias":
		var msg struct {
			RequestID string `json:"request_id"`
			Alias     string `json:"alias"`
			AccountID int64  `json:"account_id"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			fail("bad_request")
			return
		}
		alias := strings.TrimSpace(msg.Alias)
		if err := validateAlias(alias); err != nil {
			fail(err.Error())
			return
		}
		if msg.AccountID <= 0 {
			fail("invalid_account")
			return
		}
		if err := h.Store.RemoveAlias(ctx, alias, msg.AccountID); err != nil {
			h.Log.Error("admin_remove_alias", "err", err)
			if strings.Contains(err.Error(), "not found") {
				fail("not_found")
				return
			}
			fail("internal")
			return
		}
		h.Store.AuditAccount(ctx, user.ID, msg.AccountID, "admin_remove_alias", fmt.Sprintf("alias=%s account=%d", alias, msg.AccountID))
		_ = writeJSON(ctx, c, client, map[string]any{
			"type": "admin_result", "request_id": reqID, "ok": true, "account_id": msg.AccountID,
		})
		h.broadcastFullState()

	case "admin_remove_tag":
		var msg struct {
			RequestID string `json:"request_id"`
			Tag       string `json:"tag"`
			AccountID int64  `json:"account_id"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			fail("bad_request")
			return
		}
		tag := strings.TrimSpace(msg.Tag)
		if err := validateTag(tag); err != nil {
			fail(err.Error())
			return
		}
		if msg.AccountID <= 0 {
			fail("invalid_account")
			return
		}
		if err := h.Store.RemoveTag(ctx, tag, msg.AccountID); err != nil {
			h.Log.Error("admin_remove_tag", "err", err)
			if strings.Contains(err.Error(), "not found") {
				fail("not_found")
				return
			}
			fail("internal")
			return
		}
		h.Store.AuditAccount(ctx, user.ID, msg.AccountID, "admin_remove_tag", fmt.Sprintf("tag=%s account=%d", tag, msg.AccountID))
		_ = writeJSON(ctx, c, client, map[string]any{
			"type": "admin_result", "request_id": reqID, "ok": true, "account_id": msg.AccountID,
		})
		h.broadcastFullState()

	case "admin_add_character":
		var msg struct {
			RequestID string `json:"request_id"`
			Name      string `json:"name"`
			AccountID int64  `json:"account_id"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			fail("bad_request")
			return
		}
		name := strings.TrimSpace(msg.Name)
		if err := validateCharacter(name); err != nil {
			fail(err.Error())
			return
		}
		if msg.AccountID <= 0 {
			fail("invalid_account")
			return
		}
		if err := h.Store.AddCharacter(ctx, name, msg.AccountID); err != nil {
			h.Log.Error("admin_add_character", "err", err)
			fail("internal")
			return
		}
		h.Store.AuditAccount(ctx, user.ID, msg.AccountID, "admin_add_character", fmt.Sprintf("name=%s account=%d", name, msg.AccountID))
		_ = writeJSON(ctx, c, client, map[string]any{
			"type": "admin_result", "request_id": reqID, "ok": true, "account_id": msg.AccountID,
		})
		h.broadcastFullState()

	case "admin_remove_character":
		var msg struct {
			RequestID string `json:"request_id"`
			Name      string `json:"name"`
			AccountID int64  `json:"account_id"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			fail("bad_request")
			return
		}
		name := strings.TrimSpace(msg.Name)
		if name == "" || msg.AccountID <= 0 {
			fail("invalid_character")
			return
		}
		if err := h.Store.RemoveCharacter(ctx, name, msg.AccountID); err != nil {
			h.Log.Error("admin_remove_character", "err", err)
			if strings.Contains(err.Error(), "not found") {
				fail("not_found")
				return
			}
			fail("internal")
			return
		}
		h.Store.AuditAccount(ctx, user.ID, msg.AccountID, "admin_remove_character", fmt.Sprintf("name=%s account=%d", name, msg.AccountID))
		_ = writeJSON(ctx, c, client, map[string]any{
			"type": "admin_result", "request_id": reqID, "ok": true, "account_id": msg.AccountID,
		})
		h.broadcastFullState()

	case "admin_remove_account":
		var msg struct {
			RequestID string `json:"request_id"`
			AccountID int64  `json:"account_id"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			fail("bad_request")
			return
		}
		if msg.AccountID <= 0 {
			fail("invalid_account")
			return
		}
		if err := h.Store.DeleteEQAccount(ctx, msg.AccountID); err != nil {
			h.Log.Error("admin_remove_account", "err", err)
			fail("internal")
			return
		}
		h.Presence.Clear(msg.AccountID)
		h.Store.AuditAccount(ctx, user.ID, msg.AccountID, "admin_remove_account", fmt.Sprintf("id=%d", msg.AccountID))
		_ = writeJSON(ctx, c, client, map[string]any{
			"type": "admin_result", "request_id": reqID, "ok": true, "account_id": msg.AccountID,
		})
		h.broadcastFullState()

	case "admin_set_user_access":
		var msg struct {
			RequestID string `json:"request_id"`
			UserID    int64  `json:"user_id"`
			Revoked   bool   `json:"revoked"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			fail("bad_request")
			return
		}
		if msg.UserID <= 0 {
			fail("invalid_user")
			return
		}
		if msg.UserID == user.ID && msg.Revoked {
			fail("cannot_revoke_self")
			return
		}
		if err := h.Store.SetUserAccessRevoked(ctx, msg.UserID, msg.Revoked); err != nil {
			h.Log.Error("admin_set_user_access", "err", err)
			fail("internal")
			return
		}
		h.Store.Audit(ctx, user.ID, "admin_set_user_access", fmt.Sprintf("user=%d revoked=%v", msg.UserID, msg.Revoked))
		if msg.Revoked {
			h.disconnectUser(msg.UserID, "access revoked")
		}
		_ = writeJSON(ctx, c, client, map[string]any{
			"type": "admin_result", "request_id": reqID, "ok": true, "user_id": msg.UserID,
		})
		h.broadcastFullState()

	case "admin_set_user_roles":
		var msg struct {
			RequestID string   `json:"request_id"`
			UserID    int64    `json:"user_id"`
			RoleIDs   []string `json:"role_ids"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			fail("bad_request")
			return
		}
		if msg.UserID <= 0 {
			fail("invalid_user")
			return
		}
		if err := h.Store.SetUserRoles(ctx, msg.UserID, msg.RoleIDs); err != nil {
			h.Log.Error("admin_set_user_roles", "err", err)
			fail("internal")
			return
		}
		h.Store.Audit(ctx, user.ID, "admin_set_user_roles", fmt.Sprintf("user=%d roles=%d", msg.UserID, len(msg.RoleIDs)))
		_ = writeJSON(ctx, c, client, map[string]any{
			"type": "admin_result", "request_id": reqID, "ok": true, "user_id": msg.UserID,
		})
		h.broadcastFullState()
	}
}

func (h *Hub) userIsAdmin(u store.User) bool {
	for _, id := range h.BootstrapAdminIDs {
		if id == u.DiscordID {
			return true
		}
	}
	if h.AdminRoleID == "" {
		return false
	}
	for _, r := range u.RoleIDs {
		if r == h.AdminRoleID {
			return true
		}
	}
	return false
}

// IsAdmin reports whether the user may perform admin operations.
func (h *Hub) IsAdmin(u store.User) bool {
	return h.userIsAdmin(u)
}

// BroadcastFullState pushes ACL-filtered state to all SSO clients and notifies listeners.
func (h *Hub) BroadcastFullState() {
	if h == nil {
		return
	}
	h.broadcastFullState()
}

// DisconnectUser closes WS sessions for a user (e.g. after access revoke).
func (h *Hub) DisconnectUser(userID int64, reason string) {
	if h == nil {
		return
	}
	h.disconnectUser(userID, reason)
}

// OnStateChange registers a callback invoked after each full_state broadcast
// and when SSO clients connect or disconnect.
func (h *Hub) OnStateChange(fn func()) {
	if fn == nil {
		return
	}
	h.clientsMu.Lock()
	h.stateListeners = append(h.stateListeners, fn)
	h.clientsMu.Unlock()
}

// ConnectionInfo is a live desktop GUI (SSO WebSocket) client.
type ConnectionInfo struct {
	SessionID     string    `json:"session_id"`
	UserID        int64     `json:"user_id"`
	DiscordID     string    `json:"discord_id"`
	DisplayName   string    `json:"display_name"`
	ClientVersion string    `json:"client_version"`
	ConnectedAt   time.Time `json:"connected_at"`
	IsAdmin       bool      `json:"is_admin"`
}

// Connections returns currently authenticated SSO WebSocket clients.
func (h *Hub) Connections() []ConnectionInfo {
	h.clientsMu.Lock()
	defer h.clientsMu.Unlock()
	out := make([]ConnectionInfo, 0, len(h.clients))
	for _, cl := range h.clients {
		out = append(out, ConnectionInfo{
			SessionID:     cl.id,
			UserID:        cl.user.ID,
			DiscordID:     cl.user.DiscordID,
			DisplayName:   cl.user.DisplayName,
			ClientVersion: cl.clientVersion,
			ConnectedAt:   cl.connectedAt,
			IsAdmin:       h.userIsAdmin(cl.user),
		})
	}
	return out
}

func (h *Hub) notifyStateListeners() {
	h.clientsMu.Lock()
	listeners := append([]func(){}, h.stateListeners...)
	h.clientsMu.Unlock()
	for _, fn := range listeners {
		fn()
	}
}

func (h *Hub) register(c *websocket.Conn, user store.User, clientVersion string) *wsClient {
	h.clientsMu.Lock()
	if h.clients == nil {
		h.clients = make(map[string]*wsClient)
	}
	id := uuid.NewString()
	cl := &wsClient{
		id:            id,
		conn:          c,
		user:          user,
		clientVersion: clientVersion,
		connectedAt:   time.Now().UTC(),
	}
	h.clients[id] = cl
	h.clientsMu.Unlock()
	h.notifyStateListeners()
	return cl
}

func (h *Hub) unregister(id string) {
	h.clientsMu.Lock()
	_, ok := h.clients[id]
	delete(h.clients, id)
	h.clientsMu.Unlock()
	if ok {
		h.notifyStateListeners()
	}
}

func (h *Hub) sendFullState(ctx context.Context, cl *wsClient) error {
	var fs store.FullState
	var err error
	if h.userIsAdmin(cl.user) {
		// Admins see every account (including disabled / elevated) so they can manage them.
		accounts, listErr := h.Store.ListEQAccountMetas(ctx, nil, true)
		if listErr != nil {
			err = listErr
		} else {
			fs = store.FullState{Accounts: accounts, Online: h.Presence.Online()}
		}
	} else {
		fs, err = h.Store.FullStateForUser(ctx, cl.user, h.Presence.Online())
	}
	if err != nil {
		return writeJSON(ctx, cl.conn, cl, map[string]any{"type": "error", "message": "state error"})
	}
	payload := map[string]any{
		"type": "full_state", "state": fs, "is_admin": h.userIsAdmin(cl.user),
		"user_id": cl.user.ID, "discord_id": cl.user.DiscordID, "display_name": cl.user.DisplayName,
	}
	if dir, err := h.Store.ListDirectoryUsers(ctx); err == nil {
		payload["directory"] = dir
	}
	if groups, err := h.Store.ListGroupDetails(ctx); err == nil {
		payload["groups"] = groups
	}
	if roles, err := h.Store.ListDiscordRoles(ctx); err == nil {
		payload["roles"] = roles
	}
	if activity, err := h.buildShareActivity(ctx, cl.user.ID); err == nil {
		payload["share_activity"] = activity
	}
	if h.userIsAdmin(cl.user) {
		if admin, err := h.Store.AdminState(ctx); err == nil {
			payload["admin"] = admin
		}
	}
	return writeJSON(ctx, cl.conn, cl, payload)
}

func (h *Hub) buildShareActivity(ctx context.Context, ownerID int64) (store.ShareActivity, error) {
	logins, err := h.Store.ListOwnedShareLogins(ctx, ownerID, 50)
	if err != nil {
		return store.ShareActivity{}, err
	}
	if logins == nil {
		logins = []store.ShareLoginEntry{}
	}
	snap := h.Presence.Snapshot()
	hints := make([]store.PresenceHint, 0, len(snap))
	for _, e := range snap {
		hints = append(hints, store.PresenceHint{
			AccountID: e.AccountID, CharacterName: e.CharacterName,
			UserID: e.UserID, LastSeen: e.LastSeen,
		})
	}
	online, err := h.Store.BuildShareOnline(ctx, ownerID, hints)
	if err != nil {
		return store.ShareActivity{}, err
	}
	if online == nil {
		online = []store.ShareOnlineEntry{}
	}
	return store.ShareActivity{Logins: logins, Online: online}, nil
}

func (h *Hub) disconnectUser(userID int64, reason string) {
	h.clientsMu.Lock()
	list := make([]*wsClient, 0)
	for id, cl := range h.clients {
		if cl.user.ID == userID {
			list = append(list, cl)
			delete(h.clients, id)
		}
	}
	h.clientsMu.Unlock()
	for _, cl := range list {
		_ = writeJSON(context.Background(), cl.conn, cl, map[string]any{
			"type": "error", "message": reason,
		})
		_ = cl.conn.Close(websocket.StatusPolicyViolation, reason)
	}
	if len(list) > 0 {
		h.notifyStateListeners()
	}
}

func (h *Hub) broadcastFullState() {
	h.clientsMu.Lock()
	list := make([]*wsClient, 0, len(h.clients))
	for _, cl := range h.clients {
		list = append(list, cl)
	}
	listeners := append([]func(){}, h.stateListeners...)
	h.clientsMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, cl := range list {
		// Refresh user for admin flag / ACL.
		if u, err := h.Store.UserByID(ctx, cl.user.ID); err == nil {
			cl.user = u
		}
		_ = h.sendFullState(ctx, cl)
	}
	for _, fn := range listeners {
		fn()
	}
}

func (h *Hub) limiterFor(userID int64) *rate.Limiter {
	if v, ok := h.perToken.Load(userID); ok {
		return v.(*rate.Limiter)
	}
	perMin := h.ratePerMin
	if perMin <= 0 {
		perMin = 30
	}
	lim := rate.NewLimiter(rate.Every(time.Minute/time.Duration(perMin)), perMin)
	actual, _ := h.perToken.LoadOrStore(userID, lim)
	return actual.(*rate.Limiter)
}

func writeJSON(ctx context.Context, c *websocket.Conn, cl *wsClient, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if cl != nil {
		cl.writeMu.Lock()
		defer cl.writeMu.Unlock()
		return cl.conn.Write(ctx, websocket.MessageText, b)
	}
	return c.Write(ctx, websocket.MessageText, b)
}

func (h *Hub) SetRatePerMin(n int) { h.ratePerMin = n }

// pickLoginCandidate chooses an account for login_auth.
// Direct matches (username / alias / character) are never blocked by presence —
// EQ will show already-logged-in if needed. Tag pools skip busy accounts so
// clients can rotate to a free box.
func pickLoginCandidate(cands []store.LoginCandidate, pres *presence.Tracker) int64 {
	if len(cands) == 0 {
		return 0
	}
	var direct, tagPool []store.LoginCandidate
	for _, c := range cands {
		if c.Direct() {
			direct = append(direct, c)
		} else if c.ByTag {
			tagPool = append(tagPool, c)
		}
	}
	if len(direct) > 0 {
		return direct[0].ID
	}
	if len(tagPool) == 1 {
		return tagPool[0].ID
	}
	for _, c := range tagPool {
		if pres == nil || !pres.IsBusy(c.ID) {
			return c.ID
		}
	}
	return 0
}

func validateUsername(s string) error {
	if s == "" {
		return fmt.Errorf("username_required")
	}
	if utf8.RuneCountInString(s) > maxUsernameLen {
		return fmt.Errorf("username_too_long")
	}
	if strings.ContainsAny(s, "\x00\n\r") {
		return fmt.Errorf("username_invalid")
	}
	return nil
}

func validatePassword(s string) error {
	if s == "" {
		return fmt.Errorf("password_required")
	}
	if utf8.RuneCountInString(s) > maxPasswordLen {
		return fmt.Errorf("password_too_long")
	}
	if strings.ContainsRune(s, 0) {
		return fmt.Errorf("password_invalid")
	}
	return nil
}

func validateAlias(s string) error {
	if s == "" {
		return fmt.Errorf("alias_required")
	}
	if utf8.RuneCountInString(s) > maxAliasLen {
		return fmt.Errorf("alias_too_long")
	}
	if strings.ContainsAny(s, "\x00\n\r") {
		return fmt.Errorf("alias_invalid")
	}
	return nil
}

func validateTag(s string) error {
	if s == "" {
		return fmt.Errorf("tag_required")
	}
	if utf8.RuneCountInString(s) > maxTagLen {
		return fmt.Errorf("tag_too_long")
	}
	if strings.ContainsAny(s, "\x00\n\r") {
		return fmt.Errorf("tag_invalid")
	}
	return nil
}

func validateCharacter(s string) error {
	if s == "" {
		return fmt.Errorf("name_required")
	}
	if utf8.RuneCountInString(s) > maxCharacterLen {
		return fmt.Errorf("name_too_long")
	}
	if strings.ContainsAny(s, "\x00\n\r") {
		return fmt.Errorf("name_invalid")
	}
	return nil
}
