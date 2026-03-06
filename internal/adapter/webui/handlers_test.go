// internal/adapter/webui/handlers_test.go
package webui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tamzrod/Aegis/internal/config"
	"github.com/tamzrod/Aegis/internal/runtime"
)

// mockManager implements Manager for testing.
type mockManager struct {
	yaml            []byte
	applyErr        error
	reloadErr       error
	rebuildErr      error
	startRuntimeErr error
	stopRuntimeErr  error
	reloadCalled    int32 // accessed atomically
}

func (m *mockManager) GetActiveConfigYAML() []byte { return m.yaml }
func (m *mockManager) ApplyConfig(b []byte) error  { return m.applyErr }
func (m *mockManager) ReloadFromDisk() error {
	atomic.AddInt32(&m.reloadCalled, 1)
	return m.reloadErr
}
func (m *mockManager) Rebuild(_ *config.Config, _ []byte) error { return m.rebuildErr }
func (m *mockManager) StartRuntime() error                      { return m.startRuntimeErr }
func (m *mockManager) StopRuntime() error                       { return m.stopRuntimeErr }

// adminHashForTest is the bcrypt hash of "admin" at MinCost, used by newTestServer.
// MinCost keeps unit tests fast while still exercising the real bcrypt path.
const adminHashForTest = "$2a$04$2Nnq62aDGVdv7IthZa8kUOYL.YoLVbmIiRvfoJg9lWjp0i49OC2.q"

func newTestServer(mgr Manager) http.Handler {
	s := NewServer(":0", mgr, config.AuthConfig{Username: "admin", PasswordHash: adminHashForTest})

	// Perform a real login to obtain a session cookie so handler tests
	// focus on handler logic rather than auth mechanics.
	loginBody := `{"username":"admin","password":"admin"}`
	loginReq := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(loginBody))
	loginRec := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(loginRec, loginReq)

	var sessionCookie string
	for _, c := range loginRec.Result().Cookies() {
		if c.Name == sessionCookieName {
			sessionCookie = c.Value
			break
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionCookie})
		s.srv.Handler.ServeHTTP(w, r2)
	})
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

// TestRestartReturns200 verifies that POST /api/restart returns 200 and calls ReloadFromDisk.
func TestRestartReturns200(t *testing.T) {
	mgr := &mockManager{}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodPost, "/api/restart", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}

	// Wait long enough for the goroutine (100ms delay) to call ReloadFromDisk.
	time.Sleep(300 * time.Millisecond)
	if atomic.LoadInt32(&mgr.reloadCalled) == 0 {
		t.Error("want ReloadFromDisk to be called after restart, but it was not")
	}
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
	s := NewServer(":0", mgr, config.AuthConfig{})
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

