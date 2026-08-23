package store

import "testing"

func TestDiffNewShareRecipients(t *testing.T) {
	got := DiffNewShareRecipients([]int64{1, 2, 3}, []int64{2, 3, 4, 5})
	want := []int64{4, 5}
	if len(got) != len(want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v want %#v", got, want)
		}
	}
	if len(DiffNewShareRecipients(nil, nil)) != 0 {
		t.Fatal("nil inputs should be empty")
	}
}
