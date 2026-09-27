package web

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/alfred-identity/web/internal/config"
)

// AppURLScheme is the desktop app protocol Discord's HTTPS landing page opens.
const AppURLScheme = "alfred-identity"

const discordLinkButtonMaxURL = 512

// SourceImportJSON is pasted into alfred-identity → Connections → Add from JSON.
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

// BuildSourceImportJSON returns indented JSON for paste into alfred-identity.
func BuildSourceImportJSON(name, host, token, notes string) string {
	if notes == "" {
		notes = "Click Open in " + DesktopAppName + ", or paste into Connections → Add from JSON."
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

// EncodeSourceDeepLinkPayload is compact JSON as raw-URL base64 for ?d=.
func EncodeSourceDeepLinkPayload(name, host, token string) (string, error) {
	name = strings.TrimSpace(name)
	host = strings.TrimSpace(host)
	token = strings.TrimSpace(token)
	if name == "" || host == "" || token == "" {
		return "", fmt.Errorf("name, host, and token required")
	}
	b, err := json.Marshal(map[string]string{
		"name":  name,
		"host":  host,
		"token": token,
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// EncodeAppSchemeURL builds alfred-identity://import?d=...
func EncodeAppSchemeURL(name, host, token string) (string, error) {
	d, err := EncodeSourceDeepLinkPayload(name, host, token)
	if err != nil {
		return "", err
	}
	return AppURLScheme + "://import?d=" + d, nil
}

// OpenAlfredURL is the HTTPS landing page Discord can linkify and put on a button.
// Empty when WEB_PUBLIC_URL is unset.
func OpenAlfredURL(publicURL, name, host, token string) string {
	origin := strings.TrimRight(strings.TrimSpace(publicURL), "/")
	if origin == "" {
		return ""
	}
	d, err := EncodeSourceDeepLinkPayload(name, host, token)
	if err != nil {
		return ""
	}
	return origin + openAlfredPath + "?d=" + url.QueryEscape(d)
}

// DiscordOpenAlfredButtonURL returns the landing URL only if it fits a Discord link button.
func DiscordOpenAlfredButtonURL(publicURL, name, host, token string) string {
	u := OpenAlfredURL(publicURL, name, host, token)
	if u == "" || len(u) > discordLinkButtonMaxURL {
		return ""
	}
	return u
}

// ParseSourceDeepLinkPayload decodes ?d= from /open-alfred or alfred-identity://import.
func ParseSourceDeepLinkPayload(d string) (SourceImportJSON, error) {
	d = strings.TrimSpace(d)
	if d == "" {
		return SourceImportJSON{}, fmt.Errorf("empty payload")
	}
	b, err := base64.RawURLEncoding.DecodeString(d)
	if err != nil {
		b, err = base64.URLEncoding.DecodeString(d)
	}
	if err != nil {
		return SourceImportJSON{}, fmt.Errorf("invalid source payload")
	}
	var obj SourceImportJSON
	if err := json.Unmarshal(b, &obj); err != nil {
		return SourceImportJSON{}, fmt.Errorf("invalid source JSON")
	}
	obj.Name = strings.TrimSpace(obj.Name)
	obj.Host = strings.TrimSpace(obj.Host)
	obj.Token = strings.TrimSpace(obj.Token)
	if obj.Name == "" || obj.Host == "" || obj.Token == "" {
		return SourceImportJSON{}, fmt.Errorf("name, host, and token required")
	}
	return obj, nil
}