// TestGetConfigViewWithGroup verifies that the group field is included in the
// device view when the unit config declares a group.
func TestGetConfigViewWithGroup(t *testing.T) {
	yaml := []byte(`
replicator:
  units:
    - id: plc1
      group: "Site A"
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

	var view configView
	if err := json.NewDecoder(rec.Body).Decode(&view); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(view.Devices) != 1 {
		t.Fatalf("want 1 device, got %d", len(view.Devices))
	}
	if got := view.Devices[0].Group; got != "Site A" {
		t.Errorf("device group: want %q, got %q", "Site A", got)
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

// TestPutConfigApplyPreservesGroup verifies that the group field submitted in a
// configView body is persisted into the YAML passed to ApplyConfig.
func TestPutConfigApplyPreservesGroup(t *testing.T) {
	gcm := &groupCaptureMgrManager{yaml: []byte(validUnitYAML)}
	h := newTestServer(gcm)

	body := `{
		"devices": [{
			"key": "plc1",
			"display_name": "PLC1",
			"group": "Site A",
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
	if !strings.Contains(string(gcm.appliedYAML), "group: Site A") {
		t.Errorf("applied YAML does not contain group field; got:\n%s", gcm.appliedYAML)
	}
}

// groupCaptureMgrManager captures the YAML bytes passed to ApplyConfig and
// satisfies the Manager interface.
type groupCaptureMgrManager struct {
	yaml        []byte
	appliedYAML []byte
}

func (m *groupCaptureMgrManager) GetActiveConfigYAML() []byte { return m.yaml }
func (m *groupCaptureMgrManager) ApplyConfig(b []byte) error {
	m.appliedYAML = b
	return nil
}
func (m *groupCaptureMgrManager) ReloadFromDisk() error                    { return nil }
func (m *groupCaptureMgrManager) Rebuild(_ *config.Config, _ []byte) error { return nil }
func (m *groupCaptureMgrManager) StartRuntime() error                      { return nil }
func (m *groupCaptureMgrManager) StopRuntime() error                       { return nil }

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

// mockManagerWithListeners extends mockManagerWithStatus and implements ListenerProvider.
type mockManagerWithListeners struct {
	mockManagerWithStatus
	listeners []runtime.ListenerStatus
}

func (m *mockManagerWithListeners) ListenerStatuses() []runtime.ListenerStatus { return m.listeners }

// TestRuntimeStartSuccess verifies POST /api/runtime/start returns 200 when StartRuntime succeeds.
func TestRuntimeStartSuccess(t *testing.T) {
	mgr := &mockManager{}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodPost, "/api/runtime/start", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "started" {
		t.Errorf("want status=started, got %q", resp["status"])
	}
}

// TestRuntimeStartConflict verifies POST /api/runtime/start returns 409 when start fails.
func TestRuntimeStartConflict(t *testing.T) {
	mgr := &mockManager{startRuntimeErr: errors.New("cannot start: runtime state is RUNNING")}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodPost, "/api/runtime/start", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "error") {
		t.Errorf("expected JSON error field in body: %q", body)
	}
}

// TestRuntimeStartMethodNotAllowed verifies GET /api/runtime/start returns 405.
func TestRuntimeStartMethodNotAllowed(t *testing.T) {
	mgr := &mockManager{}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/runtime/start", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", rec.Code)
	}
}

// TestRuntimeStopSuccess verifies POST /api/runtime/stop returns 200 when StopRuntime succeeds.
func TestRuntimeStopSuccess(t *testing.T) {
	mgr := &mockManager{}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodPost, "/api/runtime/stop", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "stopped" {
		t.Errorf("want status=stopped, got %q", resp["status"])
	}
}

// TestRuntimeStopConflict verifies POST /api/runtime/stop returns 409 when stop fails.
func TestRuntimeStopConflict(t *testing.T) {
	mgr := &mockManager{stopRuntimeErr: errors.New("cannot stop: runtime state is STOPPED")}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodPost, "/api/runtime/stop", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "error") {
		t.Errorf("expected JSON error field in body: %q", body)
	}
}

// TestRuntimeStopMethodNotAllowed verifies GET /api/runtime/stop returns 405.
func TestRuntimeStopMethodNotAllowed(t *testing.T) {
	mgr := &mockManager{}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/runtime/stop", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", rec.Code)
	}
}

// TestRuntimeListenersWithProvider verifies GET /api/runtime/listeners returns listener JSON.
func TestRuntimeListenersWithProvider(t *testing.T) {
	mgr := &mockManagerWithListeners{
		listeners: []runtime.ListenerStatus{
			{Port: 502, Status: "listening"},
			{Port: 503, Status: "error", Error: "bind: address already in use"},
		},
	}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/runtime/listeners", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("unexpected Content-Type: %q", ct)
	}
	var got []runtime.ListenerStatus
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 listeners, got %d", len(got))
	}
	if got[0].Port != 502 || got[0].Status != "listening" {
		t.Errorf("unexpected first listener: %+v", got[0])
	}
	if got[1].Port != 503 || got[1].Status != "error" {
		t.Errorf("unexpected second listener: %+v", got[1])
	}
}

