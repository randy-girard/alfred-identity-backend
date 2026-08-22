package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

func TestHandleSSOSourceJSON(t *testing.T) {
	s := &Server{publicURL: "http://127.0.0.1:8181", sourceName: "Test Guild"}
	mux := http.NewServeMux()
	s.Mount(mux)

	req := httptest.NewRequest(http.MethodGet, SSOSourcePath, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var desc SourceDescriptor
	if err := json.Unmarshal(rec.Body.Bytes(), &desc); err != nil {
		t.Fatal(err)
	}
	if desc.Name != "Test Guild" || desc.Host != "127.0.0.1:8181" {
		t.Fatalf("got %+v", desc)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type %q", ct)
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

func TestHandleSSOSourceJSONDefaultName(t *testing.T) {
	s := &Server{publicURL: "https://guild.example.com"}
	mux := http.NewServeMux()
	s.Mount(mux)
	req := httptest.NewRequest(http.MethodGet, SSOSourcePath, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var desc SourceDescriptor
	_ = json.Unmarshal(rec.Body.Bytes(), &desc)
	if desc.Name != "guild.example.com" || desc.Host != "guild.example.com" {
		t.Fatalf("%+v", desc)
	}
}
