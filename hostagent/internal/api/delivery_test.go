package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeliveryPullAndDailySeparation(t *testing.T) {
	// mock gateway returning one assigned GitHub issue
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"number":7,"title":"issue7","html_url":"https://github.com/o/r/issues/7",
		  "state":"open","repository_url":"https://api.github.com/repos/o/r","updated_at":"2026-07-10T00:00:00Z"}]`))
	}))
	defer gw.Close()

	cfg := &Config{Gateway: GatewayConfig{URL: gw.URL, Session: "s"}, DemoSources: true}
	srv := httptest.NewServer(New(cfg).Handler())
	defer srv.Close()

	// Delivery pull -> 1 github issue
	resp, body := req(t, srv, "POST", "/delivery/pull", nil)
	if resp.StatusCode != 200 || body["pulled"].(float64) != 1 {
		t.Fatalf("delivery pull: %d %v", resp.StatusCode, body)
	}
	_, body = req(t, srv, "GET", "/delivery/issues", nil)
	if issues, _ := body["issues"].([]any); len(issues) != 1 {
		t.Fatalf("delivery issues = %v, want 1", body["issues"])
	}

	// Daily pull (demo) + list must NOT include the github issue
	req(t, srv, "POST", "/daily/pull?source=demo", nil)
	_, body = req(t, srv, "GET", "/daily/tickets", nil)
	dts, _ := body["tickets"].([]any)
	for _, tk := range dts {
		if m, ok := tk.(map[string]any); ok && m["source"] == "github" {
			t.Fatalf("github leaked into Daily tickets: %v", m)
		}
	}
	if len(dts) != 2 { // the two demo tickets only
		t.Fatalf("daily tickets = %d, want 2 (demo only)", len(dts))
	}

	// Daily sources must exclude github
	_, body = req(t, srv, "GET", "/daily/sources", nil)
	for _, s := range body["sources"].([]any) {
		if s == "github" {
			t.Fatal("github should not appear in Daily sources")
		}
	}

	// /repos endpoint
	_, body = req(t, srv, "GET", "/repos", nil)
	if _, ok := body["repos"]; !ok {
		t.Fatalf("no repos key: %v", body)
	}
}
