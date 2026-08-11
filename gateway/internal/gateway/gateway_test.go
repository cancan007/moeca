package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"orchestra/gateway/internal/config"
)

// echoUpstream reports back what it received so tests can assert header
// injection/stripping and path forwarding.
func echoUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out := map[string]string{
			"path":   r.URL.Path,
			"apikey": r.Header.Get("x-api-key"),
			"auth":   r.Header.Get("Authorization"),
			"query":  r.URL.RawQuery,
		}
		json.NewEncoder(w).Encode(out)
	}))
}

func baseConfig(upstream string) *config.Config {
	c := &config.Config{
		Listen:              "127.0.0.1:0",
		MaxBodyBytes:        8 << 20,
		AllowPrivateTargets: true, // tests proxy to loopback httptest servers
		AdminToken:          "admintok",
		Sessions:            map[string]config.Session{"tok": {ID: "s1"}},
		Services: map[string]config.Service{
			"echo": {
				Prefix:        "/echo/",
				Upstream:      upstream,
				Allowlist:     []string{"127.0.0.1"},
				InjectHeaders: map[string]string{"x-api-key": "${TEST_KEY}"},
				StripHeaders:  []string{"Authorization"},
			},
		},
	}
	return c
}

func do(t *testing.T, srv *httptest.Server, method, path string, headers map[string]string, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestInjectionAndStripping(t *testing.T) {
	t.Setenv("TEST_KEY", "secret-123")
	up := echoUpstream(t)
	defer up.Close()

	gw := New(baseConfig(up.URL), io.Discard, nil, nil)
	srv := httptest.NewServer(gw)
	defer srv.Close()

	resp := do(t, srv, "POST", "/echo/v1/thing?a=1", map[string]string{
		SessionHeader:   "tok",
		"Authorization": "Bearer client-secret-should-be-stripped",
	}, `{"hi":1}`)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got map[string]string
	json.NewDecoder(resp.Body).Decode(&got)
	if got["apikey"] != "secret-123" {
		t.Errorf("upstream x-api-key = %q, want injected secret", got["apikey"])
	}
	if got["auth"] != "" {
		t.Errorf("client Authorization leaked upstream: %q", got["auth"])
	}
	if got["path"] != "/v1/thing" {
		t.Errorf("forwarded path = %q, want /v1/thing", got["path"])
	}
	if got["query"] != "a=1" {
		t.Errorf("query = %q, want a=1", got["query"])
	}
}

func TestSessionAuth(t *testing.T) {
	up := echoUpstream(t)
	defer up.Close()
	gw := New(baseConfig(up.URL), io.Discard, nil, nil)
	srv := httptest.NewServer(gw)
	defer srv.Close()

	// missing token
	resp := do(t, srv, "GET", "/echo/x", nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: status = %d, want 401", resp.StatusCode)
	}
	// bad token
	resp = do(t, srv, "GET", "/echo/x", map[string]string{SessionHeader: "nope"}, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("bad token: status = %d, want 401", resp.StatusCode)
	}
}

func TestUnknownRoute(t *testing.T) {
	up := echoUpstream(t)
	defer up.Close()
	gw := New(baseConfig(up.URL), io.Discard, nil, nil)
	srv := httptest.NewServer(gw)
	defer srv.Close()

	resp := do(t, srv, "GET", "/unknown/x", map[string]string{SessionHeader: "tok"}, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestDynamicFetchAllowlist(t *testing.T) {
	up := echoUpstream(t)
	defer up.Close()
	cfg := &config.Config{
		MaxBodyBytes:        1 << 20,
		AllowPrivateTargets: true,
		Sessions:            map[string]config.Session{"tok": {ID: "s1"}},
		Services: map[string]config.Service{
			"fetch": {Prefix: "/fetch/", Upstream: "", Allowlist: []string{"127.0.0.1"}},
		},
	}
	gw := New(cfg, io.Discard, nil, nil)
	srv := httptest.NewServer(gw)
	defer srv.Close()

	// disallowed host -> 403
	resp := do(t, srv, "GET", "/fetch/", map[string]string{SessionHeader: "tok", TargetHeader: "https://evil.example/x"}, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("disallowed host: status = %d, want 403", resp.StatusCode)
	}
	// missing target header -> 400
	resp = do(t, srv, "GET", "/fetch/", map[string]string{SessionHeader: "tok"}, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing target: status = %d, want 400", resp.StatusCode)
	}
	// allowed host -> proxied 200
	resp = do(t, srv, "GET", "/fetch/", map[string]string{SessionHeader: "tok", TargetHeader: up.URL + "/ok"}, "")
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("allowed host: status = %d, want 200", resp.StatusCode)
	}
}

func TestRateLimit(t *testing.T) {
	up := echoUpstream(t)
	defer up.Close()
	cfg := baseConfig(up.URL)
	svc := cfg.Services["echo"]
	svc.RateLimit = config.RateLimit{RPS: 1, Burst: 1}
	cfg.Services["echo"] = svc

	fixed := time.Unix(1700000000, 0)
	gw := New(cfg, io.Discard, func() time.Time { return fixed }, nil) // clock frozen
	srv := httptest.NewServer(gw)
	defer srv.Close()

	h := map[string]string{SessionHeader: "tok"}
	r1 := do(t, srv, "GET", "/echo/a", h, "")
	r1.Body.Close()
	if r1.StatusCode != 200 {
		t.Fatalf("first request status = %d, want 200", r1.StatusCode)
	}
	r2 := do(t, srv, "GET", "/echo/a", h, "")
	r2.Body.Close()
	if r2.StatusCode != http.StatusTooManyRequests {
		t.Errorf("second request status = %d, want 429", r2.StatusCode)
	}
}

func TestBudgetExceeded(t *testing.T) {
	up := echoUpstream(t)
	defer up.Close()
	cfg := baseConfig(up.URL)
	svc := cfg.Services["echo"]
	svc.Budget = config.Budget{MaxTokensPerSession: 1}
	cfg.Services["echo"] = svc

	gw := New(cfg, io.Discard, nil, nil)
	srv := httptest.NewServer(gw)
	defer srv.Close()

	h := map[string]string{SessionHeader: "tok"}
	r1 := do(t, srv, "POST", "/echo/a", h, strings.Repeat("x", 100)) // spends > 1 token
	r1.Body.Close()
	if r1.StatusCode != 200 {
		t.Fatalf("first status = %d, want 200", r1.StatusCode)
	}
	r2 := do(t, srv, "GET", "/echo/a", h, "")
	r2.Body.Close()
	if r2.StatusCode != http.StatusPaymentRequired {
		t.Errorf("over-budget status = %d, want 402", r2.StatusCode)
	}
}

func TestStreamingPreserved(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for i := 0; i < 3; i++ {
			fmt.Fprintf(w, "data: chunk-%d\n\n", i)
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	defer up.Close()

	gw := New(baseConfig(up.URL), io.Discard, nil, nil)
	srv := httptest.NewServer(gw)
	defer srv.Close()

	resp := do(t, srv, "GET", "/echo/stream", map[string]string{SessionHeader: "tok"}, "")
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	for i := 0; i < 3; i++ {
		if !strings.Contains(string(body), fmt.Sprintf("chunk-%d", i)) {
			t.Errorf("streamed body missing chunk-%d: %q", i, body)
		}
	}
}

func TestBodySizeLimit(t *testing.T) {
	up := echoUpstream(t)
	defer up.Close()
	cfg := baseConfig(up.URL)
	cfg.MaxBodyBytes = 16
	gw := New(cfg, io.Discard, nil, nil)
	srv := httptest.NewServer(gw)
	defer srv.Close()

	resp := do(t, srv, "POST", "/echo/a", map[string]string{SessionHeader: "tok"}, strings.Repeat("x", 1000))
	resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Errorf("oversized body was accepted (status 200), want an error status")
	}
}

func TestGithubWriteAuthz(t *testing.T) {
	up := echoUpstream(t)
	defer up.Close()
	cfg := &config.Config{
		MaxBodyBytes:        8 << 20,
		AllowPrivateTargets: true,
		Sessions:            map[string]config.Session{"tok": {ID: "s1"}},
		Services: map[string]config.Service{
			"github": {
				Prefix:            "/github/",
				Upstream:          up.URL,
				Allowlist:         []string{"127.0.0.1"},
				ProtectedBranches: []string{"main", "master", "develop"},
				WriteAllow: []config.WriteRule{
					{Methods: []string{"POST"}, Path: "/repos/*/*/pulls"},
					{Methods: []string{"POST"}, Path: "/repos/*/*/git/refs"},
					{Methods: []string{"POST"}, Path: "/repos/*/*/issues/*/comments"},
				},
			},
		},
	}
	gw := New(cfg, io.Discard, nil, nil)
	srv := httptest.NewServer(gw)
	defer srv.Close()
	h := map[string]string{SessionHeader: "tok"}

	cases := []struct {
		name, method, path, body string
		want                     int
	}{
		{"read repo", "GET", "/github/repos/o/r", "", 200},
		{"create PR", "POST", "/github/repos/o/r/pulls", `{"title":"x","head":"f","base":"main"}`, 200},
		{"create feature branch", "POST", "/github/repos/o/r/git/refs", `{"ref":"refs/heads/feature-x","sha":"abc"}`, 200},
		{"create issue comment", "POST", "/github/repos/o/r/issues/5/comments", `{"body":"hi"}`, 200},
		{"create protected branch", "POST", "/github/repos/o/r/git/refs", `{"ref":"refs/heads/main","sha":"abc"}`, http.StatusForbidden},
		{"merge PR", "PUT", "/github/repos/o/r/pulls/1/merge", `{}`, http.StatusForbidden},
		{"direct commit to contents", "PUT", "/github/repos/o/r/contents/file.txt", `{"message":"m","content":"eA=="}`, http.StatusForbidden},
		{"delete branch ref", "DELETE", "/github/repos/o/r/git/refs/heads/main", "", http.StatusForbidden},
	}
	for _, c := range cases {
		resp := do(t, srv, c.method, c.path, h, c.body)
		resp.Body.Close()
		if resp.StatusCode != c.want {
			t.Errorf("%s: %s %s status = %d, want %d", c.name, c.method, c.path, resp.StatusCode, c.want)
		}
	}
}

func TestWriteUncontrolledServiceAllowsPost(t *testing.T) {
	// A service without WriteAllow (e.g. anthropic) must still accept POST.
	up := echoUpstream(t)
	defer up.Close()
	gw := New(baseConfig(up.URL), io.Discard, nil, nil) // echo service has no WriteAllow
	srv := httptest.NewServer(gw)
	defer srv.Close()
	resp := do(t, srv, "POST", "/echo/v1/messages", map[string]string{SessionHeader: "tok"}, `{"m":1}`)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("uncontrolled POST status = %d, want 200", resp.StatusCode)
	}
}

func TestProviderAdminAPI(t *testing.T) {
	up := echoUpstream(t)
	defer up.Close()
	cfg := &config.Config{
		MaxBodyBytes:        8 << 20,
		AllowPrivateTargets: true, // echo upstream is loopback
		AdminToken:          "admin-secret",
		Sessions:            map[string]config.Session{"tok": {ID: "s1"}},
		Services: map[string]config.Service{
			"openai": {
				Kind: "model", Prefix: "/openai/", Upstream: up.URL,
				Allowlist:     []string{"127.0.0.1"},
				Models:        []string{"gpt-4o"},
				InjectHeaders: map[string]string{"Authorization": "Bearer ${SECRET}"},
			},
		},
	}
	gw := New(cfg, io.Discard, nil, nil)
	srv := httptest.NewServer(gw)
	defer srv.Close()

	admin := map[string]string{AdminHeader: "admin-secret"}

	// admin GET requires the admin token; session token (sandbox-equivalent) is not enough.
	if resp := do(t, srv, "GET", "/_gateway/providers", nil, ""); resp.StatusCode != 401 {
		t.Errorf("providers w/o admin = %d, want 401", resp.StatusCode)
	}
	if resp := do(t, srv, "GET", "/_gateway/providers", map[string]string{SessionHeader: "tok"}, ""); resp.StatusCode != 401 {
		t.Errorf("providers w/ session token = %d, want 401 (session != admin)", resp.StatusCode)
	}

	// set the openai secret via admin.
	if resp := do(t, srv, "PUT", "/_gateway/providers/secret", admin, `{"name":"openai","value":"sk-live-123"}`); resp.StatusCode != 200 {
		t.Fatalf("set secret = %d, want 200", resp.StatusCode)
	}

	// GET providers (admin): openai present, hasSecret true, raw secret NOT leaked.
	resp := do(t, srv, "GET", "/_gateway/providers", admin, "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	s := string(body)
	if !strings.Contains(s, `"name":"openai"`) || !strings.Contains(s, `"hasSecret":true`) {
		t.Errorf("providers list missing openai/hasSecret: %s", s)
	}
	if strings.Contains(s, "sk-live-123") {
		t.Errorf("SECRET LEAKED in providers list: %s", s)
	}

	// proxy: the injected Authorization carries the secret to the upstream.
	pr := do(t, srv, "POST", "/openai/v1/chat/completions", map[string]string{SessionHeader: "tok"}, `{"m":1}`)
	var got map[string]string
	json.NewDecoder(pr.Body).Decode(&got)
	pr.Body.Close()
	if got["auth"] != "Bearer sk-live-123" {
		t.Errorf("upstream Authorization = %q, want injected secret", got["auth"])
	}

	// upsert a brand-new provider, then route to it.
	if resp := do(t, srv, "PUT", "/_gateway/providers", admin, `{"name":"custom","kind":"model","prefix":"/custom/","upstream":"`+up.URL+`","allowlist":["127.0.0.1"]}`); resp.StatusCode != 200 {
		t.Fatalf("upsert provider = %d, want 200", resp.StatusCode)
	}
	if resp := do(t, srv, "GET", "/custom/x", map[string]string{SessionHeader: "tok"}, ""); resp.StatusCode != 200 {
		t.Errorf("route to upserted provider = %d, want 200", resp.StatusCode)
	}
}

func TestAdminHashAuth(t *testing.T) {
	sum := sha256.Sum256([]byte("raw-admin-token"))
	cfg := &config.Config{
		MaxBodyBytes:     1 << 20,
		AdminTokenSHA256: hex.EncodeToString(sum[:]), // gateway holds ONLY the hash
		Sessions:         map[string]config.Session{"tok": {ID: "s1"}},
		Services:         map[string]config.Service{"anthropic": {Kind: "model", Prefix: "/anthropic/", Upstream: "https://api.anthropic.com", Allowlist: []string{"api.anthropic.com"}}},
	}
	gw := New(cfg, io.Discard, nil, nil)
	if gw.adminToken != "" {
		t.Errorf("gateway should not hold the raw admin token, got %q", gw.adminToken)
	}
	srv := httptest.NewServer(gw)
	defer srv.Close()

	// correct raw token (hashed by the gateway) succeeds; wrong token 401.
	if resp := do(t, srv, "GET", "/_gateway/providers", map[string]string{AdminHeader: "raw-admin-token"}, ""); resp.StatusCode != 200 {
		t.Errorf("valid admin token = %d, want 200", resp.StatusCode)
	}
	if resp := do(t, srv, "GET", "/_gateway/providers", map[string]string{AdminHeader: "wrong"}, ""); resp.StatusCode != 401 {
		t.Errorf("wrong admin token = %d, want 401", resp.StatusCode)
	}
}

func TestSSRFDeny(t *testing.T) {
	cfg := &config.Config{
		MaxBodyBytes: 1 << 20,
		// AllowPrivateTargets defaults false => block private/host targets.
		Sessions: map[string]config.Session{"tok": {ID: "s1"}},
		Services: map[string]config.Service{
			"fetch": {Prefix: "/fetch/", Upstream: "", Allowlist: []string{"127.0.0.1", "host.docker.internal"}},
		},
	}
	gw := New(cfg, io.Discard, nil, nil)
	srv := httptest.NewServer(gw)
	defer srv.Close()
	h := func(target string) map[string]string {
		return map[string]string{SessionHeader: "tok", TargetHeader: target}
	}
	// caller-supplied targets that reach host-local assets are blocked (after allowlist).
	if resp := do(t, srv, "GET", "/fetch/", h("http://127.0.0.1:9000/x"), ""); resp.StatusCode != http.StatusForbidden {
		t.Errorf("loopback target = %d, want 403", resp.StatusCode)
	}
	if resp := do(t, srv, "GET", "/fetch/", h("http://host.docker.internal:8788/x"), ""); resp.StatusCode != http.StatusForbidden {
		t.Errorf("host.docker.internal target = %d, want 403", resp.StatusCode)
	}
}

func TestHealthAndStatus(t *testing.T) {
	up := echoUpstream(t)
	defer up.Close()
	gw := New(baseConfig(up.URL), io.Discard, nil, nil)
	srv := httptest.NewServer(gw)
	defer srv.Close()

	resp := do(t, srv, "GET", "/_gateway/health", nil, "")
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("health status = %d, want 200", resp.StatusCode)
	}
	// status is admin-gated: no token and session-only are both rejected.
	resp = do(t, srv, "GET", "/_gateway/status", nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status without auth = %d, want 401", resp.StatusCode)
	}
	resp = do(t, srv, "GET", "/_gateway/status", map[string]string{SessionHeader: "tok"}, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status with session token = %d, want 401 (admin required)", resp.StatusCode)
	}
	resp = do(t, srv, "GET", "/_gateway/status", map[string]string{AdminHeader: "admintok"}, "")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status with admin token = %d, want 200", resp.StatusCode)
	}
}

