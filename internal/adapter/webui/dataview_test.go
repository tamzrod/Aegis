// internal/adapter/webui/dataview_test.go
package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamzrod/Aegis/internal/config"
)

// newServerWithDataview creates a Server with a temporary dataview.yaml path
// and returns a pre-authenticated handler ready for use in tests.
func newServerWithDataview(t *testing.T) (http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	dvPath := filepath.Join(dir, "dataview.yaml")

	s := NewServer(":0", &mockManager{}, config.AuthConfig{Username: "admin", PasswordHash: adminHashForTest})
	s.SetDataviewPath(dvPath)

	return newTestServerFromServer(t, s), dvPath
}

// newTestServerFromServer injects a session cookie into a Server and returns a handler.
func newTestServerFromServer(t *testing.T, s *Server) http.Handler {
	t.Helper()
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

// TestDataviewGetNoPersistence verifies GET /api/dataview returns an empty registers
// map when no dataview path is configured on the server.
func TestDataviewGetNoPersistence(t *testing.T) {
	h := newTestServer(&mockManager{})

	req := httptest.NewRequest(http.MethodGet, "/api/dataview", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("unexpected Content-Type: %q", ct)
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	regs, ok := resp["registers"]
	if !ok {
		t.Fatalf("expected 'registers' key in response")
	}
	if m, ok := regs.(map[string]interface{}); !ok || len(m) != 0 {
		t.Errorf("expected empty registers map, got: %v", regs)
	}
}

// TestDataviewGetEmptyFile verifies GET /api/dataview returns an empty config
// when the dataview.yaml file does not exist yet.
func TestDataviewGetEmptyFile(t *testing.T) {
	h, _ := newServerWithDataview(t)

	req := httptest.NewRequest(http.MethodGet, "/api/dataview", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := resp["registers"]; !ok {
		t.Errorf("expected 'registers' key in response")
	}
}

// TestDataviewPutAndGet verifies that PUT /api/dataview/register persists a register
// entry and GET /api/dataview returns it.
func TestDataviewPutAndGet(t *testing.T) {
	h, dvPath := newServerWithDataview(t)

	body := `{"device":"plc1","fc":3,"address":7000,"name":"Active Power","type":"float32","word_order":"AB"}`
	req := httptest.NewRequest(http.MethodPut, "/api/dataview/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("PUT want 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify the file was written.
	if _, err := os.Stat(dvPath); err != nil {
		t.Fatalf("expected dataview.yaml to be created: %v", err)
	}

	// GET should return the stored entry.
	req2 := httptest.NewRequest(http.MethodGet, "/api/dataview", nil)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("GET want 200, got %d: %s", rec2.Code, rec2.Body.String())
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(rec2.Body).Decode(&resp); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}

	regs := resp["registers"].(map[string]interface{})
	plc1, ok := regs["plc1"]
	if !ok {
		t.Fatalf("expected 'plc1' in registers")
	}
	fc3, ok := plc1.(map[string]interface{})["fc3"]
	if !ok {
		t.Fatalf("expected 'fc3' in plc1")
	}
	addr7000, ok := fc3.(map[string]interface{})["7000"]
	if !ok {
		t.Fatalf("expected address '7000' in fc3")
	}
	entry := addr7000.(map[string]interface{})
	if entry["name"] != "Active Power" {
		t.Errorf("name: want 'Active Power', got %q", entry["name"])
	}
	if entry["type"] != "float32" {
		t.Errorf("type: want 'float32', got %q", entry["type"])
	}
	if entry["word_order"] != "AB" {
		t.Errorf("word_order: want 'AB', got %q", entry["word_order"])
	}
}

// TestDataviewPutNoPersistence verifies PUT /api/dataview/register returns 503
// when no dataview path is configured on the server.
func TestDataviewPutNoPersistence(t *testing.T) {
	h := newTestServer(&mockManager{})

	body := `{"device":"plc1","fc":3,"address":7000,"name":"Test","type":"uint16","word_order":""}`
	req := httptest.NewRequest(http.MethodPut, "/api/dataview/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestDataviewPutMissingDevice verifies PUT /api/dataview/register returns 400
// when the device field is empty.
func TestDataviewPutMissingDevice(t *testing.T) {
	h, _ := newServerWithDataview(t)

	body := `{"device":"","fc":3,"address":7000,"name":"Test","type":"uint16","word_order":""}`
	req := httptest.NewRequest(http.MethodPut, "/api/dataview/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestDataviewPutInvalidFC verifies PUT /api/dataview/register returns 400
// when the fc field is out of range.
func TestDataviewPutInvalidFC(t *testing.T) {
	h, _ := newServerWithDataview(t)

	body := `{"device":"plc1","fc":9,"address":7000,"name":"Test","type":"uint16","word_order":""}`
	req := httptest.NewRequest(http.MethodPut, "/api/dataview/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestDataviewPutMethodNotAllowed verifies GET /api/dataview/register returns 405.
func TestDataviewPutMethodNotAllowed(t *testing.T) {
	h, _ := newServerWithDataview(t)

	req := httptest.NewRequest(http.MethodGet, "/api/dataview/register", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", rec.Code)
	}
}

// TestDataviewGetMethodNotAllowed verifies POST /api/dataview returns 405.
func TestDataviewGetMethodNotAllowed(t *testing.T) {
	h := newTestServer(&mockManager{})

	req := httptest.NewRequest(http.MethodPost, "/api/dataview", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", rec.Code)
	}
}

// TestDataviewPutClearEntry verifies that setting all fields to empty removes the entry
// from the dataview config.
func TestDataviewPutClearEntry(t *testing.T) {
	dir := t.TempDir()
	dvPath := filepath.Join(dir, "dataview.yaml")

	// Pre-write an entry.
	initial := dataviewFileConfig{
		Dataview: map[string]map[string]map[string]dataviewRegister{
			"plc1": {
				"fc3": {
					"7000": {Name: "Active Power", Type: "float32", WordOrder: "AB"},
				},
			},
		},
	}
	if err := writeDataviewFile(dvPath, initial); err != nil {
		t.Fatalf("write initial config: %v", err)
	}

	s := NewServer(":0", &mockManager{}, config.AuthConfig{Username: "admin", PasswordHash: adminHashForTest})
	s.SetDataviewPath(dvPath)
	h := newTestServerFromServer(t, s)

	// Clear the entry by sending empty fields.
	body := `{"device":"plc1","fc":3,"address":7000,"name":"","type":"","word_order":""}`
	req := httptest.NewRequest(http.MethodPut, "/api/dataview/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// GET should not contain the cleared entry.
	req2 := httptest.NewRequest(http.MethodGet, "/api/dataview", nil)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("GET want 200, got %d", rec2.Code)
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(rec2.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	regs := resp["registers"].(map[string]interface{})
	if plc1, ok := regs["plc1"]; ok {
		if fc3, ok := plc1.(map[string]interface{})["fc3"]; ok {
			if fc3m, ok := fc3.(map[string]interface{}); ok {
				if _, found := fc3m["7000"]; found {
					t.Error("expected entry '7000' to be removed after clearing all fields")
				}
			}
		}
	}
}

// TestDataviewPutPersistsAcrossRequests verifies that multiple PUT calls accumulate
// entries in the dataview file.
func TestDataviewPutPersistsAcrossRequests(t *testing.T) {
	h, _ := newServerWithDataview(t)

	entries := []struct {
		body string
		addr string
		name string
	}{
		{`{"device":"plc1","fc":3,"address":7000,"name":"Active Power","type":"float32","word_order":"AB"}`, "7000", "Active Power"},
		{`{"device":"plc1","fc":3,"address":7002,"name":"Status Word","type":"uint16","word_order":""}`, "7002", "Status Word"},
	}

	for _, e := range entries {
		req := httptest.NewRequest(http.MethodPut, "/api/dataview/register", strings.NewReader(e.body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT %s: want 200, got %d", e.addr, rec.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/dataview", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET want 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	regs := resp["registers"].(map[string]interface{})
	fc3 := regs["plc1"].(map[string]interface{})["fc3"].(map[string]interface{})

	for _, e := range entries {
		entry, ok := fc3[e.addr]
		if !ok {
			t.Errorf("expected address %s in fc3", e.addr)
			continue
		}
		if got := entry.(map[string]interface{})["name"]; got != e.name {
			t.Errorf("addr %s: want name=%q, got %q", e.addr, e.name, got)
		}
	}
}
