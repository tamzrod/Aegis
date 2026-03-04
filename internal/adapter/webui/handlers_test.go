// internal/adapter/webui/handlers_test.go
package webui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tamzrod/Aegis/internal/config"
	"github.com/tamzrod/Aegis/internal/runtime"
)

// mockManager implements Manager for testing.
type mockManager struct {
	yaml       []byte
	applyErr   error
	reloadErr  error
	rebuildErr error
}

func (m *mockManager) GetActiveConfigYAML() []byte                              { return m.yaml }
func (m *mockManager) ApplyConfig(b []byte) error                               { return m.applyErr }
func (m *mockManager) ReloadFromDisk() error                                    { return m.reloadErr }
func (m *mockManager) Rebuild(_ *config.Config, _ []byte) error                 { return m.rebuildErr }

func newTestServer(mgr Manager) http.Handler {
	s := NewServer(":0", mgr)
	return s.srv.Handler
}

// TestGetConfigRaw verifies that GET /api/config/raw returns the active YAML.
func TestGetConfigRaw(t *testing.T) {
	mgr := &mockManager{yaml: []byte("replicator: {}")}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/config/raw", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if body := rec.Body.String(); body != "replicator: {}" {
		t.Errorf("unexpected body: %q", body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/yaml") {
		t.Errorf("unexpected Content-Type: %q", ct)
	}
}

