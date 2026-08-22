// Package gateway implements Orchestra's single Go egress proxy.
//
// One gateway fronts every upstream an agent may call:
//
//	/anthropic/*  ->  api.anthropic.com   (x-api-key injected)
//	/github/*     ->  api.github.com      (bearer token injected)
//	/fetch/*      ->  allowlisted hosts   (dynamic target)
//	/registry/*   ->  internal registry
//
// Each request passes: session auth -> routing -> body-size limit -> rate limit
// -> token/cost budget -> allowlist -> credential injection -> streaming proxy,
// and is recorded as a structured access log. Secrets are read from the gateway
// environment and injected here, so sandboxed agents never hold them.
package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	"orchestra/gateway/internal/config"
)

// SessionHeader carries the caller's session token.
const SessionHeader = "X-Orchestra-Session"

// TargetHeader supplies the absolute URL for dynamic (/fetch/*) services.
const TargetHeader = "X-Orchestra-Target"

// RunHeader / StageHeader carry the orchestration run/stage ids for attribution
// in the monitoring plane. Scrubbed before the upstream call.
const (
	RunHeader   = "X-Orchestra-Run"
	StageHeader = "X-Orchestra-Stage"
)

// GroupsHeader states which knowledge groups the caller's session may retrieve.
//
// Unlike RunHeader and StageHeader, this is not attribution the caller supplies
// — it is an authorization statement the gateway makes about the caller, so an
// inbound value is always discarded and replaced with the session's own groups.
// It is injected only into services that ask for it via the ${GROUPS} template,
// because sending a run's group names to a third-party model provider would
// leak the shape of the workspace for no benefit.
const GroupsHeader = "X-Orchestra-Groups"

// Gateway is the HTTP handler implementing the proxy.
type Gateway struct {
	cfg            *config.Config
	reg            *providerRegistry
	adminToken     string // raw token (dev/test); prefer the hash
	adminTokenHash string // hex sha256 of the admin token (production)
	log            *logger
	limiters       *limiterSet
	budget         *budgetLedger
	// sessions are the ones minted per run at runtime, alongside cfg.Sessions.
	// A run's retrieval scope has nowhere else to live: the caller may not name
	// its own groups, so the session it presents has to carry them.
	sessions  *sessionRegistry
	transport http.RoundTripper
	now       func() time.Time
}

