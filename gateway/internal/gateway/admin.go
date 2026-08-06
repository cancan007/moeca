package gateway

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"orchestra/gateway/internal/config"
)

// AdminHeader carries the admin capability token for the provider admin API.
// It is DISTINCT from the session token: the Tauri shell holds the admin token
// and sandboxes never receive it, so a sandbox that can reach the gateway port
// still cannot touch provider config or secrets.
const AdminHeader = "X-Orchestra-Admin"

// resolveInject expands an inject-header template for a provider: ${SECRET}
// becomes the provider's in-memory secret (set via the admin API), ${GROUPS}
// the caller session's knowledge groups, and ${VAR} a gateway environment
// value. The secret is substituted first so it is never looked up as an env var.
//
// send reports whether the header should be set at all. It is false only when a
// ${GROUPS} template meets a session that declares no group policy: an omitted
// header tells the upstream "no policy", whereas an empty one would tell it
// "this caller is entitled to nothing". Both are meaningful and they are
// opposites, so the absent case cannot be expressed as an empty string.
func (g *Gateway) resolveInject(provider, tmpl string, groups []string) (value string, send bool) {
	if strings.Contains(tmpl, "${SECRET}") {
		sec, _ := g.reg.secret(provider)
		tmpl = strings.ReplaceAll(tmpl, "${SECRET}", sec)
	}
	if strings.Contains(tmpl, "${GROUPS}") {
		if groups == nil {
			return "", false
		}
		tmpl = strings.ReplaceAll(tmpl, "${GROUPS}", strings.Join(groups, ","))
	}
	return config.ExpandEnv(tmpl), true
}

// targetBlocked reports whether forwarding to host is denied by the SSRF /
// host-reach policy. Docker-host aliases and private/loopback/link-local IP
// literals are always blocked; caller-supplied (dynamic) targets are also DNS-
// resolved and blocked if any resolved IP is private (rebinding defense).
// Fixed, admin-set upstream hostnames are trusted (no DNS lookup).
func (g *Gateway) targetBlocked(svc config.Service, host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[:i]
	}
	switch h {
	case "host.docker.internal", "gateway.docker.internal", "host.lima.internal",
		"host.orb.internal", "kubernetes.docker.internal":
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return blockedIP(ip)
	}
	if svc.Upstream == "" { // dynamic, caller-supplied target
		ips, err := net.LookupIP(h)
		if err != nil {
			return true // fail closed for an unresolvable dynamic target
		}
		for _, ip := range ips {
			if blockedIP(ip) {
				return true
			}
		}
	}
	return false
}

func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// adminAuthed verifies the admin capability token (constant-time). When a hash
// is configured, the presented token is SHA-256'd and compared to it, so the
// gateway never holds the raw token (it lives only host-side). Admin is disabled
// when neither a hash nor a raw token is configured.
func (g *Gateway) adminAuthed(r *http.Request) bool {
	got := r.Header.Get(AdminHeader)
	if got == "" {
		return false
	}
	if g.adminTokenHash != "" {
		sum := sha256.Sum256([]byte(got))
		return subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(g.adminTokenHash)) == 1
	}
	if g.adminToken != "" {
		return subtle.ConstantTimeCompare([]byte(got), []byte(g.adminToken)) == 1
	}
	return false
}

// providerInput is the admin upsert body (non-secret fields only).
type providerInput struct {
	Name          string            `json:"name"`
	Kind          string            `json:"kind"`
	Prefix        string            `json:"prefix"`
	Upstream      string            `json:"upstream"`
	Allowlist     []string          `json:"allowlist"`
	Models        []string          `json:"models"`
	InjectHeaders map[string]string `json:"injectHeaders"`
	StripHeaders  []string          `json:"stripHeaders"`
	// MaxTokensPerSession, when non-nil, sets this provider's per-session token
	// budget (0 => unlimited). Omitted (nil) => preserve the existing budget, so
	// a config-file-seeded limit survives an admin edit that doesn't touch it.
	MaxTokensPerSession *int64 `json:"maxTokensPerSession"`
}

// handleProviders serves the admin provider API (GET list / PUT upsert / DELETE).
// Secrets are never returned; upsert carries no secret (use /providers/secret).
func (g *Gateway) handleProviders(w http.ResponseWriter, r *http.Request) {
	if !g.adminAuthed(r) {
		writeJSON(w, http.StatusUnauthorized, errBody("admin token required"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"providers": g.reg.list()})
	case http.MethodPut:
		var in providerInput
		if !decodeJSON(w, r, &in) {
			return
		}
		if in.Name == "" || in.Prefix == "" {
			writeJSON(w, http.StatusBadRequest, errBody("name and prefix are required"))
			return
		}
		if !strings.HasPrefix(in.Prefix, "/") || !strings.HasSuffix(in.Prefix, "/") {
			writeJSON(w, http.StatusBadRequest, errBody("prefix must start and end with '/'"))
			return
		}
		// Preserve fields the admin UI does not send, so an edit never silently
		// drops security policy (WriteAllow / ProtectedBranches) or throttling
		// (RateLimit / Budget) seeded from the config file. The budget is updated
		// only when the caller explicitly provides maxTokensPerSession.
		existing, _ := g.reg.get(in.Name)
		svc := config.Service{
			Kind:              in.Kind,
			Models:            in.Models,
			Prefix:            in.Prefix,
			Upstream:          in.Upstream,
			Allowlist:         in.Allowlist,
			InjectHeaders:     in.InjectHeaders,
			StripHeaders:      in.StripHeaders,
			RateLimit:         existing.RateLimit,
			Budget:            existing.Budget,
			WriteAllow:        existing.WriteAllow,
			ProtectedBranches: existing.ProtectedBranches,
		}
		if in.MaxTokensPerSession != nil {
			svc.Budget.MaxTokensPerSession = *in.MaxTokensPerSession
		}
		g.reg.upsert(in.Name, svc)
		writeJSON(w, http.StatusOK, map[string]string{"upserted": in.Name})
	case http.MethodDelete:
		name := r.URL.Query().Get("name")
		if name == "" {
			writeJSON(w, http.StatusBadRequest, errBody("name is required"))
			return
		}
		g.reg.delete(name)
		writeJSON(w, http.StatusOK, map[string]string{"deleted": name})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, errBody("method not allowed"))
	}
}

// handleProviderSecret sets (or clears) a provider secret in gateway memory. The
// value is never persisted by the gateway nor returned by any endpoint.
func (g *Gateway) handleProviderSecret(w http.ResponseWriter, r *http.Request) {
	if !g.adminAuthed(r) {
		writeJSON(w, http.StatusUnauthorized, errBody("admin token required"))
		return
	}
	if r.Method != http.MethodPut {
		writeJSON(w, http.StatusMethodNotAllowed, errBody("method not allowed"))
		return
	}
	var in struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Name == "" {
		writeJSON(w, http.StatusBadRequest, errBody("name is required"))
		return
	}
	g.reg.setSecret(in.Name, in.Value)
	writeJSON(w, http.StatusOK, map[string]string{"ok": in.Name})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid JSON body"))
		return false
	}
	return true
}
