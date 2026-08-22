package sso

import (
	"testing"
	"time"

	"github.com/alfred-identity/web/internal/store"
)

func TestHubConnectionsAndListeners(t *testing.T) {
	h := &Hub{BootstrapAdminIDs: []string{"boot"}}
	if got := h.Connections(); len(got) != 0 {
		t.Fatalf("%#v", got)
	}
	h.OnStateChange(nil) // no panic
	called := 0
	h.OnStateChange(func() { called++ })
	h.clientsMu.Lock()
	n := len(h.stateListeners)
	h.clientsMu.Unlock()
	if n != 1 {
		t.Fatalf("listeners=%d", n)
	}
	h.notifyStateListeners()
	if called != 1 {
		t.Fatalf("called=%d", called)
	}

	h.clientsMu.Lock()
	h.clients = map[string]*wsClient{
		"s1": {
			id:            "s1",
			user:          store.User{ID: 9, DiscordID: "boot", DisplayName: "Boot"},
			clientVersion: "1.2.3",
			connectedAt:   time.Unix(1_700_000_000, 0).UTC(),
		},
	}
	h.clientsMu.Unlock()
	conns := h.Connections()
	if len(conns) != 1 || conns[0].SessionID != "s1" || !conns[0].IsAdmin || conns[0].ClientVersion != "1.2.3" {
		t.Fatalf("%#v", conns)
	}
}

func TestLimiterForDefault(t *testing.T) {
	h := &Hub{}
	h.SetRatePerMin(0)
	a := h.limiterFor(1)
	b := h.limiterFor(1)
	if a != b {
		t.Fatal("expected cached limiter")
	}
	c := h.limiterFor(2)
	if c == a {
		t.Fatal("expected distinct per user")
	}
}
