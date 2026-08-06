package api

import (
	"net/http"
	"strings"

	"orchestra/hostagent/internal/githubapp"
)

// currentGHApp returns the configured host-side GitHub App (or nil) under the
// read lock.
func (s *Server) currentGHApp() *githubapp.App {
	s.ghMu.RLock()
	defer s.ghMu.RUnlock()
	return s.ghApp
}

// setGHApp installs (or clears, when app is nil) the GitHub App.
func (s *Server) setGHApp(app *githubapp.App) {
	s.ghMu.Lock()
	defer s.ghMu.Unlock()
	s.ghApp = app
}

// handleGitHubAppStatus reports whether a host-side GitHub App is configured
// (and its App ID). It never returns the private key.
func (s *Server) handleGitHubAppStatus(w http.ResponseWriter, _ *http.Request) {
	app := s.currentGHApp()
	if app == nil {
		writeJSON(w, 200, map[string]any{"configured": false})
		return
	}
	writeJSON(w, 200, map[string]any{"configured": true, "appId": app.AppID()})
}

// handleGitHubAppSet installs the host-side GitHub App from an App ID + private
// key (PEM). The key is held in memory only; the Tauri shell keeps it in the OS
// keychain and re-pushes it at launch.
func (s *Server) handleGitHubAppSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AppID      string `json:"appId"`
		PrivateKey string `json:"privateKey"`
	}
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.AppID) == "" || strings.TrimSpace(req.PrivateKey) == "" {
		writeErr(w, 400, "appId and privateKey are required")
		return
	}
	app, err := githubapp.New(req.AppID, req.PrivateKey)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	s.setGHApp(app)
	writeJSON(w, 200, map[string]any{"configured": true, "appId": app.AppID()})
}

// handleGitHubAppClear removes the configured GitHub App.
func (s *Server) handleGitHubAppClear(w http.ResponseWriter, _ *http.Request) {
	s.setGHApp(nil)
	writeJSON(w, 200, map[string]any{"configured": false})
}
