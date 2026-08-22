package sso

import (
	"testing"
)

func TestHubConnectionsAndListeners(t *testing.T) {
	h := &Hub{}
	if got := h.Connections(); len(got) != 0 {
		t.Fatalf("%#v", got)
	}
	h.OnStateChange(nil) // no panic
	called := false
	h.OnStateChange(func() { called = true })
	h.clientsMu.Lock()
	n := len(h.stateListeners)
	h.clientsMu.Unlock()
	if n != 1 {
		t.Fatalf("listeners=%d", n)
	}
	_ = called
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
