package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"

	"orchestra/gateway/internal/config"
)

// Sessions minted for one run.
//
// A session is what the gateway knows about a caller, and its `groups` are the
// knowledge it is entitled to retrieve. Until now there was exactly one session
// for every sandbox, defined in the config file, carrying no groups — so every
// run authenticated as the same groupless caller and the group filter had
// nothing to filter by. Binding a retrieval scope to a run means the run needs
// a session of its own.
//
// It has to be minted here rather than claimed by the caller. The whole reason
// X-Orchestra-Groups is trustworthy is that the gateway states it about the
// caller and discards whatever the caller sent; a sandbox that could name its
// own groups could name any of them, and the scope would be decoration. So the
// token is a capability: unguessable, issued to a host-side component holding
// the admin token, and never derivable from anything a sandbox knows.
//
// Runtime sessions live in memory. A gateway restart drops them, which is
// correct — the runs they belonged to did not survive it either.

// AdminSessionsPath is the admin route for minting and revoking run sessions.
const AdminSessionsPath = "/_gateway/sessions"

// sessionRegistry holds the sessions issued at runtime, alongside the static
// ones from the config file.
type sessionRegistry struct {
	mu   sync.RWMutex
	live map[string]config.Session
}

func newSessionRegistry() *sessionRegistry {
	return &sessionRegistry{live: map[string]config.Session{}}
}

func (s *sessionRegistry) put(token string, sess config.Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.live[token] = sess
}

func (s *sessionRegistry) get(token string) (config.Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.live[token]
	return sess, ok
}

func (s *sessionRegistry) delete(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.live, token)
}

func (s *sessionRegistry) count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.live)
}

// sessionInput is the admin mint body.
type sessionInput struct {
	// ID labels the session in logs and audit records — the run id, normally.
	ID    string `json:"id"`
	Label string `json:"label"`
	// Groups is the retrieval scope. Its nil/empty distinction is the same one
	// config.Session documents: nil states no policy (search everything), an
	// empty slice states entitled to nothing. A caller that means "no scope"
	// must send null, not [].
	Groups []string `json:"groups"`
}

// handleSessions mints a session (POST) or revokes one (DELETE).
//
// Minting returns the token exactly once. The gateway keeps it as the map key,
// so this is not a value that can be read back — losing it means minting
// another, which is the correct shape for a capability.
func (g *Gateway) handleSessions(w http.ResponseWriter, r *http.Request) {
	if !g.adminAuthed(r) {
		writeJSON(w, http.StatusUnauthorized, errBody("admin token required"))
		return
	}
	switch r.Method {
	case http.MethodPost:
		var in sessionInput
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, errBody("invalid body"))
			return
		}
		token, err := mintToken()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errBody("could not mint a session"))
			return
		}
		id := in.ID
		if id == "" {
			id = "run"
		}
		g.sessions.put(token, config.Session{ID: id, Label: in.Label, Groups: in.Groups})
		writeJSON(w, http.StatusCreated, map[string]any{"token": token, "id": id, "groups": in.Groups})
	case http.MethodDelete:
		tok := r.URL.Query().Get("token")
		if tok == "" {
			writeJSON(w, http.StatusBadRequest, errBody("token is required"))
			return
		}
		g.sessions.delete(tok)
		writeJSON(w, http.StatusOK, map[string]string{"revoked": tok})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, errBody("POST to mint, DELETE to revoke"))
	}
}

// mintToken produces an unguessable session token. 256 bits, because the token
// IS the authorization — there is nothing else behind it.
func mintToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "run-" + hex.EncodeToString(b[:]), nil
}
