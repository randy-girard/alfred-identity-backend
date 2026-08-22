package presence

import (
	"testing"
	"time"
)

func TestTouchBusyExpireClear(t *testing.T) {
	tr := New(50 * time.Millisecond)
	tr.Touch(7, "Hero", 42)
	if !tr.IsBusy(7) {
		t.Fatal("expected busy")
	}
	if tr.Count() != 1 {
		t.Fatalf("count=%d", tr.Count())
	}
	online := tr.Online()
	if len(online) != 1 || online[0].AccountID != 7 || online[0].CharacterName != "Hero" {
		t.Fatalf("online=%#v", online)
	}
	snap := tr.Snapshot()
	if len(snap) != 1 || snap[0].UserID != 42 {
		t.Fatalf("snapshot=%#v", snap)
	}

	time.Sleep(60 * time.Millisecond)
	if tr.IsBusy(7) {
		t.Fatal("expected expired")
	}
	if tr.Count() != 0 {
		t.Fatalf("count after expire=%d", tr.Count())
	}

	tr.Touch(8, "Alt", 1)
	tr.Clear(8)
	if tr.IsBusy(8) {
		t.Fatal("cleared account still busy")
	}
}

func TestSnapshotSorted(t *testing.T) {
	tr := New(time.Minute)
	tr.Touch(30, "c", 1)
	tr.Touch(10, "a", 1)
	tr.Touch(20, "b", 1)
	snap := tr.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("len=%d", len(snap))
	}
	if snap[0].AccountID != 10 || snap[1].AccountID != 20 || snap[2].AccountID != 30 {
		t.Fatalf("order=%v", []int64{snap[0].AccountID, snap[1].AccountID, snap[2].AccountID})
	}
}
