package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// Telling the indexer which sources belong to which group.
//
// The host owns the Knowledge graph and the indexer enforces it, but the
// indexer cannot read the graph: it is off this network by design, which is the
// same arrangement that keeps it off the sandbox network. So the mapping has to
// be pushed, and the question is only who pushes it.
//
// It used to be the front end, from the Knowledge screen — which meant the
// permission model was correct only while somebody was looking at it. A
// scheduled run firing at nine in the morning, into an indexer restarted the
// night before, ran against no labels at all. That is not a place for a screen
// to be load-bearing, so the push moved here, next to the graph it describes.
//
// It happens at three moments, and each answers a different way of going stale:
//
//	startup      the indexer may have restarted while this process was down
//	after edits  membership changed, and the change should not wait for a run
//	before a run the one moment where being wrong actually costs something
//
// The indexer also persists what it is told, so these are a correction rather
// than the only thing standing between a restart and an empty mapping. Both
// halves are wanted: persistence covers the indexer restarting, and this covers
// the graph changing while it was gone.

// ragURL returns the configured indexer URL or the loopback default.
func (c *Config) ragURL() string {
	if c.Rag.URL != "" {
		return c.Rag.URL
	}
	return "http://127.0.0.1:8790"
}

// knowledgeGroupMap builds the source→groups mapping the indexer expects. A
// source in several groups carries all of them; the indexer permits a chunk
// that matches any one.
func (s *Server) knowledgeGroupMap() (map[string][]string, error) {
	groups, err := s.store.KnowledgeGroups()
	if err != nil {
		return nil, err
	}
	// Never nil: the indexer rejects a missing map, and "no group has any
	// source" is a legitimate state that must be sent rather than skipped —
	// it is what clearing the last assignment looks like.
	m := map[string][]string{}
	for _, g := range groups {
		for _, src := range g.Sources {
			m[src] = append(m[src], g.ID)
		}
	}
	return m, nil
}

// syncKnowledgeGroups pushes the current mapping to the indexer.
//
// Errors are returned for the caller to log rather than surfaced to a user: the
// graph edit that triggered this already succeeded, and the indexer being down
// does not make it un-succeed. What it does mean is that the indexer is behind,
// which is why the push is repeated before a run rather than only on edit.
func (s *Server) syncKnowledgeGroups() error {
	if s.store == nil {
		return nil
	}
	// An in-memory graph is not the authority on anything, and pushing one would
	// overwrite the real mapping with whatever a test happens to hold.
	if s.ephemeral {
		return nil
	}
	m, err := s.knowledgeGroupMap()
	if err != nil {
		return fmt.Errorf("reading knowledge groups: %w", err)
	}
	body, err := json.Marshal(map[string]any{"map": m})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(s.cfg.ragURL(), "/")+"/groups", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("indexer returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

// syncKnowledgeGroupsLogged is the fire-and-forget form used where there is
// nobody to report to — startup, and after a graph edit that already returned.
func (s *Server) syncKnowledgeGroupsLogged(when string) {
	if err := s.syncKnowledgeGroups(); err != nil {
		log.Printf("hostagent: pushing knowledge groups to the indexer (%s): %v", when, err)
	}
}

// SyncKnowledgeGroups pushes the mapping at startup. Exported because main
// calls it, not New: a server constructed by a test has no indexer to talk to
// and should not spend ten seconds finding that out.
func (s *Server) SyncKnowledgeGroups() { s.syncKnowledgeGroupsLogged("startup") }
