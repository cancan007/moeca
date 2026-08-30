package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// A gateway session of the run's own, so a run can carry a knowledge scope.
//
// Which knowledge a run may retrieve is decided by the groups on its gateway
// session — and the gateway states that header about the caller rather than
// accepting one, because a sandbox that could name its own groups could name
// any of them and the scope would be decoration. So the scope cannot travel
// with the request; it has to be baked into a session, and only the holder of
// the admin token can mint one.
//
// That holder is this process. It is host-side, not sandboxed, and it is
// already the component that decides what a sandbox may reach — it derives the
// gateway and registry origins server-side precisely so a client cannot
// redirect them. Minting here keeps the admin token off the sandbox and out of
// the webview, which is the property that matters.
//
// A run without groups mints nothing and uses the shared session. That session
// states no entitlement, and the gateway refuses knowledge retrieval to one that
// does not — so an unscoped run reads no knowledge rather than all of it.

const (
	adminSessionsPath = "/_gateway/sessions"
	adminHeader       = "X-Orchestra-Admin"
	sessionCallLimit  = 15 * time.Second
)

// adminToken is the raw gateway admin token, handed to this process by the
// Tauri shell. Absent in development runs started by hand, where scoped
// sessions simply are not available.
func (s *Server) adminToken() string {
	return strings.TrimSpace(os.Getenv("ORCHESTRA_ADMIN_TOKEN"))
}

// mintRunSession asks the gateway for a session carrying groups, returning the
// token to authenticate this run's stages with.
//
// A failure is returned rather than swallowed: falling back to the shared
// session would silently widen the run's reach to every group, which is the
// opposite of what asking for a scope means.
func (s *Server) mintRunSession(runID string, groups []string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"id":     runID,
		"label":  "run",
		"groups": groups, // nil vs [] is load-bearing; json keeps them distinct
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost, s.cfg.gatewayAdminBase()+adminSessionsPath, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(adminHeader, s.adminToken())
	resp, err := (&http.Client{Timeout: sessionCallLimit}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode/100 != 2 {
		return "", &httpError{status: resp.StatusCode, body: string(raw)}
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Token == "" {
		return "", &httpError{status: resp.StatusCode, body: "gateway returned no session token"}
	}
	return out.Token, nil
}

// revokeRunSession retires a run's session. Best effort: the token is useless
// once the containers are gone, and a gateway restart drops it anyway.
func (s *Server) revokeRunSession(token string) {
	if token == "" || s.adminToken() == "" {
		return
	}
	req, err := http.NewRequest(http.MethodDelete,
		s.cfg.gatewayAdminBase()+adminSessionsPath+"?token="+token, nil)
	if err != nil {
		return
	}
	req.Header.Set(adminHeader, s.adminToken())
	resp, err := (&http.Client{Timeout: sessionCallLimit}).Do(req)
	if err != nil {
		log.Printf("sandbox: revoking run session: %v", err)
		return
	}
	resp.Body.Close()
}

type httpError struct {
	status int
	body   string
}

func (e *httpError) Error() string {
	return "gateway admin API: HTTP " + itoa(e.status) + ": " + truncate(e.body, 300)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// mintStageSessions gives every stage whose scope differs from the run's a
// session of its own, sharing one session between stages that ask for the same
// groups.
//
// A stage's scope is not the run's because how far it may follow knowledge
// relations is a property of the agent template it came from: a planner that
// follows none and a researcher that follows two belong in the same run and
// must not share an entitlement.
func (s *Server) mintStageSessions(run *Run, stages []Stage) error {
	byKey := map[string]string{}
	for _, st := range stages {
		if st.Groups == nil {
			continue
		}
		key := strings.Join(st.Groups, "\x00")
		tok, ok := byKey[key]
		if !ok {
			if s.adminToken() == "" {
				return &httpError{status: 400, body: "a stage asks for a knowledge scope, but there is no gateway admin token to mint a session with"}
			}
			var err error
			tok, err = s.mintRunSession(run.ID+"-"+st.ID, st.Groups)
			if err != nil {
				return err
			}
			byKey[key] = tok
		}
		if run.stageSessions == nil {
			run.stageSessions = map[string]string{}
		}
		run.stageSessions[st.ID] = tok
	}
	return nil
}

// sessionFor returns the session a stage authenticates with: its own when it
// has a scope of its own, the run's otherwise.
func (s *Server) sessionFor(run *Run, stageID string) string {
	if tok, ok := run.stageSessions[stageID]; ok {
		return tok
	}
	return run.session
}

// revokeAllSessions retires everything minted for a run. Distinct tokens only:
// stages that shared a group set shared a session.
func (s *Server) revokeAllSessions(run *Run) {
	seen := map[string]bool{}
	if run.session != "" {
		seen[run.session] = true
		s.revokeRunSession(run.session)
	}
	for _, tok := range run.stageSessions {
		if tok == "" || seen[tok] {
			continue
		}
		seen[tok] = true
		s.revokeRunSession(tok)
	}
}
