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
)

func TestHandleAccountsCRUDAndLists(t *testing.T) {
	st := openTestStoreForWeb(t)
	ctx := context.Background()
	admin, err := st.UpsertUser(ctx, "api-admin-"+testRandHex(4), "Admin", nil)
	if err != nil {
		t.Fatal(err)
	}

	s := New(Options{
		Store:             st,
		SessionKey:        []byte("test-session-key-32-bytes-long!!"),
		PublicURL:         "http://127.0.0.1:8181",
		BootstrapAdminIDs: []string{admin.DiscordID},
	})

	reqWith := func(method, path string, body []byte) *http.Request {
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), ctxUserKey{}, admin))
		req = req.WithContext(context.WithValue(req.Context(), ctxWebRoleKey{}, webRoleAdmin))
		return req
	}

	uname := "webapi_" + testRandHex(5)
	body, _ := json.Marshal(map[string]any{"username": uname, "password": "secret"})
	rr := httptest.NewRecorder()
	s.handleAccounts(rr, reqWith(http.MethodPost, BasePath+"/api/accounts", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	acctID := int64(created["account_id"].(float64))

	rr = httptest.NewRecorder()
	s.handleAccounts(rr, reqWith(http.MethodGet, BasePath+"/api/accounts", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("get accounts: %d", rr.Code)
	}

	patch, _ := json.Marshal(map[string]any{"disabled": true, "password": "newpass"})
	rr = httptest.NewRecorder()
	s.handleAccountSub(rr, reqWith(http.MethodPatch, BasePath+"/api/accounts/"+strconv.FormatInt(acctID, 10), patch))
	if rr.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rr.Code, rr.Body.String())
	}

	aliasBody, _ := json.Marshal(map[string]string{"alias": "apialias_" + testRandHex(3)})
	rr = httptest.NewRecorder()
	s.handleAccountSub(rr, reqWith(http.MethodPost, BasePath+"/api/accounts/"+strconv.FormatInt(acctID, 10)+"/aliases", aliasBody))
	if rr.Code != http.StatusOK {
		t.Fatalf("alias: %d %s", rr.Code, rr.Body.String())
	}

	tagBody, _ := json.Marshal(map[string]string{"tag": "apitag"})
	rr = httptest.NewRecorder()
	s.handleAccountSub(rr, reqWith(http.MethodPost, BasePath+"/api/accounts/"+strconv.FormatInt(acctID, 10)+"/tags", tagBody))
	if rr.Code != http.StatusOK {
		t.Fatalf("tag: %d %s", rr.Code, rr.Body.String())
	}

	charBody, _ := json.Marshal(map[string]string{"name": "ApiChar" + testRandHex(2)})
	rr = httptest.NewRecorder()
	s.handleAccountSub(rr, reqWith(http.MethodPost, BasePath+"/api/accounts/"+strconv.FormatInt(acctID, 10)+"/characters", charBody))
	if rr.Code != http.StatusOK {
		t.Fatalf("char: %d %s", rr.Code, rr.Body.String())
	}

	meta, err := st.LoadEQAccountMeta(ctx, acctID)
	if err != nil || !meta.Disabled || len(meta.Aliases) == 0 || len(meta.Tags) == 0 || len(meta.Characters) == 0 {
		t.Fatalf("meta=%#v err=%v", meta, err)
	}

	rr = httptest.NewRecorder()
	s.handleAccountSub(rr, reqWith(http.MethodDelete, BasePath+"/api/accounts/"+strconv.FormatInt(acctID, 10), nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rr.Code, rr.Body.String())
	}
}

func TestHandleAccountsImportEndpoint(t *testing.T) {
	st := openTestStoreForWeb(t)
	ctx := context.Background()
	admin, err := st.UpsertUser(ctx, "imp-admin-"+testRandHex(4), "Admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{
		Store:             st,
		SessionKey:        []byte("test-session-key-32-bytes-long!!"),
		PublicURL:         "http://127.0.0.1:8181",
		BootstrapAdminIDs: []string{admin.DiscordID},
	})

	csv := "username,password\nimp_" + testRandHex(4) + ",pw\n"
	req := httptest.NewRequest(http.MethodPost, BasePath+"/api/accounts/import", strings.NewReader(csv))
	req = req.WithContext(context.WithValue(req.Context(), ctxUserKey{}, admin))
	req = req.WithContext(context.WithValue(req.Context(), ctxWebRoleKey{}, webRoleAdmin))
	rr := httptest.NewRecorder()
	s.handleAccountsImport(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
}
