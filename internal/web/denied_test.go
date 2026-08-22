package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleDenied(t *testing.T) {
	s := &Server{}
	mux := http.NewServeMux()
	s.Mount(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, BasePath+"/denied", nil))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "Access denied") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, BasePath+"/denied?reason=revoked", nil))
	if !strings.Contains(rec.Body.String(), "SSO access revoked") {
		t.Fatalf("%s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, BasePath+"/denied?reason=not_authorized", nil))
	if !strings.Contains(rec.Body.String(), "Not authorized") {
		t.Fatalf("%s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, BasePath+"/denied", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405 got %d", rec.Code)
	}
}

func TestWriteJSONAndErr(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, map[string]any{"ok": true})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	writeErr(rec, http.StatusForbidden, "readonly")
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "readonly") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}