// New builds a Gateway. logW receives JSON access lines; nowFn/transport are
// injectable for tests (pass nil for defaults).
func New(cfg *config.Config, logW io.Writer, nowFn func() time.Time, transport http.RoundTripper) *Gateway {
	if nowFn == nil {
		nowFn = time.Now
	}
	if transport == nil {
		transport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}
	}
	adminToken := cfg.AdminToken
	if adminToken == "" {
		adminToken = os.Getenv("ORCHESTRA_ADMIN_TOKEN")
	}
	adminHash := cfg.AdminTokenSHA256
	if adminHash == "" {
		adminHash = os.Getenv("ORCHESTRA_ADMIN_TOKEN_SHA256")
	}
	return &Gateway{
		cfg:            cfg,
		reg:            newRegistry(cfg.Services),
		adminToken:     adminToken,
		adminTokenHash: strings.ToLower(strings.TrimSpace(adminHash)),
		log:            newLogger(logW),
		limiters:       newLimiterSet(nowFn),
		budget:         newBudgetLedger(),
		sessions:       newSessionRegistry(),
		transport:      transport,
		now:            nowFn,
	}
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/_gateway/health":
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	case "/_gateway/status":
		g.handleStatus(w, r)
		return
	case "/_gateway/logs":
		g.handleLogs(w, r)
		return
	case "/_gateway/metrics":
		g.handleMetrics(w, r)
		return
	case "/_gateway/audit/verify":
		g.handleAuditVerify(w, r)
		return
	case "/_gateway/providers":
		g.handleProviders(w, r)
		return
	case "/_gateway/providers/secret":
		g.handleProviderSecret(w, r)
		return
	case AdminSessionsPath:
		g.handleSessions(w, r)
		return
	}

	// 1. session auth
	session, groups, ok := g.authenticate(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errBody("invalid or missing session"))
		return
	}

	// 2. route to a service by path prefix
	name, svc, ok := g.route(r.URL.Path)
	if !ok {
		writeJSON(w, http.StatusNotFound, errBody("no service for path"))
		return
	}

	key := session + "|" + name

	// 3. body-size limit
	r.Body = http.MaxBytesReader(w, r.Body, g.cfg.MaxBodyBytes)

	// 4. rate limit
	if !g.limiters.allow(key, svc.RateLimit.RPS, svc.RateLimit.Burst) {
		g.log.write(accessLog{RequestID: requestID(), Session: session, Service: name, Method: r.Method, Path: r.URL.Path, Status: http.StatusTooManyRequests, Err: "rate_limited"})
		writeJSON(w, http.StatusTooManyRequests, errBody("rate limit exceeded"))
		return
	}

	// 5. token/cost budget precheck
	if g.budget.exceeded(key, svc.Budget.MaxTokensPerSession) {
		g.log.write(accessLog{RequestID: requestID(), Session: session, Service: name, Method: r.Method, Path: r.URL.Path, Status: http.StatusPaymentRequired, Err: "budget_exceeded"})
		writeJSON(w, http.StatusPaymentRequired, errBody("token/cost budget exceeded for session"))
		return
	}

	// 6. resolve + allowlist-check the upstream target
	target, err := g.resolveTarget(svc, r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(err.Error()))
		return
	}
	if !g.hostAllowed(svc, target.Host) {
		g.log.write(accessLog{RequestID: requestID(), Session: session, Service: name, Method: r.Method, Path: r.URL.Path, Upstream: target.Host, Status: http.StatusForbidden, Err: "host_not_allowed"})
		writeJSON(w, http.StatusForbidden, errBody("upstream host not in allowlist: "+target.Host))
		return
	}

	// 6b. SSRF / host-reach deny: refuse to forward to loopback/RFC1918/
	// link-local/docker-host targets so the gateway (or a caller-supplied /fetch
	// target) can't reach host-side assets. Dynamic (caller-supplied) targets are
	// fully resolved (DNS-rebinding defense); fixed admin-set upstreams get a
	// cheap literal check only.
	if !g.cfg.AllowPrivateTargets && g.targetBlocked(svc, target.Host) {
		g.log.write(accessLog{RequestID: requestID(), Session: session, Service: name, Method: r.Method, Path: r.URL.Path, Upstream: target.Host, Status: http.StatusForbidden, Err: "target_blocked"})
		writeJSON(w, http.StatusForbidden, errBody("forwarding to private/host-local target is not permitted: "+target.Host))
		return
	}

	// 6c. forbidden-command policy (network scope): an explicit host/CIDR blocklist
	// on top of the per-service allowlist.
	if rule, denied := g.cfg.NetworkDenied(target.Host); denied {
		g.log.write(accessLog{RequestID: requestID(), Session: session, Service: name, Method: r.Method, Path: r.URL.Path, Upstream: target.Host, Status: http.StatusForbidden, Err: "network_forbidden"})
		writeJSON(w, http.StatusForbidden, errBody("network target blocked by policy: "+rule.Pattern))
		return
	}

	// 6d. route authorization: some upstreams expose read routes that are
	// privileged in themselves (the indexer's /status and /graph enumerate every
	// source), so a service may name the paths it will forward at all. This runs
	// before the write check because that one lets every GET through by design.
	if !svc.PathAllowed(target.Path) {
		g.log.write(accessLog{RequestID: requestID(), Session: session, Service: name, Method: r.Method, Path: r.URL.Path, Upstream: target.Host, Status: http.StatusForbidden, Err: "path_not_allowed"})
		writeJSON(w, http.StatusForbidden, errBody("route not permitted for this service: "+target.Path))
		return
	}

	// 7. write authorization: deny-by-default mutating requests (e.g. a
	// sandboxed agent trying to merge a PR or push to a base branch via the
	// injected GitHub token). target.Path is the upstream-relative path.
	if !svc.WriteAllowed(r.Method, target.Path) {
		g.log.write(accessLog{RequestID: requestID(), Session: session, Service: name, Method: r.Method, Path: r.URL.Path, Upstream: target.Host, Status: http.StatusForbidden, Err: "write_forbidden"})
		writeJSON(w, http.StatusForbidden, errBody("write not permitted for this service: "+r.Method+" "+target.Path))
		return
	}
	// 7b. forbidden-command policy (command scope): an explicit blocklist of
	// HTTP operations agents make through the gateway (e.g. destructive deletes).
	if rule, denied := g.cfg.CommandDenied(r.Method, target.Path); denied {
		g.log.write(accessLog{RequestID: requestID(), Session: session, Service: name, Method: r.Method, Path: r.URL.Path, Upstream: target.Host, Status: http.StatusForbidden, Err: "command_forbidden"})
		writeJSON(w, http.StatusForbidden, errBody("command blocked by policy: "+rule.Pattern))
		return
	}
	// 8. protected-branch guard: an allowed branch-creation must not target a
	// protected ref. Inspect only this one small-bodied route; streaming paths
	// (LLM responses) are never buffered.
	if reason, ok := g.checkProtectedRef(w, r, svc, target.Path); !ok {
		g.log.write(accessLog{RequestID: requestID(), Session: session, Service: name, Method: r.Method, Path: r.URL.Path, Upstream: target.Host, Status: http.StatusForbidden, Err: reason})
		writeJSON(w, http.StatusForbidden, errBody("protected branch: "+reason))
		return
	}

	g.proxy(w, r, session, groups, name, svc, target)
}