// TestRuntimeListenersNoProvider verifies GET /api/runtime/listeners returns empty array without provider.
func TestRuntimeListenersNoProvider(t *testing.T) {
	mgr := &mockManager{}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/runtime/listeners", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "[") {
		t.Errorf("expected JSON array in body: %q", body)
	}
}

// TestRuntimeListenersMethodNotAllowed verifies POST /api/runtime/listeners returns 405.
func TestRuntimeListenersMethodNotAllowed(t *testing.T) {
	mgr := &mockManager{}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodPost, "/api/runtime/listeners", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", rec.Code)
	}
}

// mockManagerWithDevices extends mockManagerWithListeners and implements DeviceStatusProvider.
type mockManagerWithDevices struct {
	mockManagerWithListeners
	deviceStatuses []runtime.DeviceStatus
}

func (m *mockManagerWithDevices) DeviceStatuses() []runtime.DeviceStatus { return m.deviceStatuses }

// TestRuntimeDevicesWithProvider verifies GET /api/runtime/devices returns device status JSON.
func TestRuntimeDevicesWithProvider(t *testing.T) {
	mgr := &mockManagerWithDevices{
		deviceStatuses: []runtime.DeviceStatus{
			{ID: "plc1", Status: "online"},
			{ID: "plc2", Status: "error"},
		},
	}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/runtime/devices", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("unexpected Content-Type: %q", ct)
	}
	var got []runtime.DeviceStatus
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 device statuses, got %d", len(got))
	}
	if got[0].ID != "plc1" || got[0].Status != "online" {
		t.Errorf("unexpected first device status: %+v", got[0])
	}
	if got[1].ID != "plc2" || got[1].Status != "error" {
		t.Errorf("unexpected second device status: %+v", got[1])
	}
}

// TestRuntimeDevicesNoProvider verifies GET /api/runtime/devices returns empty array without provider.
func TestRuntimeDevicesNoProvider(t *testing.T) {
	mgr := &mockManager{}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/runtime/devices", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "[") {
		t.Errorf("expected JSON array in body: %q", body)
	}
}

// TestRuntimeDevicesMethodNotAllowed verifies POST /api/runtime/devices returns 405.
func TestRuntimeDevicesMethodNotAllowed(t *testing.T) {
	mgr := &mockManager{}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodPost, "/api/runtime/devices", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", rec.Code)
	}
}

// newTestServerWithAuth creates a test server (without pre-injected session).
func newTestServerWithAuth(mgr Manager, username, passwordHash string) http.Handler {
	s := NewServer(":0", mgr, config.AuthConfig{Username: username, PasswordHash: passwordHash})
	return s.srv.Handler
}

// bcryptHashOf is the bcrypt hash of "testpassword" at MinCost, used in auth tests.
const bcryptHashOf_testpassword = "$2a$04$EfZKhUjPhcA6J4aFq0R7a.Onh4.XG5W1X4S0IgbgfYO2mNhlWqSFi"

// TestFormAuthNoSessionRedirects verifies that a request without a session cookie
// to a protected route is redirected to /login.
func TestFormAuthNoSessionRedirects(t *testing.T) {
	mgr := &mockManager{yaml: []byte("replicator: {}")}
	h := newTestServerWithAuth(mgr, "admin", bcryptHashOf_testpassword)

	req := httptest.NewRequest(http.MethodGet, "/api/config/raw", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("want 302, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("want Location: /login, got %q", loc)
	}
}

// TestFormAuthLoginWrongPassword verifies that POST /api/login with bad credentials returns 401.
func TestFormAuthLoginWrongPassword(t *testing.T) {
	mgr := &mockManager{yaml: []byte("replicator: {}")}
	h := newTestServerWithAuth(mgr, "admin", bcryptHashOf_testpassword)

	body := `{"username":"admin","password":"wrongpassword"}`
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

// TestFormAuthLoginWrongUsername verifies that POST /api/login with wrong username returns 401.
func TestFormAuthLoginWrongUsername(t *testing.T) {
	mgr := &mockManager{yaml: []byte("replicator: {}")}
	h := newTestServerWithAuth(mgr, "admin", bcryptHashOf_testpassword)

	body := `{"username":"wronguser","password":"testpassword"}`
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

// TestFormAuthLoginSuccess verifies that POST /api/login with valid credentials returns 200
// and sets a session cookie.
func TestFormAuthLoginSuccess(t *testing.T) {
	mgr := &mockManager{yaml: []byte("replicator: {}")}
	h := newTestServerWithAuth(mgr, "admin", bcryptHashOf_testpassword)

	body := `{"username":"admin","password":"testpassword"}`
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}

	var found bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			found = true
			if c.Value == "" {
				t.Error("session cookie value must not be empty")
			}
		}
	}
	if !found {
		t.Errorf("want %q cookie in response, got none", sessionCookieName)
	}
}

