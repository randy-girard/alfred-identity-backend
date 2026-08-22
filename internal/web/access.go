package web

import (
	"context"
	"net/http"
	"time"

	"github.com/alfred-identity/web/internal/store"
)

const (
	webRoleNone     = ""
	webRoleAdmin    = "admin"
	webRoleReadonly = "readonly"
)

// webAccessLevel returns admin, readonly, or empty (denied).
// Bootstrap Discord IDs and the Discord admin role are always admin.
// Access groups with web_role grant admin or readonly (highest wins).
// Legacy WEB_ACCESS_ROLE_ID (when distinct from admin) grants readonly.
func (s *Server) webAccessLevel(ctx context.Context, u store.User) string {
	for _, id := range s.bootstrapIDs {
		if id == u.DiscordID {
			return webRoleAdmin
		}
	}
	if s.adminRoleID != "" {
		for _, r := range u.RoleIDs {
			if r == s.adminRoleID {
				return webRoleAdmin
			}
		}
	}
	groupLevel := ""
	if s.store != nil {
		var err error
		groupLevel, err = s.store.HighestWebRoleForUser(ctx, u)
		if err != nil {
			groupLevel = ""
		}
	}
	switch groupLevel {
	case webRoleAdmin:
		return webRoleAdmin
	case webRoleReadonly:
		return webRoleReadonly
	}
	// Legacy: dedicated web access role (not the admin role) → readonly.
	if s.accessRoleID != "" && s.accessRoleID != s.adminRoleID {
		for _, r := range u.RoleIDs {
			if r == s.accessRoleID {
				return webRoleReadonly
			}
		}
	}
	return webRoleNone
}

func (s *Server) canAccessWeb(ctx context.Context, u store.User) bool {
	return s.webAccessLevel(ctx, u) != webRoleNone
}

func (s *Server) isWebAdmin(ctx context.Context, u store.User) bool {
	return s.webAccessLevel(ctx, u) == webRoleAdmin
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if !s.isWebAdmin(ctx, u) {
			writeErr(w, http.StatusForbidden, "readonly")
			return
		}
		next(w, r)
	})
}
