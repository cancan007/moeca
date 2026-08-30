// Command hostagent is Orchestra's host-side service. It manages git worktrees
// for agent deliverables, extracts real diffs against the target branch, runs
// the CI gate, and merges on self-review approval. It runs as a Tauri sidecar
// (loopback only) next to the security gateway.
//
//	go run . -config config.json
package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"path/filepath"

	"orchestra/hostagent/internal/api"
)

func main() {
	watchParent()
	configPath := flag.String("config", "config.json", "path to host agent config JSON")
	flag.Parse()

	raw, err := os.ReadFile(*configPath)
	if err != nil {
		log.Fatalf("hostagent: %v", err)
	}
	var cfg api.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		log.Fatalf("hostagent: parse config: %v", err)
	}
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:8788"
	}
	// Persist under a stable app-data directory unless the config already set
	// one. Tests construct api.New directly with an empty DataDir (in-memory).
	if cfg.DataDir == "" {
		cfg.DataDir = os.Getenv("ORCHESTRA_DATA_DIR")
	}
	if cfg.DataDir == "" {
		if d, err := os.UserConfigDir(); err == nil {
			cfg.DataDir = filepath.Join(d, "orchestra")
		}
	}

	srv := api.New(&cfg)
	// The indexer may have restarted while this process was down, and it cannot
	// ask anyone what the Knowledge graph says. Tell it before serving, so the
	// first run of the day does not search against no labels at all.
	go srv.SyncKnowledgeGroups()
	log.Printf("hostagent: listening on %s for %d repo(s)", cfg.Listen, len(cfg.Repos))
	if err := srv.Run(); err != nil {
		log.Fatalf("hostagent: %v", err)
	}
}
