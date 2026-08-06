// Command gateway runs Orchestra's Go security-proxy.
//
//	go run . -config config.json
//
// It is designed to run as a Tauri sidecar on the host, bound to loopback, so
// sandboxed agent containers reach every upstream through it and never hold API
// keys themselves.
package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"

	"orchestra/gateway/internal/config"
	"orchestra/gateway/internal/gateway"
)

func main() {
	watchParent()
	configPath := flag.String("config", "config.json", "path to gateway config JSON")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("gateway: %v", err)
	}

	// ORCHESTRA_LISTEN overrides the configured listen address. The Tauri shell
	// sets it to 127.0.0.1 in host mode (bare process) so the gateway is NOT
	// exposed on the host's external interfaces. In container mode the config's
	// 0.0.0.0 is kept (the container only publishes 127.0.0.1 to the host, and
	// sandboxes must reach it by the container's network IP).
	if l := os.Getenv("ORCHESTRA_LISTEN"); l != "" {
		cfg.Listen = l
	}

	gw := gateway.New(cfg, os.Stdout, nil, nil)

	// Durable, tamper-evident audit plane. ORCHESTRA_AUDIT_DIR is a writable
	// path (a mounted volume in container mode) — when set, access records are
	// hash-chained into <dir>/audit.db and survive restarts. Unset => the
	// in-memory 500-record ring only (dev/tests).
	if dir := os.Getenv("ORCHESTRA_AUDIT_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Printf("gateway: audit dir %s: %v (audit not persisted)", dir, err)
		} else if store, err := gateway.OpenAuditStore(filepath.Join(dir, "audit.db")); err != nil {
			log.Printf("gateway: open audit store: %v (audit not persisted)", err)
		} else {
			gw.AttachAudit(store)
			log.Printf("gateway: durable audit at %s", filepath.Join(dir, "audit.db"))
		}
	}

	log.Printf("gateway: listening on %s with %d service(s)", cfg.Listen, len(cfg.Services))
	if err := gw.Run(); err != nil {
		log.Fatalf("gateway: %v", err)
	}
}
