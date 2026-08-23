package sso

import (
	"testing"
	"time"

	"github.com/alfred-identity/web/internal/presence"
	"github.com/alfred-identity/web/internal/store"
)

func TestPickLoginCandidateDirectIgnoresBusy(t *testing.T) {
	pres := presence.New(time.Minute)
	pres.Touch(1, "Hero", 9)
	got := pickLoginCandidate([]store.LoginCandidate{
		{ID: 1, ByUser: true, ByTag: true},
		{ID: 2, ByTag: true},
	}, pres)
	if got != 1 {
		t.Fatalf("direct match should win even when busy, got %d", got)
	}
}

func TestPickLoginCandidateTagPoolSkipsBusy(t *testing.T) {
	pres := presence.New(time.Minute)
	pres.Touch(1, "Hero", 9)
	got := pickLoginCandidate([]store.LoginCandidate{
		{ID: 1, ByTag: true},
		{ID: 2, ByTag: true},
	}, pres)
	if got != 2 {
		t.Fatalf("expected free tag account 2, got %d", got)
	}
	pres.Touch(2, "Other", 9)
	got = pickLoginCandidate([]store.LoginCandidate{
		{ID: 1, ByTag: true},
		{ID: 2, ByTag: true},
	}, pres)
	if got != 0 {
		t.Fatalf("expected all_busy, got %d", got)
	}
}

func TestPickLoginCandidateSingleTagAllowsBusy(t *testing.T) {
	pres := presence.New(time.Minute)
	pres.Touch(7, "Hero", 9)
	got := pickLoginCandidate([]store.LoginCandidate{
		{ID: 7, ByTag: true},
	}, pres)
	if got != 7 {
		t.Fatalf("single tagged account should still be choosable, got %d", got)
	}
}

func TestPickLoginCandidateEmptyAndAliasDirect(t *testing.T) {
	if got := pickLoginCandidate(nil, nil); got != 0 {
		t.Fatalf("empty=%d", got)
	}
	got := pickLoginCandidate([]store.LoginCandidate{
		{ID: 3, ByAlias: true},
		{ID: 4, ByTag: true},
	}, presence.New(time.Minute))
	if got != 3 {
		t.Fatalf("alias direct should win, got %d", got)
	}
	got = pickLoginCandidate([]store.LoginCandidate{
		{ID: 5, ByCharacter: true},
	}, nil)
	if got != 5 {
		t.Fatalf("character direct=%d", got)
	}
}
