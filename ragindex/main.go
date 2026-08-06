// Command ragindex is Orchestra's RAG indexer. It indexes a read-only knowledge
// mount into an in-memory embedding store and answers similarity queries. It
// runs as a container reachable ONLY by the gateway (for /rag/search) plus a
// loopback-published management port for the UI; sandboxed agents never reach it
// directly. It holds no API key — embeddings go through the gateway.
//
//	go run . -config config.json
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"time"

	"orchestra/ragindex/internal/api"
	"orchestra/ragindex/internal/index"
)

type fileConfig struct {
	Listen        string             `json:"listen"`
	KnowledgeRoot string             `json:"knowledgeRoot"`
	Sources       []index.SourceSpec `json:"sources"` // local dirs + external HTTPS; overrides KnowledgeRoot when set
	Gateway       string             `json:"gateway"`
	EmbedPrefix   string             `json:"embedPrefix"`
	EmbedModel    string             `json:"embedModel"`
	// EmbedMode: "gateway" (default) or "offline" — see internal/index/offline.go.
	EmbedMode string `json:"embedMode"`
}

func main() {
	configPath := flag.String("config", "config.json", "path to rag config JSON")
	flag.Parse()

	cfg := fileConfig{Listen: "0.0.0.0:8790", KnowledgeRoot: "/knowledge", EmbedPrefix: "/openai", EmbedModel: "text-embedding-3-small"}
	if raw, err := os.ReadFile(*configPath); err == nil {
		json.Unmarshal(raw, &cfg)
	}
	// Env overrides (injected by the sandbox/Tauri shell).
	cfg.Gateway = envOr("ORCHESTRA_GATEWAY", cfg.Gateway)
	cfg.EmbedPrefix = envOr("ORCHESTRA_EMBED_PREFIX", cfg.EmbedPrefix)
	cfg.EmbedModel = envOr("ORCHESTRA_EMBED_MODEL", cfg.EmbedModel)
	cfg.EmbedMode = envOr("ORCHESTRA_EMBED_MODE", cfg.EmbedMode)
	if cfg.EmbedMode == index.EmbedModeOffline {
		// Loud on purpose. These vectors are a local approximation, and an
		// operator who reaches this line by accident needs to see it before
		// they trust a search result.
		log.Printf("ragindex: EMBED MODE = offline — vectors are computed locally, NOT by a model. Demo/test use only.")
	}

	idx := index.New(index.Config{
		Root:        cfg.KnowledgeRoot, // fallback when Sources is empty
		Sources:     cfg.Sources,
		Gateway:     cfg.Gateway,
		Session:     os.Getenv("ORCHESTRA_SESSION"),
		EmbedPrefix: cfg.EmbedPrefix,
		EmbedModel:  cfg.EmbedModel,
		EmbedMode:   cfg.EmbedMode,
	})

	// Best-effort initial build, retried with backoff.
	//
	// The retry is not decoration: this container is started by the same shell
	// that pushes provider secrets into the gateway, and it loses that race. It
	// comes up, immediately tries to embed, and gets a 401 back from an upstream
	// that has no key yet — moments before the key arrives. Without a retry the
	// index then stays empty until somebody notices and presses 再インデックス,
	// so every launch leaves the Knowledge screen blank for no reason the user
	// can see.
	//
	// Failures are still surfaced via /status; this only removes the ones that
	// fix themselves.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		delay := 3 * time.Second
		for attempt := 1; ; attempt++ {
			err := idx.Build(ctx)
			if err == nil {
				return
			}
			if attempt >= 6 { // ~1.5 min of trying, then leave it to the UI
				log.Printf("ragindex: initial build failed after %d attempts: %v", attempt, err)
				return
			}
			log.Printf("ragindex: initial build attempt %d failed (%v); retrying in %s", attempt, err, delay)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
			delay *= 2
		}
	}()

	srv := api.New(idx, cfg.Listen)
	log.Printf("ragindex: listening on %s (knowledge %q, embed %s%s)", cfg.Listen, cfg.KnowledgeRoot, cfg.Gateway, cfg.EmbedPrefix)
	if err := srv.Run(); err != nil {
		log.Fatalf("ragindex: %v", err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
