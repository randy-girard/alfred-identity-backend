package web

import (
	"encoding/json"
	"testing"

	"github.com/alfred-identity/web/internal/config"
)

func TestHostFromPublicURL(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:8181":         "127.0.0.1:8181",
		"https://identity.example.com":  "identity.example.com",
		"https://identity.example.com/": "identity.example.com",
		"identity.example.com":          "identity.example.com",
		"":                              "",
	}
	for in, want := range cases {
		if got := HostFromPublicURL(in); got != want {
			t.Fatalf("HostFromPublicURL(%q)=%q want %q", in, got, want)
		}
	}
}

func TestDefaultSourceName(t *testing.T) {
	if got := defaultSourceName("127.0.0.1:8181"); got != "Local daemon" {
		t.Fatalf("got %q", got)
	}
	if got := defaultSourceName("localhost:8181"); got != "Local daemon" {
		t.Fatalf("got %q", got)
	}
	if got := defaultSourceName("identity.example.com"); got != "identity.example.com" {
		t.Fatalf("got %q", got)
	}
}

func TestSourceHostAndNameFromConfig(t *testing.T) {
	cfg := config.Config{
		WebPublicURL:     "http://127.0.0.1:8181",
		WebSSOSourceName: "Test Guild",
	}
	if got := SourceHostFromConfig(cfg); got != "127.0.0.1:8181" {
		t.Fatalf("host %q", got)
	}
	if got := SourceNameFromConfig(cfg); got != "Test Guild" {
		t.Fatalf("name %q", got)
	}
	cfg = config.Config{HTTPAddr: "0.0.0.0:8080"}
	if got := SourceHostFromConfig(cfg); got != "127.0.0.1:8080" {
		t.Fatalf("fallback host %q", got)
	}
}

func TestBuildSourceImportJSON(t *testing.T) {
	raw := BuildSourceImportJSON("Guild", "127.0.0.1:8181", "secret-token", "")
	var obj SourceImportJSON
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		t.Fatal(err)
	}
	if obj.Name != "Guild" || obj.Host != "127.0.0.1:8181" || obj.Token != "secret-token" {
		t.Fatalf("%+v", obj)
	}
	if obj.Notes == "" {
		t.Fatal("expected default notes")
	}
}