// checkProtectedRef guards the branch-creation route (POST …/git/refs) so an
// agent cannot create a protected branch. It applies only to that single
// small-bodied route; for every other request it is a no-op that leaves the
// body untouched. When it does inspect, it buffers the (already size-capped)
// body, checks the "ref" field, and restores the body for forwarding.
//
// Returns ("", true) to proceed, or (reason, false) to reject.
func (g *Gateway) checkProtectedRef(w http.ResponseWriter, r *http.Request, svc config.Service, relPath string) (string, bool) {
	if len(svc.ProtectedBranches) == 0 || !strings.EqualFold(r.Method, http.MethodPost) || !strings.HasSuffix(relPath, "/git/refs") {
		return "", true
	}
	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		return "protected_ref_read_failed", false
	}
	// Restore the body so the reverse proxy forwards it verbatim.
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))

	var payload struct {
		Ref string `json:"ref"`
	}
	if json.Unmarshal(body, &payload) == nil && payload.Ref != "" && svc.IsProtectedRef(payload.Ref) {
		return "protected_ref", false
	}
	return "", true
}

// proxy performs the credential-injecting, streaming reverse proxy and logs it.
func (g *Gateway) proxy(w http.ResponseWriter, r *http.Request, session string, groups []string, name string, svc config.Service, target *url.URL) {
	reqID := requestID()
	start := g.now()
	capture := g.cfg.Capture()
	rec := &recorder{ResponseWriter: w, status: http.StatusOK, capture: capture, trackUsage: svc.Kind == "model"}

	// Attribution + request-content capture for the monitoring plane. The body
	// is buffered (bounded by MaxBodyBytes) then restored so forwarding is
	// unaffected; only the first maxCaptureBytes are retained.
	run := r.Header.Get(RunHeader)
	stage := r.Header.Get(StageHeader)
	var reqBody, model string
	if capture {
		if raw, err := io.ReadAll(r.Body); err == nil {
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(raw))
			r.ContentLength = int64(len(raw))
			reqBody = cap8k(raw)
			model = extractModel(raw)
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), g.cfg.Timeout())
	defer cancel()
	r = r.WithContext(ctx)

	var proxyErr string
	rp := &httputil.ReverseProxy{
		FlushInterval: -1, // flush immediately for SSE / token streaming
		Transport:     g.transport,
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = target.Path
			req.URL.RawQuery = target.RawQuery
			req.Host = target.Host

			// Ask a model upstream for an uncompressed answer.
			//
			// The token budget is meant to be reconciled against the usage the
			// provider reports, and that reconciliation reads the response. A
			// gzipped body is unreadable here — the last bytes of a compressed
			// stream cannot be decoded on their own — so every model call was
			// silently falling back to the byte estimate, which is a fair proxy
			// for prose and nonsense for anything else: one request carrying an
			// image the agent had asked to look at was charged half a million
			// tokens for what the provider billed as a couple of thousand.
			//
			// Only model services, and only the response: a few kilobytes of
			// JSON uncompressed is a small price for accounting that means
			// something.
			if svc.Kind == "model" {
				req.Header.Set("Accept-Encoding", "identity")
			}

			// scrub gateway/control + client-supplied sensitive headers
			req.Header.Del(SessionHeader)
			req.Header.Del(TargetHeader)
			req.Header.Del(RunHeader)
			req.Header.Del(StageHeader)
			// A caller must never speak for its own entitlements: whatever it
			// sent here is dropped, and only the injection below can put it back.
			req.Header.Del(GroupsHeader)
			for _, h := range svc.StripHeaders {
				req.Header.Del(h)
			}
			// inject credentials: ${SECRET} resolves to this provider's in-memory
			// secret (set via the admin API), ${VAR} to the gateway environment,
			// ${GROUPS} to the session's knowledge groups.
			for k, v := range svc.InjectHeaders {
				if val, send := g.resolveInject(name, v, groups); send {
					req.Header.Set(k, val)
				}
			}
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			proxyErr = err.Error()
			// A body over the cap surfaces here, because the limit is enforced
			// while the proxy is already streaming the request upstream. Saying
			// "upstream error" for it blames the provider for something this
			// gateway did — and that is what a real run cost: a 10.9 MB request
			// was reported as a provider failure, so the search went upstream
			// and found nothing wrong there.
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeJSON(w, http.StatusRequestEntityTooLarge,
					errBody(fmt.Sprintf("request body exceeds this gateway's %s limit", byteSize(g.cfg.MaxBodyBytes))))
				return
			}
			writeJSON(w, http.StatusBadGateway, errBody("upstream error"))
		},
	}
	rp.ServeHTTP(rec, r)

	reqBytes := r.ContentLength
	if reqBytes < 0 {
		reqBytes = 0
	}
	// Charge REAL token usage for model services when the response reported it;
	// otherwise fall back to the byte estimate. inTok/outTok are also logged for
	// the monitoring plane.
	charge := estimateTokens(reqBytes, rec.bytes)
	var inTok, outTok int
	if rec.trackUsage {
		if in, out, ok := extractUsage(rec.usageTail, name, isStream(rec.Header().Get("Content-Type"))); ok {
			inTok, outTok = in, out
			charge = int64(in + out)
		}
	}
	// A response that is not text is not tokens. Downloading a generated video
	// moved the ledger by half a million on the byte estimate alone, which
	// exhausted a two-million budget in three artifacts — for bytes no model
	// ever read. The budget counts what a model was asked to think about, so a
	// payload it will never see costs the nominal minimum instead.
	if !isTextual(rec.Header().Get("Content-Type")) && inTok+outTok == 0 {
		charge = 1
	}
	// A request the provider REFUSED is not billed by the provider, so it must
	// not be billed here either. Without this, the byte estimate stands in for
	// usage that never happened: one 8 MB request rejected with "prompt is too
	// long" was charged two million tokens and exhausted the session's entire
	// budget, blocking every run after it — for work no upstream ever did.
	//
	// Deliberately narrow. A 4xx with no reported usage is the provable case:
	// the provider looked at the request and declined it. A 5xx or a dropped
	// stream may well have generated tokens before it broke, so those keep
	// paying the estimate.
	key := session + "|" + name
	tokens := g.budget.total(key)
	if refused := rec.status >= 400 && rec.status < 500 && inTok+outTok == 0; !refused {
		tokens = g.budget.add(key, charge)
	}
	g.log.write(accessLog{
		RequestID: reqID, Session: session, Run: run, Stage: stage, Service: name, Model: model,
		Method: r.Method, Path: r.URL.Path, Upstream: target.Host,
		Status: rec.status, ReqBytes: reqBytes, RespBytes: rec.bytes,
		ReqBody: reqBody, RespBody: cap8k(rec.body),
		DurationMs: g.now().Sub(start).Milliseconds(),
		TokensEst:  tokens, InputTokens: inTok, OutputTokens: outTok, Err: proxyErr,
	})
}

