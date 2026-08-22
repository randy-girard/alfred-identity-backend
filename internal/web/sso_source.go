package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// SSOSourcePath is the public path for the distributable GUI source JSON (not under /admin).
const SSOSourcePath = "/sso-source.json"

// SourceDescriptor is the shareable SSO source payload (no token).
type SourceDescriptor struct {
	Name  string `json:"name"`
	Host  string `json:"host"`
	Notes string `json:"notes,omitempty"`
}

func (s *Server) handleSSOSourceJSON(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	host := HostFromPublicURL(s.publicURL)
	if host == "" {
		writeErr(w, http.StatusServiceUnavailable, "WEB_PUBLIC_URL not configured")
		return
	}
	name := strings.TrimSpace(s.sourceName)
	if name == "" {
		name = defaultSourceName(host)
	}
	desc := SourceDescriptor{
		Name:  name,
		Host:  host,
		Notes: "Paste your Discord SSO token in the Alfred Identity app after adding this source.",
	}
	b, err := json.MarshalIndent(desc, "", "  ")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "encode failed")
		return
	}
	b = append(b, '\n')
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="sso-source.json"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(b)
}

// HostFromPublicURL extracts host[:port] from a WEB_PUBLIC_URL origin.
func HostFromPublicURL(publicURL string) string {
	publicURL = strings.TrimSpace(publicURL)
	if publicURL == "" {
		return ""
	}
	if !strings.Contains(publicURL, "://") {
		publicURL = "https://" + publicURL
	}
	u, err := url.Parse(publicURL)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Host
}

func defaultSourceName(host string) string {
	h := host
	if i := strings.Index(h, ":"); i >= 0 {
		h = h[:i]
	}
	h = strings.Trim(h, "[]")
	if h == "" || h == "127.0.0.1" || h == "localhost" {
		return "Local daemon"
	}
	return h
}
