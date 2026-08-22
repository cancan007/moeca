package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A body over the cap is this gateway's decision, and the answer has to say so.
// Reported as "upstream error" it sent a real investigation to the provider,
// which had never seen the request: the gateway cut it off mid-send.
func TestAnOversizedBodyIsReportedAsTooLarge(t *testing.T) {
	reached := false
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		reached = true
		w.Write([]byte(`{"ok":true}`))
	}))
	defer up.Close()

	cfg := baseConfig(up.URL)
	cfg.MaxBodyBytes = 1 << 10
	srv := httptest.NewServer(New(cfg, io.Discard, nil, nil))
	defer srv.Close()

	res := do(t, srv, "POST", "/echo/a", map[string]string{SessionHeader: "tok"}, strings.Repeat("x", 4<<10))
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()

	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413; body = %s", res.StatusCode, body)
	}
	// The message has to name the limit, or the operator cannot act on it.
	if !strings.Contains(string(body), "1 KiB") {
		t.Errorf("error does not name the limit: %s", body)
	}
	_ = reached
}

// A genuine upstream failure still reads as one.
func TestARealUpstreamFailureIsStillAnUpstreamError(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	up.Close() // refuse connections

	srv := httptest.NewServer(New(baseConfig(up.URL), io.Discard, nil, nil))
	defer srv.Close()

	res := do(t, srv, "POST", "/echo/a", map[string]string{SessionHeader: "tok"}, "{}")
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()

	if res.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", res.StatusCode)
	}
	if !strings.Contains(string(body), "upstream") {
		t.Errorf("body = %s", body)
	}
}

func TestByteSize(t *testing.T) {
	for _, c := range []struct {
		n    int64
		want string
	}{{512, "512 B"}, {2 << 10, "2 KiB"}, {8 << 20, "8 MiB"}} {
		if got := byteSize(c.n); got != c.want {
			t.Errorf("byteSize(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}
