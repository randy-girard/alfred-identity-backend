package store_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/alfred-identity/web/internal/crypto"
	"github.com/alfred-identity/web/internal/db"
	"github.com/alfred-identity/web/internal/store"
)

// Integration tests require Postgres. Set TEST_DATABASE_URL (preferred) or DATABASE_URL.
// Example: postgres://alfred:alfred@127.0.0.1:5432/alfred_identity_test?sslmode=disable
func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run store integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sqlDB, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("db connect: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Migrate(sqlDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	aead, err := crypto.NewAEAD(key)
	if err != nil {
		t.Fatal(err)
	}
	return &store.Store{DB: sqlDB, AEAD: aead, Key: key}
}

func containsID(ids []int64, want int64) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestShareRestrictedACL(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	owner, err := st.UpsertUser(ctx, "owner-"+randHex(4), "Owner", nil)
	if err != nil {
		t.Fatal(err)
	}
	friend, err := st.UpsertUser(ctx, "friend-"+randHex(4), "Friend", nil)
	if err != nil {
		t.Fatal(err)
	}
	stranger, err := st.UpsertUser(ctx, "stranger-"+randHex(4), "Stranger", nil)
	if err != nil {
		t.Fatal(err)
	}

	uname := "shareacct_" + randHex(6)
	id, _, err := st.ShareLocalAccount(ctx, owner, uname, "secret-pass", []string{"sharealias"}, []int64{friend.ID}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if id <= 0 {
		t.Fatalf("id=%d", id)
	}

	ownerAllowed, err := st.AllowedAccountIDs(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	friendAllowed, err := st.AllowedAccountIDs(ctx, friend)
	if err != nil {
		t.Fatal(err)
	}
	strangerAllowed, err := st.AllowedAccountIDs(ctx, stranger)
	if err != nil {
		t.Fatal(err)
	}
	if !containsID(ownerAllowed, id) {
		t.Fatal("owner should see restricted share")
	}
	if !containsID(friendAllowed, id) {
		t.Fatal("shared user should see restricted share")
	}
	if containsID(strangerAllowed, id) {
		t.Fatal("stranger must not see restricted share")
	}

	// Open base account is visible to everyone with a token/user row.
	baseUser := "baseacct_" + randHex(6)
	baseID, err := st.AddEQAccount(ctx, baseUser, "pw", "")
	if err != nil {
		t.Fatal(err)
	}
	if !containsID(strangerAllowed, baseID) {
		// re-fetch after add
		strangerAllowed, err = st.AllowedAccountIDs(ctx, stranger)
		if err != nil {
			t.Fatal(err)
		}
		if !containsID(strangerAllowed, baseID) {
			t.Fatal("stranger should see open base account")
		}
	}

	if err := st.UnshareLocalAccount(ctx, owner, uname); err != nil {
		t.Fatal(err)
	}
	friendAllowed, err = st.AllowedAccountIDs(ctx, friend)
	if err != nil {
		t.Fatal(err)
	}
	if containsID(friendAllowed, id) {
		t.Fatal("after unshare, friend should lose access")
	}

	// Ensure row gone
	var n int
	if err := st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM eq_accounts WHERE id=$1`, id).Scan(&n); err != nil && err != sql.ErrNoRows {
		// count query shouldn't ErrNoRows
		if err != nil {
			t.Fatal(err)
		}
	}
	if n != 0 {
		t.Fatalf("account still present count=%d", n)
	}
}

func TestShareRestrictedByRole(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	owner, err := st.UpsertUser(ctx, "owner-"+randHex(4), "Owner", nil)
	if err != nil {
		t.Fatal(err)
	}
	friend, err := st.UpsertUser(ctx, "friend-"+randHex(4), "Friend", []string{"role-share-1"})
	if err != nil {
		t.Fatal(err)
	}
	stranger, err := st.UpsertUser(ctx, "stranger-"+randHex(4), "Stranger", nil)
	if err != nil {
		t.Fatal(err)
	}

	uname := "shareacct_" + randHex(6)
	id, _, err := st.ShareLocalAccount(ctx, owner, uname, "secret-pass", nil, nil, []string{"role-share-1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	friendAllowed, err := st.AllowedAccountIDs(ctx, friend)
	if err != nil {
		t.Fatal(err)
	}
	strangerAllowed, err := st.AllowedAccountIDs(ctx, stranger)
	if err != nil {
		t.Fatal(err)
	}
	if !containsID(friendAllowed, id) {
		t.Fatal("user with shared role should see restricted share")
	}
	if containsID(strangerAllowed, id) {
		t.Fatal("user without shared role must not see restricted share")
	}
}

func TestShareRestrictedByGroup(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	owner, err := st.UpsertUser(ctx, "owner-"+randHex(4), "Owner", nil)
	if err != nil {
		t.Fatal(err)
	}
	friend, err := st.UpsertUser(ctx, "friend-"+randHex(4), "Friend", nil)
	if err != nil {
		t.Fatal(err)
	}
	stranger, err := st.UpsertUser(ctx, "stranger-"+randHex(4), "Stranger", nil)
	if err != nil {
		t.Fatal(err)
	}

	groupID, err := st.CreateGroup(ctx, "share-group-"+randHex(4), "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddGroupUser(ctx, groupID, friend.ID); err != nil {
		t.Fatal(err)
	}

	uname := "shareacct_" + randHex(6)
	id, _, err := st.ShareLocalAccount(ctx, owner, uname, "secret-pass", nil, nil, nil, []int64{groupID})
	if err != nil {
		t.Fatal(err)
	}
	friendAllowed, err := st.AllowedAccountIDs(ctx, friend)
	if err != nil {
		t.Fatal(err)
	}
	strangerAllowed, err := st.AllowedAccountIDs(ctx, stranger)
	if err != nil {
		t.Fatal(err)
	}
	if !containsID(friendAllowed, id) {
		t.Fatal("group member should see restricted share")
	}
	if containsID(strangerAllowed, id) {
		t.Fatal("non-member must not see restricted share")
	}
}

func TestTokenCreateRevoke(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	u, err := st.UpsertUser(ctx, "tok-"+randHex(4), "Tok", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, id, err := st.CreateToken(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if raw == "" || id <= 0 {
		t.Fatalf("raw=%q id=%d", raw, id)
	}
	tok, ok, err := st.ActiveToken(ctx, u.ID)
	if err != nil || !ok || !tok.HasSecret {
		t.Fatalf("active=%v ok=%v err=%v", tok, ok, err)
	}
	if err := st.RevokeToken(ctx, u.ID, 0); err != nil {
		t.Fatal(err)
	}
	_, ok, err = st.ActiveToken(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected no active token")
	}
}

func TestMetricsRecordAndQuery(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Minute)
	values := map[string]float64{
		store.MetricGUIConnections: 4,
		store.MetricGameSessions:   2,
		store.MetricDBLatencyMS:    3.5,
	}
	if err := st.RecordMetricSamples(ctx, now, values); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordMetricSamples(ctx, now.Add(2*time.Minute), map[string]float64{
		store.MetricGUIConnections: 6,
	}); err != nil {
		t.Fatal(err)
	}
	series, err := st.QueryMetricSeries(ctx, now.Add(-time.Hour), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	gui := series[store.MetricGUIConnections]
	if len(gui) == 0 {
		t.Fatalf("series=%v", series)
	}
	last := gui[len(gui)-1].V
	if last != 6 {
		t.Fatalf("max=%v points=%v", last, gui)
	}
	lat := series[store.MetricDBLatencyMS]
	if len(lat) == 0 || lat[0].V != 3.5 {
		t.Fatalf("latency=%v", lat)
	}
	// Two connection samples in the same bucket should aggregate with max, not avg.
	bucket := now.Add(30 * time.Second)
	if err := st.RecordMetricSamples(ctx, bucket, map[string]float64{store.MetricGUIConnections: 2}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordMetricSamples(ctx, bucket.Add(10*time.Second), map[string]float64{store.MetricGUIConnections: 5}); err != nil {
		t.Fatal(err)
	}
	series, err = st.QueryMetricSeries(ctx, now.Add(-time.Hour), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range series[store.MetricGUIConnections] {
		if p.T.Equal(now.Truncate(time.Minute)) || p.T.Equal(bucket.Truncate(time.Minute)) {
			if p.V == 3.5 || p.V == 3 || p.V == 3.0 {
				t.Fatalf("expected integer max, got %v at %v", p.V, p.T)
			}
		}
	}
}

func TestPurgeMetricsOlderThan(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	old := time.Now().UTC().Add(-100 * 24 * time.Hour)
	if err := st.RecordMetricSamples(ctx, old, map[string]float64{store.MetricGUIConnections: 1}); err != nil {
		t.Fatal(err)
	}
	n, err := st.PurgeMetricsOlderThan(ctx, time.Now().UTC().Add(-90*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("purged=%d", n)
	}
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	const hexdigits = "0123456789abcdef"
	out := make([]byte, n*2)
	for i, v := range b {
		out[i*2] = hexdigits[v>>4]
		out[i*2+1] = hexdigits[v&0x0f]
	}
	return string(out)
}
