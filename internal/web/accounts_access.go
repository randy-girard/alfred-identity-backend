package web

import (
	"context"
	"net/http"

	"github.com/alfred-identity/web/internal/store"
)

// webVisibleAccounts returns non-restricted SSO accounts plus restricted shares the user may use.
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
	out := make([]store.EQAccountMeta, 0, len(all))
	for _, a := range all {
		if !a.Restricted {
			out = append(out, a)
			continue
		}
		if _, ok := allowedSet[a.ID]; !ok {
			continue
		}
		meta, err := s.store.LoadEQAccountMetaForViewer(ctx, a.ID, u)
		if err != nil {
			continue
		}
		out = append(out, meta)
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
// When setAccess is true, restricted accounts always reject (share grants are managed in the desktop GUI).
func (s *Server) rejectRestrictedAccountManage(w http.ResponseWriter, ctx context.Context, accountID int64, u store.User, setAccess bool) bool {
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
	if meta.OwnerUserID != u.ID {
		writeErr(w, http.StatusForbidden, "share_not_owner")
		return true
	}
	return false
}
