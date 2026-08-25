package store

import (
	"context"
	"errors"
)

var (
	// ErrShareAccessManagedInGUI is returned when access grants on a private share
	// are mutated outside the desktop share_account path.
	ErrShareAccessManagedInGUI = errors.New("share_access_managed_in_gui")
	// ErrSharePasswordManagedInGUI is returned when a private-share password is
	// changed outside the desktop share path.
	ErrSharePasswordManagedInGUI = errors.New("share_password_managed_in_gui")
	// ErrShareNotOwner is returned when a non-owner mutates a private share.
	ErrShareNotOwner = errors.New("share_not_owner")
)

// CheckRestrictedAccountManage enforces private-share ownership rules shared by
// web admin and SSO admin WebSocket handlers.
//
// Restricted + set password / set access → always forbidden (GUI-managed).
// Restricted + other mutations → require owner_user_id == actorUserID.
// Non-restricted accounts → allowed.
func (s *Store) CheckRestrictedAccountManage(ctx context.Context, accountID, actorUserID int64, setAccess, setPassword bool) error {
	meta, err := s.LoadEQAccountMeta(ctx, accountID)
	if err != nil {
		return err
	}
	if !meta.Restricted {
		return nil
	}
	if setAccess {
		return ErrShareAccessManagedInGUI
	}
	if setPassword {
		return ErrSharePasswordManagedInGUI
	}
	if meta.OwnerUserID != actorUserID {
		return ErrShareNotOwner
	}
	return nil
}
