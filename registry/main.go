// Command registry is Orchestra's package-registry proxy.
//
// It is the dependency-fetch counterpart to the gateway: a strict sandbox has no
// route to the internet, so `npm install` / `pip install` / `go mod download`
// would fail outright. This service sits on the internal egress network next to
// the gateway, is dual-homed onto the upstream bridge, and relays those fetches
// — GET/HEAD only, to fixed upstreams, with every download logged.
//
//	go run . -config config.json
package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"strconv"
	"strings"

	"orchestra/registry/internal/proxy"
)

func main() {
	configPath := flag.String("config", "config.json", "path to registry config JSON")
	flag.Parse()

	cfg := proxy.Config{Ecosystems: proxy.DefaultEcosystems()}
	if raw, err := os.ReadFile(*configPath); err == nil {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			log.Fatalf("registry: parsing %s: %v", *configPath, err)
		}
	} else if !os.IsNotExist(err) {
		log.Fatalf("registry: reading %s: %v", *configPath, err)
	}
	// A config that omits ecosystems gets the built-in set rather than serving
	// nothing — the defaults are the whole point of the service.
	if len(cfg.Ecosystems) == 0 {
		cfg.Ecosystems = proxy.DefaultEcosystems()
	}

	// Env overrides (injected by the Tauri shell when it starts the container).
	cfg.Listen = envOr("ORCHESTRA_LISTEN", cfg.Listen)
	cfg.PublicBase = envOr("ORCHESTRA_REGISTRY_PUBLIC_BASE", cfg.PublicBase)
	cfg.CacheDir = envOr("ORCHESTRA_REGISTRY_CACHE_DIR", cfg.CacheDir)
	if v := os.Getenv("ORCHESTRA_REGISTRY_MAX_CACHE_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.MaxCacheBytes = n
		}
	}

	go watchParent()

	srv := proxy.New(cfg)
	names := make([]string, 0, len(cfg.Ecosystems))
	for _, e := range cfg.Ecosystems {
		names = append(names, e.Name)
	}
	log.Printf("registry: listening on %s (ecosystems: %s, cache %q)", cfg.Listen, strings.Join(names, ", "), cfg.CacheDir)
	if err := srv.Run(); err != nil {
		log.Fatalf("registry: %v", err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
