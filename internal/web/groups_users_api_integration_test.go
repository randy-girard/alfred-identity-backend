package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/alfred-identity/web/internal/store"
)

func adminReq(admin store.User, method, path string, body []byte) *http.Request {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), ctxUserKey{}, admin))
	req = req.WithContext(context.WithValue(req.Context(), ctxWebRoleKey{}, webRoleAdmin))
	return req
}

func TestHandleGroupsAndUsersAPI(t *testing.T) {
	st := openTestStoreForWeb(t)
	ctx := context.Background()
	admin, err := st.UpsertUser(ctx, "gadmin-"+testRandHex(4), "Admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	target, err := st.UpsertUser(ctx, "gtarget-"+testRandHex(4), "Target", []string{"guild-role"})
	if err != nil {
		t.Fatal(err)
	}

	s := New(Options{
		Store:             st,
		SessionKey:        []byte("test-session-key-32-bytes-long!!"),
		PublicURL:         "http://127.0.0.1:8181",
		BootstrapAdminIDs: []string{admin.DiscordID},
	})

	gbody, _ := json.Marshal(map[string]any{
		"name":             "grp-" + testRandHex(4),
		"description":      "desc",
		"web_role":         "readonly",
		"discord_commands": []string{"sso"},
		"user_ids":         []int64{target.ID},
		"role_ids":         []string{"guild-role"},
	})
	rr := httptest.NewRecorder()
	s.handleGroups(rr, adminReq(admin, http.MethodPost, BasePath+"/api/groups", gbody))
	if rr.Code != http.StatusOK {
		t.Fatalf("create group: %d %s", rr.Code, rr.Body.String())
	}
	var gresp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &gresp); err != nil {
		t.Fatal(err)
	}
	gid := int64(gresp["id"].(float64))

	patch, _ := json.Marshal(map[string]any{"description": "updated"})
	rr = httptest.NewRecorder()
	s.handleGroupSub(rr, adminReq(admin, http.MethodPatch, BasePath+"/api/groups/"+strconv.FormatInt(gid, 10), patch))
	if rr.Code != http.StatusOK {
		t.Fatalf("patch group: %d %s", rr.Code, rr.Body.String())
	}

	rolesBody, _ := json.Marshal(map[string]any{"role_ids": []string{"guild-role", "extra-role"}})
	rr = httptest.NewRecorder()
	s.handleUsers(rr, adminReq(admin, http.MethodPut, BasePath+"/api/users/"+strconv.FormatInt(target.ID, 10)+"/roles", rolesBody))
	if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "roles_managed_by_discord") {
		t.Fatalf("set roles: %d %s", rr.Code, rr.Body.String())
	}

	accessBody, _ := json.Marshal(map[string]any{"revoked": true})
	rr = httptest.NewRecorder()
	s.handleUsers(rr, adminReq(admin, http.MethodPatch, BasePath+"/api/users/"+strconv.FormatInt(target.ID, 10)+"/access", accessBody))
	if rr.Code != http.StatusOK {
		t.Fatalf("revoke access: %d %s", rr.Code, rr.Body.String())
	}
	u, err := st.UserByID(ctx, target.ID)
	if err != nil || !u.AccessRevoked {
		t.Fatalf("revoked=%v err=%v", u.AccessRevoked, err)
	}

	rr = httptest.NewRecorder()
	s.handleUsers(rr, adminReq(admin, http.MethodPatch, BasePath+"/api/users/"+strconv.FormatInt(admin.ID, 10)+"/access", accessBody))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("self-revoke: %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	s.handleGroupSub(rr, adminReq(admin, http.MethodDelete, BasePath+"/api/groups/"+strconv.FormatInt(gid, 10), nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("delete group: %d %s", rr.Code, rr.Body.String())
	}
}

func TestHandleMeStateAndAudit(t *testing.T) {
	st := openTestStoreForWeb(t)
	ctx := context.Background()
	admin, err := st.UpsertUser(ctx, "me-"+testRandHex(4), "Admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	acct, err := st.AddEQAccount(ctx, "auditacct_"+testRandHex(5), "pw", "")
	if err != nil {
		t.Fatal(err)
	}
	st.AuditAccount(ctx, admin.ID, acct, "web_test", "detail")

	s := New(Options{
		Store:             st,
		SessionKey:        []byte("test-session-key-32-bytes-long!!"),
		PublicURL:         "http://127.0.0.1:8181",
		BootstrapAdminIDs: []string{admin.DiscordID},
	})

	rr := httptest.NewRecorder()
	s.handleMe(rr, adminReq(admin, http.MethodGet, BasePath+"/api/me", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("me: %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	s.handleState(rr, adminReq(admin, http.MethodGet, BasePath+"/api/state", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("state: %d %s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	s.handleAudit(rr, adminReq(admin, http.MethodGet, BasePath+"/api/audit?account_id="+strconv.FormatInt(acct, 10), nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("audit: %d %s", rr.Code, rr.Body.String())
	}
	var auditBody map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &auditBody); err != nil {
		t.Fatal(err)
	}
	entries, _ := auditBody["entries"].([]any)
	if len(entries) == 0 {
		t.Fatal("expected audit entries")
	}
}

func TestHandleSettingsBackupImportEndpoint(t *testing.T) {
	st := openTestStoreForWeb(t)
	admin, _ := st.UpsertUser(context.Background(), "set-"+testRandHex(4), "Admin", nil)
	s := New(Options{
		Store:             st,
		SessionKey:        []byte("test-session-key-32-bytes-long!!"),
		PublicURL:         "http://127.0.0.1:8181",
		BootstrapAdminIDs: []string{admin.DiscordID},
	})
	body := `{"version":1,"users":[],"groups":[],"accounts":[]}`
	rr := httptest.NewRecorder()
	s.handleSettingsBackup(rr, adminReq(admin, http.MethodPost, BasePath+"/api/settings/backup", []byte(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("import backup: %d %s", rr.Code, rr.Body.String())
	}
}
