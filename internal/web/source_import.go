package web

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/alfred-identity/web/internal/config"
)

// SourceImportJSON is pasted into Alfred Identity → Connections → Add from JSON.
type SourceImportJSON struct {
	Name  string `json:"name"`
	Host  string `json:"host"`
	Token string `json:"token,omitempty"`
	Notes string `json:"notes,omitempty"`
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

// SourceHostFromConfig returns host:port for GUI source JSON from daemon config.
func SourceHostFromConfig(cfg config.Config) string {
	if h := HostFromPublicURL(cfg.WebPublicURL); h != "" {
		return h
	}
	addr := strings.TrimSpace(cfg.HTTPAddr)
	if strings.HasPrefix(addr, "0.0.0.0:") {
		return "127.0.0.1" + addr[7:]
	}
	return addr
}

// SourceNameFromConfig returns the display name for GUI source JSON.
func SourceNameFromConfig(cfg config.Config) string {
	if n := strings.TrimSpace(cfg.WebSSOSourceName); n != "" {
		return n
	}
	return defaultSourceName(SourceHostFromConfig(cfg))
}

// BuildSourceImportJSON returns indented JSON for paste into Alfred Identity.
func BuildSourceImportJSON(name, host, token, notes string) string {
	if notes == "" {
		notes = "Paste into Alfred Identity → Connections → Add from JSON."
	}
	obj := SourceImportJSON{
		Name:  name,
		Host:  host,
		Token: token,
		Notes: notes,
	}
	b, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b)
}