func TestContentCaptureAndAttribution(t *testing.T) {
	up := echoUpstream(t)
	defer up.Close()
	gw := New(baseConfig(up.URL), io.Discard, nil, nil)
	srv := httptest.NewServer(gw)
	defer srv.Close()

	// A proxied request with run/stage headers + a JSON body carrying a model.
	resp := do(t, srv, "POST", "/echo/v1/messages", map[string]string{
		SessionHeader: "tok", RunHeader: "run-1", StageHeader: "planner",
	}, `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`)
	resp.Body.Close()

	// Logs are admin-gated; session token must NOT read them (content protection).
	if r := do(t, srv, "GET", "/_gateway/logs", map[string]string{SessionHeader: "tok"}, ""); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("logs with session token = %d, want 401", r.StatusCode)
	}
	lr := do(t, srv, "GET", "/_gateway/logs", map[string]string{AdminHeader: "admintok"}, "")
	defer lr.Body.Close()
	var out struct {
		Logs []struct {
			Run, Stage, Model, ReqBody, RespBody string
		} `json:"logs"`
	}
	json.NewDecoder(lr.Body).Decode(&out)
	if len(out.Logs) == 0 {
		t.Fatalf("no logs captured")
	}
	got := out.Logs[0]
	if got.Run != "run-1" || got.Stage != "planner" {
		t.Errorf("attribution = run:%q stage:%q, want run-1/planner", got.Run, got.Stage)
	}
	if got.Model != "gpt-4o" {
		t.Errorf("model = %q, want gpt-4o", got.Model)
	}
	if !strings.Contains(got.ReqBody, "hello") {
		t.Errorf("reqBody not captured: %q", got.ReqBody)
	}
	if got.RespBody == "" {
		t.Errorf("respBody not captured")
	}
}

