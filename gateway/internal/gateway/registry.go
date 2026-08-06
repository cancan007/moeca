package gateway

import (
	"sort"
	"strings"
	"sync"

	"orchestra/gateway/internal/config"
)

// providerRegistry is the gateway's live, mutable set of services (providers)
// plus their in-memory secrets. It is seeded from the config file and then
// mutated only via the admin API (which the Tauri shell drives over loopback).
//
// Secrets live ONLY here, in memory: they are never written to disk by the
// gateway, never returned by the admin GET, and never passed to a sandbox.
type providerRegistry struct {
	mu       sync.RWMutex
	services map[string]config.Service
	secrets  map[string]string // service name -> secret value (in-memory only)
}

func newRegistry(seed map[string]config.Service) *providerRegistry {
	services := make(map[string]config.Service, len(seed))
	for k, v := range seed {
		services[k] = v
	}
	return &providerRegistry{services: services, secrets: map[string]string{}}
}

// route returns the service whose prefix is the longest match for path.
func (r *providerRegistry) route(path string) (string, config.Service, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var bestName string
	var best config.Service
	bestLen := -1
	for name, svc := range r.services {
		if strings.HasPrefix(path, svc.Prefix) && len(svc.Prefix) > bestLen {
			bestName, best, bestLen = name, svc, len(svc.Prefix)
		}
	}
	return bestName, best, bestLen >= 0
}

func (r *providerRegistry) get(name string) (config.Service, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	svc, ok := r.services[name]
	return svc, ok
}

func (r *providerRegistry) upsert(name string, svc config.Service) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.services[name] = svc
}

func (r *providerRegistry) delete(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.services, name)
	delete(r.secrets, name)
}

// setSecret stores (or clears, if val=="") a provider secret in memory.
func (r *providerRegistry) setSecret(name, val string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if val == "" {
		delete(r.secrets, name)
		return
	}
	r.secrets[name] = val
}

func (r *providerRegistry) secret(name string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.secrets[name]
	return s, ok
}

// providerView is the secret-free description returned by the admin GET.
type providerView struct {
	Name          string            `json:"name"`
	Kind          string            `json:"kind"`
	Prefix        string            `json:"prefix"`
	Upstream      string            `json:"upstream"`
	Allowlist     []string          `json:"allowlist"`
	Models        []string          `json:"models"`
	InjectHeaders map[string]string `json:"injectHeaders"`
	HasSecret     bool              `json:"hasSecret"`
	// MaxTokensPerSession is the per-session token budget (0 => unlimited), so
	// the admin UI can display and round-trip the current limit.
	MaxTokensPerSession int64 `json:"maxTokensPerSession"`
}

// list returns every provider, secret-free (value replaced by a HasSecret flag).
func (r *providerRegistry) list() []providerView {
	r.mu.RLock()
	defer r.mu.RUnlock()
	views := make([]providerView, 0, len(r.services))
	for name, svc := range r.services {
		_, has := r.secrets[name]
		views = append(views, providerView{
			Name:                name,
			Kind:                svc.Kind,
			Prefix:              svc.Prefix,
			Upstream:            svc.Upstream,
			Allowlist:           svc.Allowlist,
			Models:              svc.Models,
			InjectHeaders:       svc.InjectHeaders, // header *templates* (e.g. "Bearer ${SECRET}") — no secret values
			HasSecret:           has,
			MaxTokensPerSession: svc.Budget.MaxTokensPerSession,
		})
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Name < views[j].Name })
	return views
}
