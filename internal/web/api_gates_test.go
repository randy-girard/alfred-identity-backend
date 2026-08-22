package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alfred-identity/web/internal/store"
)

func TestCanAccessWeb(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	if !s.canAccessWeb(ctx, store.User{DiscordID: "bootstrap-1"}) {
		t.Fatal("bootstrap should access")
	}
	if s.canAccessWeb(ctx, store.User{DiscordID: "nobody"}) {
		t.Fatal("nobody should be denied")
	}
	s.accessRoleID = "role-access"
	if !s.canAccessWeb(ctx, store.User{DiscordID: "x", RoleIDs: []string{"role-access"}}) {
		t.Fatal("legacy access role")
	}
}

func TestHandleMe(t *testing.T) {
	s := testServer(t)
	req := httptest.NewRequest(http.MethodGet, BasePath+"/api/me", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxUserKey{}, store.User{
		ID: 7, DiscordID: "d7", DisplayName: "Seven", RoleIDs: []string{"r1"},
	}))
	req = req.WithContext(context.WithValue(req.Context(), ctxWebRoleKey{}, webRoleReadonly))
	rr := httptest.NewRecorder()
	s.handleMe(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["discord_id"] != "d7" || body["is_admin"] != false || body["web_role"] != webRoleReadonly {
		t.Fatalf("%v", body)
	}

	rr = httptest.NewRecorder()
	s.handleMe(rr, httptest.NewRequest(http.MethodPost, BasePath+"/api/me", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST %d", rr.Code)
	}
}

func TestAPISubresourceGates(t *testing.T) {
	s := testServer(t)

	rr := httptest.NewRecorder()
	s.handleAccountSub(rr, httptest.NewRequest(http.MethodGet, BasePath+"/api/accounts/", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("empty account: %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	s.handleAccountSub(rr, httptest.NewRequest(http.MethodGet, BasePath+"/api/accounts/nope", nil))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "invalid_account") {
		t.Fatalf("bad id: %d %s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	s.handleAccountSub(rr, httptest.NewRequest(http.MethodGet, BasePath+"/api/accounts/0/shares", nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("zero id: %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	s.handleAccountSub(rr, httptest.NewRequest(http.MethodGet, BasePath+"/api/accounts/3/shares", nil))
	if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "shares_managed_in_gui") {
		t.Fatalf("shares: %d %s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	s.handleGroupSub(rr, httptest.NewRequest(http.MethodGet, BasePath+"/api/groups/", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("empty group: %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	s.handleGroupSub(rr, httptest.NewRequest(http.MethodGet, BasePath+"/api/groups/abc", nil))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "invalid_group") {
		t.Fatalf("bad group: %d %s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	s.handleUsers(rr, httptest.NewRequest(http.MethodPatch, BasePath+"/api/users/1", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("short users path: %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	s.handleUsers(rr, httptest.NewRequest(http.MethodPatch, BasePath+"/api/users/x/access", nil))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "invalid_user") {
		t.Fatalf("bad user: %d %s", rr.Code, rr.Body.String())
	}
}

func TestSpaHandlerRedirectsUnauthed(t *testing.T) {
	s := testServer(t)
	h := s.spaHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not serve files without session")
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, BasePath+"/", nil))
	if rr.Code != http.StatusFound {
		t.Fatalf("status %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != BasePath+"/login" {
		t.Fatalf("location %q", loc)
	}
}

func TestHostFromPublicURLEdges(t *testing.T) {
	if HostFromPublicURL("http://") != "" {
		t.Fatal("empty host")
	}
	if HostFromPublicURL("  https://[::1]:8181  ") != "[::1]:8181" {
		t.Fatalf("%q", HostFromPublicURL("  https://[::1]:8181  "))
	}
	if got := defaultSourceName("guild.example.com:443"); got != "guild.example.com" {
		t.Fatalf("%q", got)
	}
	if got := defaultSourceName(":"); got != "Local daemon" {
		t.Fatalf("%q", got)
	}
}
