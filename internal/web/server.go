package web

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alfred-identity/web/internal/presence"
	"github.com/alfred-identity/web/internal/sso"
	"github.com/alfred-identity/web/internal/store"
	"github.com/coder/websocket"
)

// BasePath is the URL prefix for the browser admin UI (OAuth, API, static assets).
const BasePath = "/admin"

//go:embed static/*
var staticFS embed.FS

type Server struct {
	store        *store.Store
	hub          *sso.Hub
	presence     *presence.Tracker
	log          *slog.Logger
	sessionKey   []byte
	publicURL    string
	clientID     string
	clientSecret string
	guildID      string
	accessRoleID string
	bootstrapIDs []string
	adminRoleID  string
	sourceName   string

	liveMu sync.Mutex
	live   map[*websocket.Conn]store.User
}

type Options struct {
	Store             *store.Store
	Hub               *sso.Hub
	Presence          *presence.Tracker
	Log               *slog.Logger
	SessionKey        []byte
	PublicURL         string
	ClientID          string
	ClientSecret      string
	GuildID           string
	AccessRoleID      string
	BootstrapAdminIDs []string
	AdminRoleID       string
	SSOSourceName     string
}

func New(opts Options) *Server {
	key := sha256.Sum256(opts.SessionKey)
	s := &Server{
		store:        opts.Store,
		hub:          opts.Hub,
		presence:     opts.Presence,
		log:          opts.Log,
		sessionKey:   key[:],
		publicURL:    strings.TrimRight(opts.PublicURL, "/"),
		clientID:     opts.ClientID,
		clientSecret: opts.ClientSecret,
		guildID:      opts.GuildID,
		accessRoleID: opts.AccessRoleID,
		bootstrapIDs: append([]string{}, opts.BootstrapAdminIDs...),
		adminRoleID:  opts.AdminRoleID,
		sourceName:   strings.TrimSpace(opts.SSOSourceName),
		live:         make(map[*websocket.Conn]store.User),
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if opts.Hub != nil {
		opts.Hub.OnStateChange(func() {
			s.broadcastLiveState()
		})
	}
	return s
}

func (s *Server) Mount(mux *http.ServeMux) {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))

	mux.HandleFunc(BasePath+"/login", s.handleLogin)
	mux.HandleFunc(BasePath+"/oauth/callback", s.handleOAuthCallback)
	mux.HandleFunc(BasePath+"/logout", s.handleLogout)
	mux.HandleFunc(BasePath+"/denied", s.handleDenied)
	mux.HandleFunc(BasePath+"/ws", s.handleLiveWS)

	mux.HandleFunc(BasePath+"/api/me", s.requireAuth(s.handleMe))
	mux.HandleFunc(BasePath+"/api/state", s.requireAuth(s.handleState))
	mux.HandleFunc(BasePath+"/api/metrics", s.requireAuth(s.handleMetrics))
	mux.HandleFunc(BasePath+"/api/accounts/import", s.requireAdmin(s.handleAccountsImport))
	mux.HandleFunc(BasePath+"/api/accounts/export", s.requireAdmin(s.handleAccountsExport))
	mux.HandleFunc(BasePath+"/api/accounts", s.requireAdmin(s.handleAccounts))
	mux.HandleFunc(BasePath+"/api/accounts/", s.requireAuth(s.handleAccountSub))
	mux.HandleFunc(BasePath+"/api/users/", s.requireAuth(s.handleUsers))
	mux.HandleFunc(BasePath+"/api/groups", s.requireAuth(s.handleGroups))
	mux.HandleFunc(BasePath+"/api/groups/", s.requireAuth(s.handleGroupSub))
	mux.HandleFunc(BasePath+"/api/audit", s.requireAuth(s.handleAudit))
	mux.HandleFunc(BasePath+"/api/settings/backup", s.requireAdmin(s.handleSettingsBackup))

	mux.HandleFunc(BasePath, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, BasePath+"/", http.StatusFound)
	})
	mux.Handle(BasePath+"/", s.spaHandler(fileServer))

	// Legacy /web paths: temporary redirects (avoid cached 301 stripping OAuth query).
	// OAuth callback is handled in place so Discord can still use an old redirect URI.
	mux.HandleFunc("/web/oauth/callback", s.handleOAuthCallback)
	mux.HandleFunc("/web/login", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, BasePath+"/login", http.StatusFound)
	})
	mux.HandleFunc("/web", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, BasePath+"/", http.StatusFound)
	})
	mux.HandleFunc("/web/", func(w http.ResponseWriter, r *http.Request) {
		suffix := strings.TrimPrefix(r.URL.Path, "/web")
		if suffix == "" {
			suffix = "/"
		}
		target := BasePath + suffix
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusFound)
	})
}

func (s *Server) spaHandler(files http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, BasePath)
		if path == "" {
			path = "/"
		}
		// API/auth/ws handled elsewhere; this only serves UI assets.
		if strings.HasPrefix(path, "/api") || path == "/login" || path == "/logout" ||
			path == "/denied" || strings.HasPrefix(path, "/oauth") || path == "/ws" {
			http.NotFound(w, r)
			return
		}
		sess, err := s.sessionFromRequest(r)
		if err != nil {
			http.Redirect(w, r, BasePath+"/login", http.StatusFound)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		u, err := s.store.UserByID(ctx, sess.UserID)
		if err != nil || u.AccessRevoked || !s.canAccessWeb(ctx, u) {
			s.clearSessionCookie(w)
			http.Redirect(w, r, BasePath+"/denied?reason=not_authorized", http.StatusFound)
			return
		}
		// SPA fallback: unknown paths → index.html
		if path != "/" && !strings.Contains(path[1:], ".") {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			files.ServeHTTP(w, r2)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = path
		files.ServeHTTP(w, r2)
	})
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.sessionFromRequest(r)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		u, err := s.store.UserByID(ctx, sess.UserID)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if u.AccessRevoked {
			writeErr(w, http.StatusForbidden, "access_revoked")
			return
		}
		level := s.webAccessLevel(ctx, u)
		if level == webRoleNone {
			writeErr(w, http.StatusForbidden, "forbidden")
			return
		}
		ctx = context.WithValue(r.Context(), ctxUserKey{}, u)
		ctx = context.WithValue(ctx, ctxSessionKey{}, sess)
		ctx = context.WithValue(ctx, ctxWebRoleKey{}, level)
		next(w, r.WithContext(ctx))
	}
}

type ctxUserKey struct{}
type ctxSessionKey struct{}
type ctxWebRoleKey struct{}

func currentUser(r *http.Request) store.User {
	u, _ := r.Context().Value(ctxUserKey{}).(store.User)
	return u
}

func currentWebRole(r *http.Request) string {
	role, _ := r.Context().Value(ctxWebRoleKey{}).(string)
	return role
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": code})
}
