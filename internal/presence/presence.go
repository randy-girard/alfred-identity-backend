package presence

import (
	"sort"
	"sync"
	"time"

	"github.com/alfred-identity/web/internal/store"
)

type entry struct {
	AccountID     int64
	CharacterName string
	UserID        int64
	LastSeen      time.Time
}

type Tracker struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[int64]entry // by account id
}

func New(ttl time.Duration) *Tracker {
	return &Tracker{ttl: ttl, m: make(map[int64]entry)}
}

func (t *Tracker) Touch(accountID int64, character string, userID int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.m[accountID] = entry{
		AccountID:     accountID,
		CharacterName: character,
		UserID:        userID,
		LastSeen:      time.Now(),
	}
}

func (t *Tracker) Clear(accountID int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.m, accountID)
}

func (t *Tracker) IsBusy(accountID int64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expireLocked()
	e, ok := t.m[accountID]
	return ok && time.Since(e.LastSeen) < t.ttl
}

func (t *Tracker) Online() []store.OnlineEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expireLocked()
	out := make([]store.OnlineEntry, 0, len(t.m))
	for _, e := range t.m {
		out = append(out, store.OnlineEntry{AccountID: e.AccountID, CharacterName: e.CharacterName})
	}
	return out
}

// SnapshotEntry is a live presence row for admin status views.
type SnapshotEntry struct {
	AccountID     int64
	CharacterName string
	UserID        int64
	LastSeen      time.Time
}

// Snapshot returns non-expired presence entries (sorted by account id).
func (t *Tracker) Snapshot() []SnapshotEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expireLocked()
	out := make([]SnapshotEntry, 0, len(t.m))
	for _, e := range t.m {
		out = append(out, SnapshotEntry{
			AccountID:     e.AccountID,
			CharacterName: e.CharacterName,
			UserID:        e.UserID,
			LastSeen:      e.LastSeen,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AccountID < out[j].AccountID })
	return out
}

func (t *Tracker) Count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expireLocked()
	return len(t.m)
}

func (t *Tracker) expireLocked() {
	now := time.Now()
	for id, e := range t.m {
		if now.Sub(e.LastSeen) >= t.ttl {
			delete(t.m, id)
		}
	}
}
