package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orchestra/gateway/internal/config"
)

// What the token budget counts.
//
// It was counting bytes: a compressed response made the real usage unreadable,
// so every model call fell back to the estimate — and an artifact download was
// charged as if a model had read the file. Three generated files exhausted a
// two-million-token session.

func modelGateway(t *testing.T, h http.HandlerFunc) (*Gateway, *httptest.Server) {
	t.Helper()
	up := httptest.NewServer(h)
	t.Cleanup(up.Close)
	cfg := baseConfig(up.URL)
	svc := cfg.Services["echo"]
	svc.Kind = "model"
	cfg.Services["echo"] = svc
	gw := New(cfg, io.Discard, nil, nil)
	srv := httptest.NewServer(gw)
	t.Cleanup(srv.Close)
	return gw, srv
}

// The provider's own numbers win over the estimate — which is the whole point
// of asking for them.
func TestRealUsageIsChargedNotTheByteEstimate(t *testing.T) {
	gw, srv := modelGateway(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7}}`))
	})
	// A large request whose byte estimate would be far bigger than the truth.
	res := do(t, srv, "POST", "/echo/a", map[string]string{SessionHeader: "tok"}, strings.Repeat("x", 40000))
	res.Body.Close()

	if spent := gw.budget.total("s1|echo"); spent != 18 {
		t.Errorf("charged %d, want the reported 18 tokens", spent)
	}
}

// A model response arrives readable, or the numbers above can never be found.
func TestModelRequestsAskForAnUncompressedResponse(t *testing.T) {
	var got string
	_, srv := modelGateway(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Accept-Encoding")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	})
	req, _ := http.NewRequest("POST", srv.URL+"/echo/a", strings.NewReader("{}"))
	req.Header.Set(SessionHeader, "tok")
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	if got != "identity" {
		t.Errorf("upstream was asked for %q, want identity so usage can be read", got)
	}
}

// A generated file is not tokens. Charging it as such is a category mistake,
// and it was enough to exhaust a budget on three artifacts.
func TestABinaryDownloadIsNotChargedAsTokens(t *testing.T) {
	gw, srv := modelGateway(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Write(make([]byte, 2<<20)) // 2 MB
	})
	res := do(t, srv, "GET", "/echo/v/content", map[string]string{SessionHeader: "tok"}, "")
	res.Body.Close()

	if spent := gw.budget.total("s1|echo"); spent > 10 {
		t.Errorf("a 2 MB video charged %d tokens", spent)
	}
}

// Text still pays the estimate when the provider reports nothing: an upstream
// that hides its usage must not become free.
func TestATextResponseWithoutUsageStillPays(t *testing.T) {
	gw, srv := modelGateway(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":"no usage here"}`))
	})
	res := do(t, srv, "POST", "/echo/a", map[string]string{SessionHeader: "tok"}, strings.Repeat("x", 4000))
	res.Body.Close()

	if spent := gw.budget.total("s1|echo"); spent < 100 {
		t.Errorf("charged %d for a 4 KB text exchange, want the byte estimate", spent)
	}
}

func TestIsTextual(t *testing.T) {
	for _, ct := range []string{"application/json", "text/plain; charset=utf-8", "text/event-stream", "", "application/xml"} {
		if !isTextual(ct) {
			t.Errorf("isTextual(%q) = false", ct)
		}
	}
	for _, ct := range []string{"video/mp4", "image/png", "audio/mpeg", "application/octet-stream"} {
		if isTextual(ct) {
			t.Errorf("isTextual(%q) = true", ct)
		}
	}
}

var _ = config.Budget{}

// An embeddings call is charged what it consumed, not the size of the vectors
// it hands back. Requiring both counts sent it to the byte estimate — and
// asking upstreams for uncompressed responses (so that usage could be read at
// all) had quadrupled the very number that estimate was reading.
func TestAnEmbeddingsCallIsChargedItsRealUsage(t *testing.T) {
	// A response shaped like the real one: a megabyte of vectors, and a usage
	// object with no completion side.
	vectors := strings.Repeat(`-0.0123456,`, 90000)
	gw, srv := modelGateway(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"embedding":[` + vectors + `0]}],` +
			`"usage":{"prompt_tokens":10232,"total_tokens":10232}}`))
	})
	res := do(t, srv, "POST", "/echo/v1/embeddings", map[string]string{SessionHeader: "tok"}, strings.Repeat("x", 30000))
	// Drained, not just closed: usage sits at the END of the body, so a client
	// that hangs up early leaves the gateway nothing to read it from.
	io.Copy(io.Discard, res.Body)
	res.Body.Close()

	if spent := gw.budget.total("s1|echo"); spent != 10232 {
		t.Errorf("charged %d, want the reported 10232 — the byte estimate would be ~250000", spent)
	}
}
