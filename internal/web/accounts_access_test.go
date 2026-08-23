package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/alfred-identity/web/internal/store"
)

func TestWebRestrictedShareAccess(t *testing.T) {
	st := openTestStoreForWeb(t)
	ctx := context.Background()

	owner, err := st.UpsertUser(ctx, "owner-web-"+testRandHex(4), "Owner", nil)
	if err != nil {
		t.Fatal(err)
	}
	friend, err := st.UpsertUser(ctx, "friend-web-"+testRandHex(4), "Friend", nil)
	if err != nil {
		t.Fatal(err)
	}
	stranger, err := st.UpsertUser(ctx, "stranger-web-"+testRandHex(4), "Stranger", nil)
	if err != nil {
		t.Fatal(err)
	}

	shareID, _, err := st.ShareLocalAccount(ctx, owner, "webshare_"+testRandHex(6), "secret", nil, []int64{friend.ID}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	openName := "webopen_" + testRandHex(6)
	openID, err := st.AddEQAccount(ctx, openName, "open-pass", "")
	if err != nil {
		t.Fatal(err)
	}
	limitedName := "weblimited_" + testRandHex(6)
	limitedID, err := st.AddEQAccount(ctx, limitedName, "lim-pass", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetAccountAccess(ctx, limitedID, nil, []int64{friend.ID}, nil); err != nil {
		t.Fatal(err)
	}

	s := New(Options{
		Store:             st,
		SessionKey:        []byte("test-session-key-32-bytes-long!!"),
		PublicURL:         "http://127.0.0.1:8181",
		BootstrapAdminIDs: []string{stranger.DiscordID},
	})

	reqWithUser := func(method, path string, u store.User, body []byte) *http.Request {
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), ctxUserKey{}, u))
		req = req.WithContext(context.WithValue(req.Context(), ctxWebRoleKey{}, webRoleAdmin))
		return req
	}

	// Stranger (admin) state: sees share on shares tab but not on accounts (no SSO access).
	// Open account (no grants) is visible; limited account is not (no user grant).
	rr := httptest.NewRecorder()
	s.handleState(rr, reqWithUser(http.MethodGet, BasePath+"/api/state", stranger, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("state: %d %s", rr.Code, rr.Body.String())
	}
	var stBody map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &stBody); err != nil {
		t.Fatal(err)
	}
	accountIDs := map[int64]bool{}
	for _, a := range stBody["accounts"].([]any) {
		m := a.(map[string]any)
		accountIDs[int64(m["id"].(float64))] = true
	}
	if accountIDs[shareID] {
		t.Fatal("stranger should not see restricted share on accounts without access")
	}
	if !accountIDs[openID] {
		t.Fatal("stranger should see open account with no grants")
	}
	if !accountIDs[limitedID] {
		t.Fatal("stranger admin should see limited account for web management")
	}
	shares := stBody["shares"].([]any)
	foundShare := false
	for _, raw := range shares {
		m := raw.(map[string]any)
		if int64(m["id"].(float64)) == shareID {
			foundShare = true
			break
		}
	}
	if !foundShare {
		t.Fatalf("admin shares missing id=%d (count=%d)", shareID, len(shares))
	}

	// Friend sees share on accounts but cannot PATCH access grants.
	rr = httptest.NewRecorder()
	patchBody, _ := json.Marshal(map[string]any{"required_user_ids": []int64{friend.ID}})
	s.handleAccountSub(rr, reqWithUser(http.MethodPatch, BasePath+"/api/accounts/"+strconv.FormatInt(shareID, 10), friend, patchBody))
	if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "share_access_managed_in_gui") {
		t.Fatalf("friend access patch: %d %s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	patchBody, _ = json.Marshal(map[string]any{"disabled": true})
	s.handleAccountSub(rr, reqWithUser(http.MethodPatch, BasePath+"/api/accounts/"+strconv.FormatInt(shareID, 10), friend, patchBody))
	if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "share_not_owner") {
		t.Fatalf("friend disabled patch: %d %s", rr.Code, rr.Body.String())
	}

	// Owner cannot PATCH access grants or password on web (desktop GUI only).
	rr = httptest.NewRecorder()
	patchBody, _ = json.Marshal(map[string]any{"required_user_ids": []int64{friend.ID}})
	s.handleAccountSub(rr, reqWithUser(http.MethodPatch, BasePath+"/api/accounts/"+strconv.FormatInt(shareID, 10), owner, patchBody))
	if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "share_access_managed_in_gui") {
		t.Fatalf("owner access patch: %d %s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	patchBody, _ = json.Marshal(map[string]any{"password": "new-pass"})
	s.handleAccountSub(rr, reqWithUser(http.MethodPatch, BasePath+"/api/accounts/"+strconv.FormatInt(shareID, 10), owner, patchBody))
	if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "share_password_managed_in_gui") {
		t.Fatalf("owner password patch: %d %s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	patchBody, _ = json.Marshal(map[string]any{"password": "new-pass"})
	s.handleAccountSub(rr, reqWithUser(http.MethodPatch, BasePath+"/api/accounts/"+strconv.FormatInt(shareID, 10), friend, patchBody))
	if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "share_password_managed_in_gui") {
		t.Fatalf("friend password patch: %d %s", rr.Code, rr.Body.String())
	}

	// Friend state includes share and limited account; open account visible to all SSO users.
	rr = httptest.NewRecorder()
	s.handleState(rr, reqWithUser(http.MethodGet, BasePath+"/api/state", friend, nil))
	if err := json.Unmarshal(rr.Body.Bytes(), &stBody); err != nil {
		t.Fatal(err)
	}
	accountIDs = map[int64]bool{}
	for _, a := range stBody["accounts"].([]any) {
		m := a.(map[string]any)
		accountIDs[int64(m["id"].(float64))] = true
	}
	if !accountIDs[shareID] {
		t.Fatal("friend should see restricted share on accounts")
	}
	if !accountIDs[openID] {
		t.Fatal("friend should see open account with no grants")
	}
	if !accountIDs[limitedID] {
		t.Fatal("friend should see user-limited account they are granted")
	}
}

func testRandHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
