package store

import "testing"

func TestNormalizeWebRole(t *testing.T) {
	cases := map[string]string{
		"":         "",
		"none":     "",
		"OFF":      "",
		"disabled": "",
		"admin":    "admin",
		"Admin":    "admin",
		"readonly": "readonly",
		"viewer":   "readonly",
		"read_only": "readonly",
	}
	for in, want := range cases {
		got, err := NormalizeWebRole(in)
		if err != nil || got != want {
			t.Fatalf("%q → %q err=%v want %q", in, got, err, want)
		}
	}
	if _, err := NormalizeWebRole("superuser"); err == nil {
		t.Fatal("expected error")
	}
}
