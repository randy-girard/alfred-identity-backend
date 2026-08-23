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
	rr := httptest.NewRecorder()
	s.handleState(rr, reqWithUser(http.MethodGet, BasePath+"/api/state", stranger, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("state: %d %s", rr.Code, rr.Body.String())
	}
	var stBody map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &stBody); err != nil {
		t.Fatal(err)
	}
	for _, a := range stBody["accounts"].([]any) {
		m := a.(map[string]any)
		if int64(m["id"].(float64)) == shareID {
			t.Fatal("stranger should not see restricted share on accounts without access")
		}
	}
	shares := stBody["shares"].([]any)
	if len(shares) != 1 {
		t.Fatalf("admin shares count=%d want 1", len(shares))
	}

	// Friend sees share on accounts but cannot PATCH.
	rr = httptest.NewRecorder()
	patchBody, _ := json.Marshal(map[string]any{"required_user_ids": []int64{friend.ID}})
	s.handleAccountSub(rr, reqWithUser(http.MethodPatch, BasePath+"/api/accounts/"+strconv.FormatInt(shareID, 10), friend, patchBody))
	if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "share_not_owner") {
		t.Fatalf("friend patch: %d %s", rr.Code, rr.Body.String())
	}

	// Owner can PATCH password but not access grants.
	rr = httptest.NewRecorder()
	patchBody, _ = json.Marshal(map[string]any{"required_user_ids": []int64{friend.ID}})
	s.handleAccountSub(rr, reqWithUser(http.MethodPatch, BasePath+"/api/accounts/"+strconv.FormatInt(shareID, 10), owner, patchBody))
	if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "share_access_managed_in_gui") {
		t.Fatalf("owner access patch: %d %s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	patchBody, _ = json.Marshal(map[string]any{"password": "new-pass"})
	s.handleAccountSub(rr, reqWithUser(http.MethodPatch, BasePath+"/api/accounts/"+strconv.FormatInt(shareID, 10), owner, patchBody))
	if rr.Code != http.StatusOK {
		t.Fatalf("owner password patch: %d %s", rr.Code, rr.Body.String())
	}

	// Friend state includes share on accounts.
	rr = httptest.NewRecorder()
	s.handleState(rr, reqWithUser(http.MethodGet, BasePath+"/api/state", friend, nil))
	if err := json.Unmarshal(rr.Body.Bytes(), &stBody); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range stBody["accounts"].([]any) {
		m := a.(map[string]any)
		if int64(m["id"].(float64)) == shareID {
			found = true
		}
	}
	if !found {
		t.Fatal("friend should see restricted share on accounts")
	}
}

func testRandHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