// cap8k returns a UTF-8-safe string of up to maxCaptureBytes.
func cap8k(b []byte) string {
	if len(b) > maxCaptureBytes {
		b = b[:maxCaptureBytes]
	}
	return strings.ToValidUTF8(string(b), "")
}

// extractModel best-effort reads the "model" field from a JSON request body.
func extractModel(b []byte) string {
	var m struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(b, &m) == nil {
		return m.Model
	}
	return ""
}

// authenticate returns the session id and its knowledge groups for a valid
// token. When no sessions are configured, an anonymous session is allowed
// (local dev) with no group policy.
func (g *Gateway) authenticate(r *http.Request) (id string, groups []string, ok bool) {
	tok := r.Header.Get(SessionHeader)
	// A session minted for one run wins over the static ones: it is the only
	// place a per-run retrieval scope can come from, and it must work even in a
	// configuration that defines no static sessions at all.
	if tok != "" {
		if s, found := g.sessions.get(tok); found {
			if s.ID != "" {
				return s.ID, s.Groups, true
			}
			return "run", s.Groups, true
		}
	}
	if len(g.cfg.Sessions) == 0 {
		return "anonymous", nil, true
	}
	if tok == "" {
		return "", nil, false
	}
	if s, found := g.cfg.Sessions[tok]; found {
		if s.ID != "" {
			return s.ID, s.Groups, true
		}
		return "session", s.Groups, true
	}
	return "", nil, false
}

