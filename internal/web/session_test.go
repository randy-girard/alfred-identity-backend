package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alfred-identity/web/internal/store"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	return New(Options{
		SessionKey:        []byte("test-session-key-32-bytes-long!!"),
		PublicURL:         "http://127.0.0.1:8181",
		AccessRoleID:      "role-access",
		BootstrapAdminIDs: []string{"bootstrap-1"},
	})
}

func TestOAuthStateRoundTrip(t *testing.T) {
	s := testServer(t)
	state, err := s.makeOAuthState()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.verifyOAuthState(state); err != nil {
		t.Fatal(err)
	}
	if err := s.verifyOAuthState(state + "x"); err == nil {
		t.Fatal("expected bad signature")
	}
	if err := s.verifyOAuthState("not.valid"); err == nil {
		t.Fatal("expected invalid format")
	}
}

func TestSessionSignParse(t *testing.T) {
	s := testServer(t)
	sess := Session{
		UserID:      9,
		DiscordID:   "d1",
		DisplayName: "Ada",
		RoleIDs:     []string{"r1"},
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}
	tok, err := s.signSession(sess)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.parseSession(tok)
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != 9 || got.DiscordID != "d1" || got.DisplayName != "Ada" {
		t.Fatalf("got=%+v", got)
	}
	if _, err := s.parseSession("tampered." + tok); err == nil {
		t.Fatal("expected bad signature")
	}
	expired := sess
	expired.ExpiresAt = time.Now().Add(-time.Minute).Unix()
	tok2, err := s.signSession(expired)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.parseSession(tok2); err == nil {
		t.Fatal("expected expired")
	}
}

func TestSessionCookieRoundTrip(t *testing.T) {
	s := testServer(t)
	rr := httptest.NewRecorder()
	if err := s.setSessionCookie(rr, Session{
		UserID: 1, DiscordID: "x", DisplayName: "y",
	}); err != nil {
		t.Fatal(err)
	}
	res := rr.Result()
	cookies := res.Cookies()
	if len(cookies) == 0 {
		t.Fatal("no cookie")
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookies[0])
	got, err := s.sessionFromRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != 1 {
		t.Fatalf("got=%+v", got)
	}
	s.clearSessionCookie(rr)
}

func TestSessionCookieSecureFlag(t *testing.T) {
	httpsSrv := New(Options{
		SessionKey: []byte("test-session-key-32-bytes-long!!"),
		PublicURL:  "https://identity.example.com",
	})
	rr := httptest.NewRecorder()
	if err := httpsSrv.setSessionCookie(rr, Session{UserID: 1, ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	c := rr.Result().Cookies()[0]
	if !c.Secure || !c.HttpOnly || c.Name != sessionCookie {
		t.Fatalf("%+v", c)
	}

	httpSrv := testServer(t)
	rr = httptest.NewRecorder()
	if err := httpSrv.setSessionCookie(rr, Session{UserID: 1, ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	if rr.Result().Cookies()[0].Secure {
		t.Fatal("http origin should not set Secure")
	}
}

func TestHandleLogout(t *testing.T) {
	s := testServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, BasePath+"/logout", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != BasePath+"/login" {
		t.Fatalf("location %q", loc)
	}
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && (c.MaxAge < 0 || c.Value == "") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected cleared session cookie")
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, BasePath+"/logout", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405 got %d", rec.Code)
	}
}

func TestWebAccessLevel(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	if got := s.webAccessLevel(ctx, store.User{DiscordID: "bootstrap-1"}); got != webRoleAdmin {
		t.Fatalf("bootstrap: got %q", got)
	}
	// Legacy access role (no admin role configured) → readonly
	if got := s.webAccessLevel(ctx, store.User{DiscordID: "other", RoleIDs: []string{"role-access"}}); got != webRoleReadonly {
		t.Fatalf("access role: got %q", got)
	}
	if got := s.webAccessLevel(ctx, store.User{DiscordID: "other", RoleIDs: []string{"nope"}}); got != webRoleNone {
		t.Fatalf("deny: got %q", got)
	}
	s.accessRoleID = ""
	if got := s.webAccessLevel(ctx, store.User{DiscordID: "other", RoleIDs: []string{"role-access"}}); got != webRoleNone {
		t.Fatalf("empty access role: got %q", got)
	}
	s.adminRoleID = "role-admin"
	if got := s.webAccessLevel(ctx, store.User{DiscordID: "x", RoleIDs: []string{"role-admin"}}); got != webRoleAdmin {
		t.Fatalf("admin role: got %q", got)
	}
}

func TestRedirectURI(t *testing.T) {
	s := testServer(t)
	want := "http://127.0.0.1:8181/admin/oauth/callback"
	if got := s.redirectURI(); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
