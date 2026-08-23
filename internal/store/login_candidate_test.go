package store

import "testing"

func TestLoginCandidateDirect(t *testing.T) {
	cases := []struct {
		name string
		c    LoginCandidate
		want bool
	}{
		{name: "username", c: LoginCandidate{ByUser: true}, want: true},
		{name: "alias", c: LoginCandidate{ByAlias: true}, want: true},
		{name: "character", c: LoginCandidate{ByCharacter: true}, want: true},
		{name: "tag_only", c: LoginCandidate{ByTag: true}, want: false},
		{name: "empty", c: LoginCandidate{}, want: false},
		{name: "tag_and_user", c: LoginCandidate{ByUser: true, ByTag: true}, want: true},
	}
	for _, tc := range cases {
		if got := tc.c.Direct(); got != tc.want {
			t.Fatalf("%s: Direct()=%v want %v", tc.name, got, tc.want)
		}
	}
}