// A request the provider REFUSED is not billed by the provider, and must not be
// billed here. One 8 MB request rejected with "prompt is too long" was charged
// two million tokens from its byte estimate, exhausting the session's whole
// budget and blocking every run after it — for work no upstream ever did.
func TestARefusedRequestIsNotCharged(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"prompt is too long"}`))
	}))
	defer up.Close()

	cfg := baseConfig(up.URL)
	svc := cfg.Services["echo"]
	svc.Budget = config.Budget{MaxTokensPerSession: 1000}
	cfg.Services["echo"] = svc

	gw := New(cfg, io.Discard, nil, nil)
	srv := httptest.NewServer(gw)
	defer srv.Close()

	h := map[string]string{SessionHeader: "tok"}
	// Big enough that its byte estimate alone would blow the ceiling.
	r1 := do(t, srv, "POST", "/echo/a", h, strings.Repeat("x", 20000))
	r1.Body.Close()
	if r1.StatusCode != http.StatusBadRequest {
		t.Fatalf("first status = %d, want the upstream's 400", r1.StatusCode)
	}
	// The next request must still be allowed: nothing was spent.
	r2 := do(t, srv, "POST", "/echo/a", h, "hi")
	r2.Body.Close()
	if r2.StatusCode == http.StatusPaymentRequired {
		t.Error("a refused request consumed the budget")
	}
	if spent := gw.budget.total("s1|echo"); spent != 0 {
		t.Errorf("spent = %d after a refused request, want 0", spent)
	}
}

// A failure that may have generated tokens before it broke still pays. Only the
// provable "the provider declined to process this" case is free.
func TestAServerErrorStillCharges(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("boom"))
	}))
	defer up.Close()

	gw := New(baseConfig(up.URL), io.Discard, nil, nil)
	srv := httptest.NewServer(gw)
	defer srv.Close()

	r := do(t, srv, "POST", "/echo/a", map[string]string{SessionHeader: "tok"}, strings.Repeat("x", 4000))
	r.Body.Close()
	if spent := gw.budget.total("s1|echo"); spent == 0 {
		t.Error("a 5xx was treated as free; a dropped generation can still have been billed")
	}
}
