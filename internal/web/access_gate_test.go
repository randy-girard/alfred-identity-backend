package web

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alfred-identity/web/internal/store"
)

func TestRejectIfReadonly(t *testing.T) {
	s := testServer(t)
	s.bootstrapIDs = []string{"boot"}

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxWebRoleKey{}, webRoleAdmin))
	rr := httptest.NewRecorder()
	if s.rejectIfReadonly(rr, req) {
		t.Fatal("admin should not reject")
	}

	req = httptest.NewRequest(http.MethodPost, "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxWebRoleKey{}, webRoleReadonly))
	rr = httptest.NewRecorder()
	if !s.rejectIfReadonly(rr, req) || rr.Code != http.StatusForbidden {
		t.Fatalf("readonly: code=%d rejected=%v", rr.Code, true)
	}

	// No web role on context; bootstrap user → allowed via isWebAdmin fallback
	req = httptest.NewRequest(http.MethodPost, "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxUserKey{}, store.User{DiscordID: "boot"}))
	rr = httptest.NewRecorder()
	if s.rejectIfReadonly(rr, req) {
		t.Fatal("bootstrap admin should write")
	}
}

func TestSSOSourceHEADAndErrors(t *testing.T) {
	s := &Server{publicURL: "http://127.0.0.1:8181", sourceName: "Guild"}
	mux := http.NewServeMux()
	s.Mount(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, SSOSourcePath, nil))
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("HEAD: %d body=%q", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, SSOSourcePath, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", rec.Code)
	}

	s2 := &Server{publicURL: ""}
	mux2 := http.NewServeMux()
	s2.Mount(mux2)
	rec = httptest.NewRecorder()
	mux2.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, SSOSourcePath, nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("empty publicURL: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSpaHandlerReservedPaths(t *testing.T) {
	s := testServer(t)
	h := s.spaHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("files should not be served for reserved paths")
	}))
	for _, path := range []string{
		BasePath + "/api/state",
		BasePath + "/login",
		BasePath + "/logout",
		BasePath + "/denied",
		BasePath + "/oauth/callback",
		BasePath + "/ws",
	} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("%s → %d", path, rr.Code)
		}
	}
}

func TestVerifyOAuthStateExpiry(t *testing.T) {
	s := testServer(t)
	sign := func(ts int64) string {
		nonce := "abc"
		payload := fmt.Sprintf("%s.%d", nonce, ts)
		mac := hmac.New(sha256.New, s.sessionKey)
		_, _ = mac.Write([]byte(payload))
		sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
		return payload + "." + sig
	}
	if err := s.verifyOAuthState(sign(time.Now().Add(-20 * time.Minute).Unix())); err == nil {
		t.Fatal("expected expired")
	}
	if err := s.verifyOAuthState("nonce.notanumber.sig"); err == nil {
		t.Fatal("expected bad timestamp")
	}
	if err := s.verifyOAuthState(sign(0)); err == nil {
		t.Fatal("expected bad timestamp for 0")
	}
	if err := s.verifyOAuthState(sign(time.Now().Add(2 * time.Minute).Unix())); err == nil {
		t.Fatal("expected expired for future skew")
	}
}

func TestOAuthLoginAndCallbackGates(t *testing.T) {
	s := New(Options{
		SessionKey:   []byte("test-session-key-32-bytes-long!!"),
		PublicURL:    "http://127.0.0.1:8181",
		ClientID:     "cid",
		ClientSecret: "sec",
	})
	mux := http.NewServeMux()
	s.Mount(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, BasePath+"/login", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("login status %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc == "" || !containsAll(loc, "client_id=cid", "redirect_uri=", "state=") {
		t.Fatalf("location %q", loc)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, BasePath+"/login", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("login POST %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, BasePath+"/oauth/callback?error=access_denied", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("oauth error %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, BasePath+"/oauth/callback", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing code %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, BasePath+"/oauth/callback?code=x&state=bad", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad state %d", rec.Code)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}
