// Package api exposes the RAG indexer's HTTP surface.
//
// /search is the ONLY route a sandboxed agent can reach — and only through the
// gateway's /rag route (this service is not on the sandbox egress network).
// /index, /sources and /status are host-facing management routes (loopback).
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"orchestra/ragindex/internal/index"
)

type Server struct {
	idx    *index.Index
	listen string
}

func New(idx *index.Index, listen string) *Server {
	return &Server{idx: idx, listen: listen}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /search", s.handleSearch)
	mux.HandleFunc("POST /index", s.handleIndex)
	mux.HandleFunc("GET /sources", s.handleStatus)
	mux.HandleFunc("GET /status", s.handleStatus)
	mux.HandleFunc("GET /graph", s.handleGraph)
	mux.HandleFunc("POST /groups", s.handleGroups)
	return cors(mux)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query string `json:"query"`
		K     int    `json:"k"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, 400, "invalid JSON body")
		return
	}
	if req.Query == "" {
		writeErr(w, 400, "query is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	res, err := s.idx.Search(ctx, req.Query, req.K, groupFilter(r))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"results": res})
}

// handleIndex kicks off a rebuild in the background and returns immediately; the
// UI polls /status for progress.
func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		_ = s.idx.Build(ctx)
	}()
	writeJSON(w, 202, map[string]string{"status": "building"})
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, s.idx.Status())
}

// handleGraph serves the projected index for the Knowledge graph view. It is a
// host-facing management route like /status — the projection exposes every
// source in the index at once, which is precisely what a group-scoped agent
// must not see, so it stays off the route the gateway proxies.
func (s *Server) handleGraph(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, s.idx.Graph())
}

// handleGroups receives the source→groups mapping from the host, which owns the
// Knowledge graph. Host-facing like the routes above, and for a sharper reason:
// this decides what every scoped search may reach, so an agent able to call it
// could grant itself the whole index.
//
// Applying it re-tags chunks in place — no re-embedding — so a permission edit
// takes effect at once rather than after a rebuild.
func (s *Server) handleGroups(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Map map[string][]string `json:"map"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil {
		writeErr(w, 400, "invalid JSON body")
		return
	}
	if req.Map == nil {
		writeErr(w, 400, "map is required")
		return
	}
	matched := s.idx.SetGroups(req.Map)
	// The counts differ when the graph names sources this index does not have,
	// which is the normal state right after a source is removed — worth
	// surfacing rather than reporting a bare success.
	writeJSON(w, 200, map[string]int{"sources": len(req.Map), "matched": matched})
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) Run() error {
	srv := &http.Server{Addr: s.listen, Handler: s.Handler(), ReadHeaderTimeout: 15 * time.Second}
	return srv.ListenAndServe()
}
