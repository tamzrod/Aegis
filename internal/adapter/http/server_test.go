// internal/adapter/http/server_test.go
package webui_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	webui "github.com/tamzrod/Aegis/internal/adapter/http"
)

// stubRuntime implements view.RuntimeView with fixed values.
type stubRuntime struct {
	start          time.Time
	deviceCount    int
	readBlockCount int
}

func (s *stubRuntime) StartTime() time.Time  { return s.start }
func (s *stubRuntime) DeviceCount() int      { return s.deviceCount }
func (s *stubRuntime) ReadBlockCount() int   { return s.readBlockCount }

// stubConfig implements view.ConfigView with fixed bytes.
type stubConfig struct {
	data []byte
}

func (s *stubConfig) ActiveConfigYAML() []byte { return s.data }

func newTestServer() *webui.Server {
	rv := &stubRuntime{
		start:          time.Now().Add(-5 * time.Minute),
		deviceCount:    2,
		readBlockCount: 4,
	}
	cv := &stubConfig{data: []byte("server:\n  listeners: []\n")}
	return webui.NewServer(":0", rv, cv)
}

func TestHandleHealthz(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()

	srv.HandleHealthz(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("healthz: want status 200, got %d", rr.Code)
	}
	ct := rr.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("healthz: want Content-Type text/plain, got %q", ct)
	}
	if body := rr.Body.String(); body != "OK\n" {
		t.Errorf("healthz: want body %q, got %q", "OK\n", body)
	}
}

func TestHandleStatus(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rr := httptest.NewRecorder()

	srv.HandleStatus(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: want status 200, got %d", rr.Code)
	}
	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("status: want Content-Type application/json, got %q", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "uptime_seconds") {
		t.Errorf("status: want uptime_seconds in body, got %q", body)
	}
	if !strings.Contains(body, "device_count") {
		t.Errorf("status: want device_count in body, got %q", body)
	}
}

func TestHandleUI_Root(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	srv.NewMux().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("root: want status 200, got %d", rr.Code)
	}
	ct := rr.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("root: want Content-Type text/html, got %q", ct)
	}
	if body := rr.Body.String(); !strings.Contains(body, "<html") {
		t.Errorf("root: want HTML body, got %q", body)
	}
}

func TestHandleUI_StaticFiles(t *testing.T) {
	srv := newTestServer()
	mux := srv.NewMux()

	// /index.html is redirected to / by http.FileServer (standard behaviour).
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/index.html", http.StatusMovedPermanently},
		{"/app.js", http.StatusOK},
		{"/style.css", http.StatusOK},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != tc.want {
			t.Errorf("%s: want status %d, got %d", tc.path, tc.want, rr.Code)
		}
	}
}

func TestHandleConfig(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	rr := httptest.NewRecorder()

	srv.HandleConfig(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("config: want status 200, got %d", rr.Code)
	}
	ct := rr.Header().Get("Content-Type")
	if ct != "application/yaml" {
		t.Errorf("config: want Content-Type application/yaml, got %q", ct)
	}
	if body := rr.Body.String(); !strings.Contains(body, "listeners") {
		t.Errorf("config: want yaml content in body, got %q", body)
	}
}