// route finds the service whose prefix matches the path (longest prefix wins),
// reading from the live provider registry (so admin edits take effect).
func (g *Gateway) route(path string) (string, config.Service, bool) {
	return g.reg.route(path)
}

// resolveTarget computes the absolute upstream URL for a request.
func (g *Gateway) resolveTarget(svc config.Service, r *http.Request) (*url.URL, error) {
	if svc.Upstream == "" {
		// dynamic: the caller names the absolute target via header
		raw := r.Header.Get(TargetHeader)
		if raw == "" {
			return nil, fmt.Errorf("dynamic service requires %s header", TargetHeader)
		}
		u, err := url.Parse(raw)
		if err != nil || !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, fmt.Errorf("invalid target url")
		}
		return u, nil
	}
	base, err := url.Parse(svc.Upstream)
	if err != nil {
		return nil, fmt.Errorf("bad upstream config")
	}
	remainder := strings.TrimPrefix(r.URL.Path, strings.TrimSuffix(svc.Prefix, "/"))
	u := *base
	u.Path = singleJoinSlash(base.Path, remainder)
	u.RawQuery = r.URL.RawQuery
	return &u, nil
}

func (g *Gateway) hostAllowed(svc config.Service, host string) bool {
	if len(svc.Allowlist) > 0 {
		return config.HostAllowed(host, svc.Allowlist)
	}
	// fixed upstream with no explicit allowlist: only its own host is reachable
	return true
}

