// Command sandbox is Orchestra's sandbox control plane. It manages hardened
// Docker containers for agents: each sandbox can touch only its task's git
// worktree and holds no secrets — outbound API calls go through the separate
// gateway, which injects keys. It runs as a Tauri sidecar (loopback only) next
// to the gateway and host agent.
//
//	go run . -config config.json
package main

import (
	"encoding/json"
	"log"
	"os"

	"flag"

	"orchestra/sandbox/internal/api"
)

func main() {
	watchParent()
	configPath := flag.String("config", "config.json", "path to sandbox config JSON")
	flag.Parse()

	raw, err := os.ReadFile(*configPath)
	if err != nil {
		log.Fatalf("sandbox: %v", err)
	}
	var cfg api.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		log.Fatalf("sandbox: parse config: %v", err)
	}
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:8789"
	}

	srv := api.New(&cfg)
	log.Printf("sandbox: listening on %s (network %q, image %q)", cfg.Listen, cfg.Network, cfg.Image)
	if err := srv.Run(); err != nil {
		log.Fatalf("sandbox: %v", err)
	}
}