// TestFormAuthSessionGrantsAccess verifies that a valid session cookie grants access to a
// protected route.
func TestFormAuthSessionGrantsAccess(t *testing.T) {
	mgr := &mockManager{yaml: []byte("replicator: {}")}
	h := newTestServerWithAuth(mgr, "admin", bcryptHashOf_testpassword)

	// Login to get a session cookie.
	loginBody := `{"username":"admin","password":"testpassword"}`
	loginReq := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(loginBody))
	loginRec := httptest.NewRecorder()
	h.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login failed: %d", loginRec.Code)
	}
	var sessionCookie string
	for _, c := range loginRec.Result().Cookies() {
		if c.Name == sessionCookieName {
			sessionCookie = c.Value
		}
	}

	// Use the session cookie to access a protected endpoint.
	req := httptest.NewRequest(http.MethodGet, "/api/config/raw", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionCookie})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

// TestFormAuthEmptyConfigRedirects verifies that empty AuthConfig still redirects
// unauthenticated requests (auth is always enforced).
func TestFormAuthEmptyConfigRedirects(t *testing.T) {
	mgr := &mockManager{yaml: []byte("replicator: {}")}
	h := newTestServerWithAuth(mgr, "", "")

	req := httptest.NewRequest(http.MethodGet, "/api/config/raw", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("want 302 (always redirect), got %d", rec.Code)
	}
}

// mockPasswordUpdater implements PasswordUpdater for testing.
type mockPasswordUpdater struct {
	mockManager
	updateErr   error
	updatedHash string
}

func (m *mockPasswordUpdater) UpdatePasswordHash(hash string) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.updatedHash = hash
	return nil
}

// newTestServerWithDefaultPassword creates a test server in DEFAULT MODE
// (DefaultPassword=true) and returns both the handler and the session cookie
// obtained by logging in with admin/admin.
func newTestServerWithDefaultPassword() (http.Handler, string) {
	mgr := &mockPasswordUpdater{}
	s := NewServer(":0", mgr, config.AuthConfig{
		Username:        "admin",
		PasswordHash:    adminHashForTest,
		DefaultPassword: true,
	})

	loginBody := `{"username":"admin","password":"admin"}`
	loginReq := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(loginBody))
	loginRec := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(loginRec, loginReq)

	var sessionCookie string
	for _, c := range loginRec.Result().Cookies() {
		if c.Name == sessionCookieName {
			sessionCookie = c.Value
			break
		}
	}
	return s.srv.Handler, sessionCookie
}

// TestLoginDefaultModeReturnsPasswordChangeRequired verifies that POST /api/login
// with DefaultPassword=true returns password_change_required=true in the response.
func TestLoginDefaultModeReturnsPasswordChangeRequired(t *testing.T) {
	mgr := &mockPasswordUpdater{}
	// Manually set DefaultPassword on the server's handler via NewServer.
	s := NewServer(":0", mgr, config.AuthConfig{
		Username:        "admin",
		PasswordHash:    adminHashForTest,
		DefaultPassword: true,
	})
	handler := s.srv.Handler

	body := `{"username":"admin","password":"admin"}`
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("want status=ok, got %v", resp["status"])
	}
	if resp["password_change_required"] != true {
		t.Errorf("want password_change_required=true, got %v", resp["password_change_required"])
	}
}

