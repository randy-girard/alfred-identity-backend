package web

import (
	"context"
	"net/http"

	"github.com/alfred-identity/web/internal/store"
)

// webVisibleAccounts returns SSO accounts the viewer may see on the Accounts tab.
// Non-restricted accounts with no role/user/group grants are visible to every web user with SSO access;
// restricted private shares follow AllowedAccountIDs (owner and share recipients only).
// Web admins also see non-restricted accounts they do not personally have SSO access to, for management.
func (s *Server) webVisibleAccounts(ctx context.Context, u store.User) ([]store.EQAccountMeta, error) {
	all, err := s.store.ListEQAccountMetas(ctx, nil, true)
	if err != nil {
		return nil, err
	}
	allowed, err := s.store.AllowedAccountIDs(ctx, u)
	if err != nil {
		return nil, err
	}
	allowedSet := make(map[int64]struct{}, len(allowed))
	for _, id := range allowed {
		allowedSet[id] = struct{}{}
	}
	admin := s.isWebAdmin(ctx, u)
	out := make([]store.EQAccountMeta, 0, len(all))
	for _, a := range all {
		_, hasAccess := allowedSet[a.ID]
		switch {
		case a.Restricted:
			if !hasAccess {
				continue
			}
			meta, err := s.store.LoadEQAccountMetaForViewer(ctx, a.ID, u)
			if err != nil {
				continue
			}
			out = append(out, meta)
		case hasAccess || admin:
			out = append(out, a)
		}
	}
	return out, nil
}

// webVisibleShares returns restricted accounts for the Shared accounts tab: owned shares for any
// web user, and all private shares for web admins (with full grant details).
func (s *Server) webVisibleShares(ctx context.Context, u store.User) ([]store.EQAccountMeta, error) {
	all, err := s.store.ListEQAccountMetas(ctx, nil, true)
	if err != nil {
		return nil, err
	}
	admin := s.isWebAdmin(ctx, u)
	out := make([]store.EQAccountMeta, 0)
	for _, a := range all {
		if !a.Restricted {
			continue
		}
		if a.OwnerUserID == u.ID {
			meta, err := s.store.LoadEQAccountMetaForViewer(ctx, a.ID, u)
			if err != nil {
				continue
			}
			out = append(out, meta)
			continue
		}
		if admin {
			meta, err := s.store.LoadEQAccountMeta(ctx, a.ID)
			if err != nil {
				continue
			}
			out = append(out, meta)
		}
	}
	return out, nil
}

// rejectRestrictedAccountManage blocks web mutations on private shares unless the actor owns the share.
// Access grants and passwords on restricted accounts are always rejected (managed in the desktop GUI).
func (s *Server) rejectRestrictedAccountManage(w http.ResponseWriter, ctx context.Context, accountID int64, u store.User, setAccess, setPassword bool) bool {
	meta, err := s.store.LoadEQAccountMeta(ctx, accountID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found")
		return true
	}
	if !meta.Restricted {
		return false
	}
	if setAccess {
		writeErr(w, http.StatusForbidden, "share_access_managed_in_gui")
		return true
	}
	if setPassword {
		writeErr(w, http.StatusForbidden, "share_password_managed_in_gui")
		return true
	}
	if meta.OwnerUserID != u.ID {
		writeErr(w, http.StatusForbidden, "share_not_owner")
		return true
	}
	return false
}