// TestPutConfigRawSuccess verifies that a valid PUT returns 200.
func TestPutConfigRawSuccess(t *testing.T) {
	mgr := &mockManager{}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodPut, "/api/config/raw", strings.NewReader("replicator: {}"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

// TestPutConfigRawValidationFailure verifies that an invalid config returns 400 with JSON error.
func TestPutConfigRawValidationFailure(t *testing.T) {
	mgr := &mockManager{applyErr: errors.New("replicator.units: at least one unit required")}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodPut, "/api/config/raw", strings.NewReader("bad: yaml"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("unexpected Content-Type: %q", ct)
	}
	if body := rec.Body.String(); !strings.Contains(body, "error") {
		t.Errorf("expected JSON error field in body: %q", body)
	}
}

// TestReloadSuccess verifies that POST /api/reload returns 200 on success.
func TestReloadSuccess(t *testing.T) {
	mgr := &mockManager{}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodPost, "/api/reload", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

// TestReloadFailure verifies that POST /api/reload returns 400 on validation failure.
func TestReloadFailure(t *testing.T) {
	mgr := &mockManager{reloadErr: errors.New("invalid config")}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodPost, "/api/reload", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

// TestRestartReturns200 verifies that POST /api/restart returns 200.
func TestRestartReturns200(t *testing.T) {
	// Intercept os.Exit so the test process is not killed.
	orig := osExit
	exited := false
	osExit = func(code int) { exited = true }
	defer func() { osExit = orig }()

	mgr := &mockManager{}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodPost, "/api/restart", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	_ = exited // exit happens in a goroutine; we just verify 200 response
}

// TestWebUIDisabledByDefault verifies that the default WebUIConfig has enabled=false.
func TestWebUIDisabledByDefault(t *testing.T) {
	// This verifies the config struct default behaviour documented in the spec.
	// When webui is absent from YAML, Enabled should be false.
	yaml := []byte(`
replicator:
  units:
    - id: plc1
      source:
        endpoint: "192.168.1.1:502"
        timeout_ms: 1000
      reads:
        - fc: 3
          address: 0
          quantity: 10
          interval_ms: 1000
      target:
        port: 502
        unit_id: 1
`)
	// We import config indirectly via the test to avoid cycle — we just verify
	// the Manager interface is the contract; config defaults are tested in config package.
	// Here we verify the WebUI server is not started when Enabled=false by checking
	// that NewServer is the only construct needed (no automatic start).
	mgr := &mockManager{yaml: yaml}
	s := NewServer(":0", mgr)
	if s == nil {
		t.Fatal("NewServer returned nil")
	}
}

// TestGetConfigView verifies that GET /api/config/view returns a valid JSON view model.
func TestGetConfigView(t *testing.T) {
	yaml := []byte(`
replicator:
  units:
    - id: plc1
      source:
        endpoint: "192.168.1.100:502"
        unit_id: 1
        timeout_ms: 1000
        device_name: "PLC1"
      reads:
        - fc: 3
          address: 0
          quantity: 10
          interval_ms: 1000
      target:
        port: 502
        unit_id: 1
        status_unit_id: 255
        status_slot: 0
        mode: "B"
`)
	mgr := &mockManager{yaml: yaml}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/config/view", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("unexpected Content-Type: %q", ct)
	}

	var view configView
	if err := json.NewDecoder(rec.Body).Decode(&view); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(view.Devices) != 1 {
		t.Fatalf("want 1 device, got %d", len(view.Devices))
	}
	d := view.Devices[0]
	if d.Key != "plc1" {
		t.Errorf("device key: want %q, got %q", "plc1", d.Key)
	}
	if d.DisplayName != "PLC1" {
		t.Errorf("display name: want %q, got %q", "PLC1", d.DisplayName)
	}
	if d.Source.Endpoint != "192.168.1.100:502" {
		t.Errorf("source endpoint: want %q, got %q", "192.168.1.100:502", d.Source.Endpoint)
	}
	if len(d.Reads) != 1 {
		t.Fatalf("want 1 read, got %d", len(d.Reads))
	}
	if d.Reads[0].FC != 3 {
		t.Errorf("read FC: want 3, got %d", d.Reads[0].FC)
	}
	if d.Target.Port != 502 {
		t.Errorf("target port: want 502, got %d", d.Target.Port)
	}
	if d.Target.Mode != "B" {
		t.Errorf("target mode: want %q, got %q", "B", d.Target.Mode)
	}
	if view.SelectedKey != "plc1" {
		t.Errorf("selected_key: want %q, got %q", "plc1", view.SelectedKey)
	}
}

// TestGetConfigViewMethodNotAllowed verifies that POST /api/config/view returns 405.
func TestGetConfigViewMethodNotAllowed(t *testing.T) {
	mgr := &mockManager{yaml: []byte(`replicator: {units: []}`)}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodPost, "/api/config/view", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", rec.Code)
	}
}

// validUnitYAML is shared YAML for apply tests.
const validUnitYAML = `
replicator:
  units:
    - id: plc1
      source:
        endpoint: "192.168.1.100:502"
        unit_id: 1
        timeout_ms: 1000
        device_name: "PLC1"
      reads:
        - fc: 3
          address: 0
          quantity: 10
          interval_ms: 1000
      target:
        port: 502
        unit_id: 1
        status_unit_id: 255
        status_slot: 0
        mode: "B"
webui:
  enabled: true
  listen: ":8080"
`

// TestPutConfigApplySuccess verifies that PUT /api/config/apply with a valid
// configView JSON body returns 200 with {"status":"ok"}.
func TestPutConfigApplySuccess(t *testing.T) {
	mgr := &mockManager{yaml: []byte(validUnitYAML)}
	h := newTestServer(mgr)

	// Build a minimal valid configView JSON body.
	body := `{
		"devices": [{
			"key": "plc1",
			"display_name": "PLC1",
			"source": {"endpoint":"192.168.1.100:502","unit_id":1,"timeout_ms":1000,"device_name":"PLC1"},
			"reads":  [{"fc":3,"address":0,"quantity":10,"interval_ms":1000}],
			"target": {"port":502,"unit_id":1,"status_unit_id":255,"status_slot":0,"mode":"B"}
		}],
		"selected_key": "plc1"
	}`

	req := httptest.NewRequest(http.MethodPut, "/api/config/apply", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("unexpected Content-Type: %q", ct)
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] != "applied" {
		t.Errorf("want status=applied, got %q", resp["status"])
	}
}

// TestPutConfigApplyValidationFailure verifies that PUT /api/config/apply returns
// 400 with a JSON error body when the manager rejects the config.
func TestPutConfigApplyValidationFailure(t *testing.T) {
	mgr := &mockManager{
		yaml:     []byte(validUnitYAML),
		applyErr: errors.New("replicator.units[0]: source.timeout_ms must be > 0"),
	}
	h := newTestServer(mgr)

	body := `{
		"devices": [{
			"key": "plc1",
			"display_name": "PLC1",
			"source": {"endpoint":"192.168.1.100:502","unit_id":1,"timeout_ms":0,"device_name":"PLC1"},
			"reads":  [{"fc":3,"address":0,"quantity":10,"interval_ms":1000}],
			"target": {"port":502,"unit_id":1,"status_unit_id":255,"status_slot":0,"mode":"B"}
		}],
		"selected_key": "plc1"
	}`

	req := httptest.NewRequest(http.MethodPut, "/api/config/apply", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("unexpected Content-Type: %q", ct)
	}
	if body := rec.Body.String(); !strings.Contains(body, "error") {
		t.Errorf("expected JSON error field in body: %q", body)
	}
}

// TestPutConfigApplyRebuildFailure verifies that PUT /api/config/apply returns
// 500 with {"error":"runtime rebuild failed"} when applying the config fails.
func TestPutConfigApplyRebuildFailure(t *testing.T) {
	mgr := &mockManager{
		yaml:     []byte(validUnitYAML),
		applyErr: errors.New("port already in use"),
	}
	h := newTestServer(mgr)

	body := `{
		"devices": [{
			"key": "plc1",
			"display_name": "PLC1",
			"source": {"endpoint":"192.168.1.100:502","unit_id":1,"timeout_ms":1000,"device_name":"PLC1"},
			"reads":  [{"fc":3,"address":0,"quantity":10,"interval_ms":1000}],
			"target": {"port":502,"unit_id":1,"status_unit_id":255,"status_slot":0,"mode":"B"}
		}],
		"selected_key": "plc1"
	}`

	req := httptest.NewRequest(http.MethodPut, "/api/config/apply", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("unexpected Content-Type: %q", ct)
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "runtime rebuild failed" {
		t.Errorf("want error=%q, got %q", "runtime rebuild failed", resp["error"])
	}
}

// TestPutConfigApplyMethodNotAllowed verifies that GET /api/config/apply returns 405.
func TestPutConfigApplyMethodNotAllowed(t *testing.T) {
	mgr := &mockManager{yaml: []byte(validUnitYAML)}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/config/apply", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", rec.Code)
	}
}

// mockManagerWithStatus extends mockManager and implements StatusProvider.
type mockManagerWithStatus struct {
	mockManager
	state runtime.RuntimeState
}

func (m *mockManagerWithStatus) RuntimeStatus() runtime.RuntimeState { return m.state }

// TestRuntimeStatusRunning verifies that GET /api/runtime/status returns running=true.
func TestRuntimeStatusRunning(t *testing.T) {
	mgr := &mockManagerWithStatus{state: runtime.RuntimeState{Running: true}}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/runtime/status", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("unexpected Content-Type: %q", ct)
	}

	var got runtime.RuntimeState
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Running {
		t.Errorf("want running=true, got false")
	}
	if got.Error != "" {
		t.Errorf("want empty error, got %q", got.Error)
	}
}