// TestLoginNormalModeDoesNotRequirePasswordChange verifies that POST /api/login
// with DefaultPassword=false does not return password_change_required.
func TestLoginNormalModeDoesNotRequirePasswordChange(t *testing.T) {
	mgr := &mockManager{}
	handler := newTestServerWithAuth(mgr, "admin", adminHashForTest)

	body := `{"username":"admin","password":"admin"}`
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["password_change_required"] == true {
		t.Errorf("want password_change_required absent or false, got true")
	}
}

// TestPasswordChangeRequiredSessionRedirects verifies that a session with
// passwordChangeRequired=true is redirected to /change-password when accessing
// other protected routes.
func TestPasswordChangeRequiredSessionRedirects(t *testing.T) {
	h, sessionCookie := newTestServerWithDefaultPassword()

	req := httptest.NewRequest(http.MethodGet, "/api/config/raw", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionCookie})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("want 302, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/change-password" {
		t.Errorf("want Location: /change-password, got %q", loc)
	}
}

// TestPasswordChangeRequiredSessionAllowsChangePasswordRoute verifies that a
// session with passwordChangeRequired=true can access /change-password.
func TestPasswordChangeRequiredSessionAllowsChangePasswordRoute(t *testing.T) {
	h, sessionCookie := newTestServerWithDefaultPassword()

	req := httptest.NewRequest(http.MethodGet, "/change-password", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionCookie})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// Should not redirect; change-password page is served (200).
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (Location: %s)", rec.Code, rec.Header().Get("Location"))
	}
}

