package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRingRetainsRecords asserts the ring buffer keeps records after requests.
func TestRingRetainsRecords(t *testing.T) {
	t.Setenv("TEST_KEY", "secret-123")
	up := echoUpstream(t)
	defer up.Close()

	gw := New(baseConfig(up.URL), io.Discard, nil, nil)
	srv := httptest.NewServer(gw)
	defer srv.Close()

	for i := 0; i < 3; i++ {
		resp := do(t, srv, "POST", "/echo/v1/thing", map[string]string{SessionHeader: "tok"}, `{"hi":1}`)
		resp.Body.Close()
	}

	snap := gw.log.ring.snapshot()
	if len(snap) != 3 {
		t.Fatalf("ring retained %d records, want 3", len(snap))
	}
	// newest first: verify ordering is by insertion (all same path here, just check count + service)
	for _, rec := range snap {
		if rec.Service != "echo" {
			t.Fatalf("unexpected service %q", rec.Service)
		}
		if rec.Time == "" {
			t.Fatalf("record missing time")
		}
	}
}

// TestLogsEndpoint asserts /_gateway/logs returns retained records when authed
// and 401 without a session.
func TestLogsEndpoint(t *testing.T) {
	t.Setenv("TEST_KEY", "secret-123")
	up := echoUpstream(t)
	defer up.Close()

	gw := New(baseConfig(up.URL), io.Discard, nil, nil)
	srv := httptest.NewServer(gw)
	defer srv.Close()

	// generate one proxied request
	do(t, srv, "POST", "/echo/v1/thing", map[string]string{SessionHeader: "tok"}, `{"hi":1}`).Body.Close()

	// unauthenticated -> 401
	resp := do(t, srv, "GET", "/_gateway/logs", nil, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("logs without session = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	// authenticated -> logs returned
	resp = do(t, srv, "GET", "/_gateway/logs", map[string]string{AdminHeader: "admintok"}, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("logs = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Logs []accessLog `json:"logs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Logs) != 1 {
		t.Fatalf("logs endpoint returned %d records, want 1", len(body.Logs))
	}
	if body.Logs[0].Path != "/echo/v1/thing" {
		t.Fatalf("unexpected logged path %q", body.Logs[0].Path)
	}
}

// TestMetricsEndpoint asserts /_gateway/metrics aggregates the ring buffer and
// requires a session.
func TestMetricsEndpoint(t *testing.T) {
	t.Setenv("TEST_KEY", "secret-123")
	up := echoUpstream(t)
	defer up.Close()

	gw := New(baseConfig(up.URL), io.Discard, nil, nil)
	srv := httptest.NewServer(gw)
	defer srv.Close()

	for i := 0; i < 4; i++ {
		do(t, srv, "POST", "/echo/v1/thing", map[string]string{SessionHeader: "tok"}, `{"hi":1}`).Body.Close()
	}

	// unauthenticated -> 401
	resp := do(t, srv, "GET", "/_gateway/metrics", nil, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("metrics without session = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	resp = do(t, srv, "GET", "/_gateway/metrics", map[string]string{AdminHeader: "admintok"}, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics = %d, want 200", resp.StatusCode)
	}
	var m struct {
		TotalRequests  int   `json:"totalRequests"`
		TotalTokensEst int64 `json:"totalTokensEst"`
		Sessions       int   `json:"sessions"`
		PerService     map[string]struct {
			Requests  int   `json:"requests"`
			TokensEst int64 `json:"tokensEst"`
		} `json:"perService"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	if m.TotalRequests != 4 {
		t.Fatalf("totalRequests = %d, want 4", m.TotalRequests)
	}
	if m.Sessions != 1 {
		t.Fatalf("sessions = %d, want 1", m.Sessions)
	}
	if m.PerService["echo"].Requests != 4 {
		t.Fatalf("perService[echo].requests = %d, want 4", m.PerService["echo"].Requests)
	}
}

// TestCORSPreflight asserts the cors wrapper short-circuits OPTIONS before auth.
func TestCORSPreflight(t *testing.T) {
	gw := New(baseConfig("http://127.0.0.1:0"), io.Discard, nil, nil)
	srv := httptest.NewServer(cors(gw))
	defer srv.Close()

	resp := do(t, srv, "OPTIONS", "/_gateway/logs", nil, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("OPTIONS = %d, want 204", resp.StatusCode)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("missing CORS origin header")
	}
}
