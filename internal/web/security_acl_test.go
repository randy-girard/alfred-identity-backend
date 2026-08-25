package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alfred-identity/web/internal/presence"
)

func TestReadonlyStateOmitsAdminFields(t *testing.T) {
	st := openTestStoreForWeb(t)
	ctx := context.Background()
	admin, err := st.UpsertUser(ctx, "ro-admin-"+testRandHex(4), "Admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := st.UpsertUser(ctx, "ro-view-"+testRandHex(4), "Viewer", []string{"legacy-web"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddEQAccount(ctx, "roacct_"+testRandHex(5), "pw", ""); err != nil {
		t.Fatal(err)
	}
	pres := presence.New(time.Minute)
	pres.Touch(999001, "HiddenChar", admin.ID)

	s := New(Options{
		Store:             st,
		Presence:          pres,
		SessionKey:        []byte("test-session-key-32-bytes-long!!"),
		PublicURL:         "http://127.0.0.1:8181",
		BootstrapAdminIDs: []string{admin.DiscordID},
		AccessRoleID:      "legacy-web",
		AdminRoleID:       "discord-admin-role",
	})

	req := httptest.NewRequest(http.MethodGet, BasePath+"/api/state", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxUserKey{}, viewer))
	req = req.WithContext(context.WithValue(req.Context(), ctxWebRoleKey{}, webRoleReadonly))
	rr := httptest.NewRecorder()
	s.handleState(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("state: %d %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if users, _ := body["users"].([]any); len(users) != 0 {
		t.Fatalf("readonly must not see users: %#v", users)
	}
	if roles, _ := body["roles"].([]any); len(roles) != 0 {
		t.Fatalf("readonly must not see roles: %#v", roles)
	}
	if conns, _ := body["connections"].([]any); len(conns) != 0 {
		t.Fatalf("readonly must not see connections: %#v", conns)
	}
	if groups, ok := body["groups"].([]any); !ok || len(groups) != 0 {
		t.Fatalf("readonly must not see groups: %#v", body["groups"])
	}
	for _, raw := range body["online"].([]any) {
		m := raw.(map[string]any)
		if int64(m["account_id"].(float64)) == 999001 {
			t.Fatal("readonly must not see presence for inaccessible accounts")
		}
	}

	mux := http.NewServeMux()
	s.Mount(mux)
	cookieRR := httptest.NewRecorder()
	if err := s.setSessionCookie(cookieRR, Session{
		UserID: viewer.ID, DiscordID: viewer.DiscordID, DisplayName: viewer.DisplayName,
		RoleIDs: viewer.RoleIDs, ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	cookies := cookieRR.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected session cookie")
	}
	auditReq := httptest.NewRequest(http.MethodGet, BasePath+"/api/audit", nil)
	auditReq.AddCookie(cookies[0])
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, auditReq)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("readonly audit via mount: %d %s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	s.handleState(rr, adminReq(admin, http.MethodGet, BasePath+"/api/state", nil))
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if users, _ := body["users"].([]any); len(users) == 0 {
		t.Fatal("admin should see users")
	}
}

func TestWebSetUserRolesForbidden(t *testing.T) {
	st := openTestStoreForWeb(t)
	ctx := context.Background()
	admin, err := st.UpsertUser(ctx, "role-admin-"+testRandHex(4), "Admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	target, err := st.UpsertUser(ctx, "role-target-"+testRandHex(4), "Target", []string{"guild-role"})
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{
		Store:             st,
		SessionKey:        []byte("test-session-key-32-bytes-long!!"),
		PublicURL:         "http://127.0.0.1:8181",
		BootstrapAdminIDs: []string{admin.DiscordID},
		AdminRoleID:       "discord-admin-role",
	})
	body, _ := json.Marshal(map[string]any{"role_ids": []string{"discord-admin-role"}})
	rr := httptest.NewRecorder()
	s.handleUsers(rr, adminReq(admin, http.MethodPut, BasePath+"/api/users/"+strconv.FormatInt(target.ID, 10)+"/roles", body))
	if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "roles_managed_by_discord") {
		t.Fatalf("set roles: %d %s", rr.Code, rr.Body.String())
	}
	u, err := st.UserByID(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range u.RoleIDs {
		if r == "discord-admin-role" {
			t.Fatal("roles must not change")
		}
	}
}

func TestRequireAccountACLRejectsEmptyGrants(t *testing.T) {
	st := openTestStoreForWeb(t)
	ctx := context.Background()
	admin, err := st.UpsertUser(ctx, "acl-"+testRandHex(4), "Admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.AddEQAccount(ctx, "aclacct_"+testRandHex(5), "pw", "")
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{
		Store:             st,
		SessionKey:        []byte("test-session-key-32-bytes-long!!"),
		PublicURL:         "http://127.0.0.1:8181",
		BootstrapAdminIDs: []string{admin.DiscordID},
		RequireAccountACL: true,
	})
	body, _ := json.Marshal(map[string]any{"access": "all"})
	rr := httptest.NewRecorder()
	s.handleAccountSub(rr, adminReq(admin, http.MethodPatch, BasePath+"/api/accounts/"+strconv.FormatInt(id, 10), body))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "account_acl_required") {
		t.Fatalf("empty ACL: %d %s", rr.Code, rr.Body.String())
	}
}