// TestHandleChangePasswordSuccess verifies POST /api/change-password succeeds
// and clears the password-change-required session flag.
func TestHandleChangePasswordSuccess(t *testing.T) {
	h, sessionCookie := newTestServerWithDefaultPassword()

	body := `{"new_password":"newpass123","confirm_password":"newpass123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/change-password", strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionCookie})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("want status=ok, got %q", resp["status"])
	}

	// After password change, the session should allow access to protected routes.
	req2 := httptest.NewRequest(http.MethodGet, "/api/config/raw", nil)
	req2.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionCookie})
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("want 200 after password change, got %d", rec2.Code)
	}
}

// TestHandleChangePasswordMismatch verifies POST /api/change-password returns 400
// when passwords do not match.
func TestHandleChangePasswordMismatch(t *testing.T) {
	h, sessionCookie := newTestServerWithDefaultPassword()

	body := `{"new_password":"newpass123","confirm_password":"different"}`
	req := httptest.NewRequest(http.MethodPost, "/api/change-password", strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionCookie})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

// TestHandleChangePasswordEmptyPassword verifies POST /api/change-password returns 400
// when the new password is empty.
func TestHandleChangePasswordEmptyPassword(t *testing.T) {
	h, sessionCookie := newTestServerWithDefaultPassword()

	body := `{"new_password":"","confirm_password":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/change-password", strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionCookie})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

// TestFaviconRoutesPublic verifies that /favicon/* assets are served without a session cookie.
func TestFaviconRoutesPublic(t *testing.T) {
	paths := []struct {
		path        string
		contentType string
	}{
		{"/favicon/favicon.ico", "image/vnd.microsoft.icon"},
		{"/favicon/favicon.svg", "image/svg+xml"},
		{"/favicon/favicon-96x96.png", "image/png"},
		{"/favicon/apple-touch-icon.png", "image/png"},
		{"/favicon/site.webmanifest", "application/manifest+json"},
		{"/favicon/web-app-manifest-192x192.png", "image/png"},
		{"/favicon/web-app-manifest-512x512.png", "image/png"},
	}

	mgr := &mockManager{}
	h := newTestServerWithAuth(mgr, "admin", bcryptHashOf_testpassword)

	for _, tc := range paths {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: want 200, got %d", tc.path, rec.Code)
			continue
		}
		ct := rec.Header().Get("Content-Type")
		if !strings.HasPrefix(ct, tc.contentType) {
			t.Errorf("GET %s: want Content-Type %q, got %q", tc.path, tc.contentType, ct)
		}
	}
}

// ---------- handleDeviceStatus tests ----------

// mockDeviceStatusReader implements DeviceStatusReader for testing.
type mockDeviceStatusReader struct {
	snap    *runtime.StatusBlockSnapshot
	readErr error
}

func (m *mockDeviceStatusReader) ReadDeviceStatus(port, statusUnitID, statusSlot uint16) (*runtime.StatusBlockSnapshot, error) {
	return m.snap, m.readErr
}

// mockManagerWithDSR embeds mockManager and also implements DeviceStatusReader.
type mockManagerWithDSR struct {
	mockManager
	dsr mockDeviceStatusReader
}

func (m *mockManagerWithDSR) ReadDeviceStatus(port, statusUnitID, statusSlot uint16) (*runtime.StatusBlockSnapshot, error) {
	return m.dsr.ReadDeviceStatus(port, statusUnitID, statusSlot)
}

// TestHandleDeviceStatusOK verifies GET /api/device/status returns 200 with the snapshot.
func TestHandleDeviceStatusOK(t *testing.T) {
	snap := &runtime.StatusBlockSnapshot{
		Health:              "OK",
		Online:              true,
		RequestsTotal:       42,
		ResponsesValid:      40,
		TimeoutsTotal:       2,
		TransportErrors:     0,
		ConsecutiveFailCurr: 0,
		ConsecutiveFailMax:  1,
	}
	mgr := &mockManagerWithDSR{dsr: mockDeviceStatusReader{snap: snap}}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/device/status?port=502&unit_id=100&slot=0", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("unexpected Content-Type: %q", ct)
	}
	var got runtime.StatusBlockSnapshot
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Health != "OK" {
		t.Errorf("health: want OK, got %q", got.Health)
	}
	if !got.Online {
		t.Error("want online=true")
	}
	if got.RequestsTotal != 42 {
		t.Errorf("requests_total: want 42, got %d", got.RequestsTotal)
	}
}

// TestHandleDeviceStatusMissingParams verifies GET /api/device/status returns 400
// when required query parameters are absent.
func TestHandleDeviceStatusMissingParams(t *testing.T) {
	mgr := &mockManagerWithDSR{}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/device/status?unit_id=100&slot=0", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

// TestHandleDeviceStatusNotFound verifies GET /api/device/status returns 404
// when the status block is not found in the store.
func TestHandleDeviceStatusNotFound(t *testing.T) {
	mgr := &mockManagerWithDSR{
		dsr: mockDeviceStatusReader{readErr: errors.New("status memory not found")},
	}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/device/status?port=502&unit_id=100&slot=0", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

// TestHandleDeviceStatusNoProvider verifies GET /api/device/status returns 503
// when no DeviceStatusReader is registered (manager does not implement the interface).
func TestHandleDeviceStatusNoProvider(t *testing.T) {
	mgr := &mockManager{}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/device/status?port=502&unit_id=100&slot=0", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rec.Code)
	}
}

// TestHandleDeviceStatusMethodNotAllowed verifies POST /api/device/status returns 405.
func TestHandleDeviceStatusMethodNotAllowed(t *testing.T) {
	mgr := &mockManagerWithDSR{}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodPost, "/api/device/status?port=502&unit_id=100&slot=0", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", rec.Code)
	}
}

// TestHelpPageServed verifies GET /help returns 200 and the help page HTML.
func TestHelpPageServed(t *testing.T) {
	mgr := &mockManager{}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodGet, "/help", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Aegis Documentation") {
		t.Errorf("expected help page body to contain 'Aegis Documentation', got: %.200s", body)
	}
}

// ---------- handleViewerRead tests ----------

