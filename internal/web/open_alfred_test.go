package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleOpenAlfredRedirectsToAppScheme(t *testing.T) {
	d, err := EncodeSourceDeepLinkPayload("Guild", "identity.example.com", "secret-token")
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/open-alfred?d="+d, nil)
	HandleOpenAlfred(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	body, _ := io.ReadAll(rr.Body)
	html := string(body)
	if !strings.Contains(html, AppURLScheme+"://import?d=") {
		t.Fatalf("missing app scheme: %s", html)
	}
	if strings.Contains(html, "secret-token") && !strings.Contains(html, "d=") {
		t.Fatal("token should only appear encoded")
	}
	if rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache=%q", rr.Header().Get("Cache-Control"))
	}
}

func TestHandleOpenAlfredInvalidPayload(t *testing.T) {
	rr := httptest.NewRecorder()
	HandleOpenAlfred(rr, httptest.NewRequest(http.MethodGet, "/open-alfred?d=not-a-valid-payload", nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleOpenAlfredNoPayload(t *testing.T) {
	rr := httptest.NewRecorder()
	HandleOpenAlfred(rr, httptest.NewRequest(http.MethodGet, "/open-alfred", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Open "+DesktopAppName) {
		t.Fatalf("body=%s", rr.Body.String())
	}
}

func TestHandleOpenAlfredMethodNotAllowed(t *testing.T) {
	rr := httptest.NewRecorder()
	HandleOpenAlfred(rr, httptest.NewRequest(http.MethodPost, "/open-alfred", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d", rr.Code)
	}
}
