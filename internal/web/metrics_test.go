package web

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alfred-identity/web/internal/crypto"
	"github.com/alfred-identity/web/internal/db"
	"github.com/alfred-identity/web/internal/store"
)

func openTestStoreForWeb(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run metrics API integration tests")
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

func TestHandleMetricsValidation(t *testing.T) {
	s := testServer(t)
	rr := httptest.NewRecorder()
	s.handleMetrics(rr, httptest.NewRequest(http.MethodPost, BasePath+"/api/metrics", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	s.handleMetrics(rr, httptest.NewRequest(http.MethodGet, BasePath+"/api/metrics?range=bad", nil))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "invalid_range") {
		t.Fatalf("bad range: %d %s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	s.handleMetrics(rr, httptest.NewRequest(http.MethodGet, BasePath+"/api/metrics", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil store: %d %s", rr.Code, rr.Body.String())
	}
}

func TestHandleMetricsWithStore(t *testing.T) {
	st := openTestStoreForWeb(t)
	s := New(Options{
		Store:             st,
		SessionKey:        []byte("test-session-key-32-bytes-long!!"),
		PublicURL:         "http://127.0.0.1:8181",
		AccessRoleID:      "role-access",
		BootstrapAdminIDs: []string{"bootstrap-1"},
	})
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Minute)
	if err := st.RecordMetricSamples(ctx, now, map[string]float64{
		store.MetricGUIConnections: 2,
		store.MetricGameSessions:   1,
	}); err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, BasePath+"/api/metrics?range=1h", nil)
	s.handleMetrics(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["range"] != "1h" || body["ok"] != true {
		t.Fatalf("%v", body)
	}
	series, _ := body["series"].(map[string]any)
	gui, _ := series["gui_connections"].([]any)
	if len(gui) == 0 {
		t.Fatalf("expected gui series: %v", series)
	}
}