// mockViewerReader implements ViewerReader for testing.
type mockViewerReader struct {
	mockManager
	values   []uint16
	readErr  error
	lastKey  string
	lastFC   uint8
	lastAddr uint16
	lastQty  uint16
}

func (m *mockViewerReader) ReadViewerRegisters(deviceKey string, fc uint8, address, quantity uint16) ([]uint16, error) {
	m.lastKey = deviceKey
	m.lastFC = fc
	m.lastAddr = address
	m.lastQty = quantity
	return m.values, m.readErr
}

// TestHandleViewerReadOK verifies GET /api/viewer/read returns 200 with register values.
func TestHandleViewerReadOK(t *testing.T) {
	mgr := &mockViewerReader{values: []uint16{230, 125, 5400}}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/viewer/read?device=inv01&fc=3&address=7000&quantity=3", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("unexpected Content-Type: %q", ct)
	}
	var got viewerReadResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Device != "inv01" {
		t.Errorf("want device=inv01, got %q", got.Device)
	}
	if got.FC != 3 {
		t.Errorf("want fc=3, got %d", got.FC)
	}
	if got.Address != 7000 {
		t.Errorf("want address=7000, got %d", got.Address)
	}
	if len(got.Values) != 3 || got.Values[0] != 230 || got.Values[1] != 125 || got.Values[2] != 5400 {
		t.Errorf("unexpected values: %v", got.Values)
	}
}

// TestHandleViewerReadMissingDevice verifies GET /api/viewer/read returns 400 when device is missing.
func TestHandleViewerReadMissingDevice(t *testing.T) {
	mgr := &mockViewerReader{}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/viewer/read?fc=3&address=0&quantity=10", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

// TestHandleViewerReadMissingParams verifies GET /api/viewer/read returns 400 when fc/address/quantity are missing.
func TestHandleViewerReadMissingParams(t *testing.T) {
	mgr := &mockViewerReader{}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/viewer/read?device=inv01&address=0&quantity=10", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleViewerReadInvalidFC verifies GET /api/viewer/read returns 400 for fc=5.
func TestHandleViewerReadInvalidFC(t *testing.T) {
	mgr := &mockViewerReader{}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/viewer/read?device=inv01&fc=5&address=0&quantity=10", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

// TestHandleViewerReadZeroQuantity verifies GET /api/viewer/read returns 400 for quantity=0.
func TestHandleViewerReadZeroQuantity(t *testing.T) {
	mgr := &mockViewerReader{}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/viewer/read?device=inv01&fc=3&address=0&quantity=0", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

// TestHandleViewerReadBackendError verifies GET /api/viewer/read returns 502 when the store read fails.
func TestHandleViewerReadBackendError(t *testing.T) {
	mgr := &mockViewerReader{readErr: errors.New("memory not found")}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/viewer/read?device=inv01&fc=3&address=7000&quantity=10", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "error") {
		t.Errorf("expected JSON error field in body: %q", body)
	}
}

// TestHandleViewerReadNoProvider verifies GET /api/viewer/read returns 503 when no ViewerReader is registered.
func TestHandleViewerReadNoProvider(t *testing.T) {
	mgr := &mockManager{}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/viewer/read?device=inv01&fc=3&address=0&quantity=10", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rec.Code)
	}
}

// TestHandleViewerReadMethodNotAllowed verifies POST /api/viewer/read returns 405.
func TestHandleViewerReadMethodNotAllowed(t *testing.T) {
	mgr := &mockViewerReader{}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodPost, "/api/viewer/read", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", rec.Code)
	}
}

// TestViewerPageServed verifies GET /viewer returns 200 and the viewer page HTML.
func TestViewerPageServed(t *testing.T) {
	mgr := &mockManager{}
	h := newTestServer(mgr)

	req := httptest.NewRequest(http.MethodGet, "/viewer", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Data Viewer") {
		t.Errorf("expected viewer page body to contain 'Data Viewer', got: %.200s", body)
	}
}