func (g *Gateway) handleStatus(w http.ResponseWriter, r *http.Request) {
	if !g.adminAuthed(r) {
		writeJSON(w, http.StatusUnauthorized, errBody("admin token required"))
		return
	}
	views := g.reg.list()
	services := make([]string, 0, len(views))
	for _, v := range views {
		services = append(services, v.Name)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"listen":      g.cfg.Listen,
		"services":    services,
		"spentTokens": g.budget.snapshot(),
	})
}

// AttachAudit gives the gateway a durable, tamper-evident audit sink. Access
// records are then persisted (hash-chained) in addition to the in-memory ring,
// and /_gateway/logs, /_gateway/metrics, /_gateway/audit/verify read from it.
func (g *Gateway) AttachAudit(s *AuditStore) { g.log.store = s }

// records returns up to limit access records, newest first — from the durable
// store when attached, otherwise the in-memory ring.
func (g *Gateway) records(limit int) []accessLog {
	if g.log.store != nil {
		if recs, err := g.log.store.recent(limit); err == nil {
			return recs
		}
	}
	return g.log.ring.snapshot()
}

// handleLogs returns the retained access records (most recent first). Session
// authenticated like /_gateway/status.
func (g *Gateway) handleLogs(w http.ResponseWriter, r *http.Request) {
	if !g.adminAuthed(r) {
		writeJSON(w, http.StatusUnauthorized, errBody("admin token required"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": g.records(500)})
}

// handleAuditVerify recomputes the audit hash chain and reports the first break.
func (g *Gateway) handleAuditVerify(w http.ResponseWriter, r *http.Request) {
	if !g.adminAuthed(r) {
		writeJSON(w, http.StatusUnauthorized, errBody("admin token required"))
		return
	}
	if g.log.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": 0, "note": "no durable audit store"})
		return
	}
	res, err := g.log.store.verify()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// serviceMetrics aggregates request/token counts for one service.
type serviceMetrics struct {
	Requests  int   `json:"requests"`
	TokensEst int64 `json:"tokensEst"`
}

// handleMetrics aggregates the ring buffer into totals and per-service counts.
func (g *Gateway) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if !g.adminAuthed(r) {
		writeJSON(w, http.StatusUnauthorized, errBody("admin token required"))
		return
	}
	logs := g.records(500)
	perService := map[string]*serviceMetrics{}
	sessions := map[string]struct{}{}
	var totalTokens int64
	for _, l := range logs {
		if l.Session != "" {
			sessions[l.Session] = struct{}{}
		}
		totalTokens += l.TokensEst
		m := perService[l.Service]
		if m == nil {
			m = &serviceMetrics{}
			perService[l.Service] = m
		}
		m.Requests++
		m.TokensEst += l.TokensEst
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"totalRequests":  len(logs),
		"totalTokensEst": totalTokens,
		"sessions":       len(sessions),
		"perService":     perService,
	})
}

// cors permits the local frontend (Vite dev server and the Tauri webview) to
// call this loopback service from the browser. Loopback-only, so this is safe.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, "+SessionHeader+", "+TargetHeader)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Run starts the HTTP server (blocking).
func (g *Gateway) Run() error {
	srv := &http.Server{
		Addr:              g.cfg.Listen,
		Handler:           cors(g),
		ReadHeaderTimeout: 15 * time.Second,
	}
	return srv.ListenAndServe()
}

func singleJoinSlash(a, b string) string {
	a = strings.TrimSuffix(a, "/")
	if b == "" {
		if a == "" {
			return "/"
		}
		return a
	}
	if !strings.HasPrefix(b, "/") {
		b = "/" + b
	}
	return a + b
}

func requestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "req-0"
	}
	return "req-" + hex.EncodeToString(b[:])
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func errBody(msg string) map[string]string { return map[string]string{"error": msg} }