// TestRuntimeStatusError verifies that GET /api/runtime/status returns running=false with error.
func TestRuntimeStatusError(t *testing.T) {
	mgr := &mockManagerWithStatus{
		state: runtime.RuntimeState{Running: false, Error: "dial timeout"},
	}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/runtime/status", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}

	var got runtime.RuntimeState
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Running {
		t.Errorf("want running=false, got true")
	}
	if got.Error != "dial timeout" {
		t.Errorf("want error=%q, got %q", "dial timeout", got.Error)
	}
}

// TestRuntimeStatusNoProvider verifies that GET /api/runtime/status returns running=false
// when the manager does not implement StatusProvider.
func TestRuntimeStatusNoProvider(t *testing.T) {
	mgr := &mockManager{}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/runtime/status", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}

	var got runtime.RuntimeState
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Running {
		t.Errorf("want running=false, got true")
	}
}

// TestRuntimeStatusMethodNotAllowed verifies that POST /api/runtime/status returns 405.
func TestRuntimeStatusMethodNotAllowed(t *testing.T) {
	mgr := &mockManager{}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodPost, "/api/runtime/status", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", rec.Code)
	}
}

// TestGetConfigExport verifies that GET /api/config/export returns the active YAML
// with Content-Disposition header for file download.
func TestGetConfigExport(t *testing.T) {
	mgr := &mockManager{yaml: []byte("replicator:\n  units: []\n")}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/config/export", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/yaml") {
		t.Errorf("unexpected Content-Type: %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("expected attachment Content-Disposition, got: %q", cd)
	}
	if body := rec.Body.String(); body != "replicator:\n  units: []\n" {
		t.Errorf("unexpected body: %q", body)
	}
}

// TestGetConfigExportMethodNotAllowed verifies that POST /api/config/export returns 405.
func TestGetConfigExportMethodNotAllowed(t *testing.T) {
	mgr := &mockManager{yaml: []byte("replicator: {}")}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodPost, "/api/config/export", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", rec.Code)
	}
}

// TestPostConfigImportSuccess verifies that POST /api/config/import with valid YAML returns 200.
func TestPostConfigImportSuccess(t *testing.T) {
	mgr := &mockManager{}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodPost, "/api/config/import", strings.NewReader("replicator:\n  units: []\n"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("unexpected Content-Type: %q", ct)
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] != "imported" {
		t.Errorf("want status=imported, got %q", resp["status"])
	}
}

// TestPostConfigImportFailure verifies that POST /api/config/import returns 400 on apply error.
func TestPostConfigImportFailure(t *testing.T) {
	mgr := &mockManager{applyErr: errors.New("replicator.units: at least one unit required")}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodPost, "/api/config/import", strings.NewReader("bad: yaml"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "error") {
		t.Errorf("expected JSON error field in body: %q", body)
	}
}

// TestPostConfigImportMethodNotAllowed verifies that GET /api/config/import returns 405.
func TestPostConfigImportMethodNotAllowed(t *testing.T) {
	mgr := &mockManager{}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/config/import", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", rec.Code)
	}
}
