package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Opening a pull request for a Delivery task.
//
// This is deliberately separate from approve-and-merge. That path merges the
// branch into the target locally and pushes nothing; this one pushes the branch
// and opens a PR, leaving the merge decision to GitHub. Doing both to the same
// branch would be contradictory — a branch already merged locally makes for an
// empty or confusing PR — so they are distinct actions, not one flow.
//
// Auth is the host-side GitHub App, the same one that pulls issues. It needs
// Contents: Read and write to push and Pull requests: Read and write to open the
// PR; a missing permission surfaces as GitHub's own 403, which is more useful
// than anything we could infer.

// prReq asks for a pull request from Branch into Base (default: the repo's
// target branch).
type prReq struct {
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
	Base   string `json:"base"`
	Title  string `json:"title"`
	Body   string `json:"body"`
}

// prResp is what the UI needs to link to the result.
type prResp struct {
	URL     string `json:"url"`
	Number  int    `json:"number"`
	Branch  string `json:"branch"`
	Base    string `json:"base"`
	Created bool   `json:"created"` // false when an open PR already existed
}

func (s *Server) handlePullRequest(w http.ResponseWriter, r *http.Request) {
	var req prReq
	if !decode(w, r, &req) {
		return
	}
	repo, ok := s.repo(req.Repo)
	if !ok {
		writeErr(w, 404, "unknown repo")
		return
	}
	if req.Branch == "" {
		writeErr(w, 400, "branch is required")
		return
	}
	base := req.Base
	if base == "" {
		base = repo.Target
	}
	if req.Branch == base {
		writeErr(w, 400, "branch and base are the same")
		return
	}

	app := s.currentGHApp()
	if app == nil {
		writeErr(w, 409, "GitHub App is not configured; set it up in Settings → Proxy / トークン")
		return
	}
	slug, err := s.g.GitHubSlug(repo.Path)
	if err != nil || slug == "" {
		writeErr(w, 409, fmt.Sprintf("repo %q has no github.com origin", repo.Name))
		return
	}
	owner, name, ok := splitSlug(slug)
	if !ok {
		writeErr(w, 409, fmt.Sprintf("unexpected github origin %q", slug))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()

	token, err := app.InstallationTokenFor(ctx, owner, name)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	// Push first: GitHub rejects a PR whose head branch it has never seen, and
	// the error for that is far less clear than a push failure.
	remote := "https://github.com/" + owner + "/" + name + ".git"
	if _, err := s.g.Push(repo.Path, remote, req.Branch, token); err != nil {
		writeErr(w, 502, err.Error())
		return
	}

	title := req.Title
	if title == "" {
		title = req.Branch
	}
	body, status, err := app.Post(ctx, owner, name, "/repos/"+owner+"/"+name+"/pulls", map[string]any{
		"title": title,
		"head":  req.Branch,
		"base":  base,
		"body":  req.Body,
	})
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}

	switch {
	case status >= 200 && status < 300:
		var pr struct {
			Number  int    `json:"number"`
			HTMLURL string `json:"html_url"`
		}
		_ = json.Unmarshal(body, &pr)
		writeJSON(w, 201, prResp{URL: pr.HTMLURL, Number: pr.Number, Branch: req.Branch, Base: base, Created: true})
	case status == 422:
		// GitHub returns 422 both for "a pull request already exists" and for
		// genuinely invalid input. Re-opening the same task should be idempotent,
		// so look for the existing PR rather than surfacing an error; if there is
		// none, this was a real validation failure.
		if pr, found := s.findOpenPR(ctx, app, owner, name, req.Branch, base); found {
			writeJSON(w, 200, pr)
			return
		}
		writeErr(w, 422, githubMessage(body))
	default:
		writeErr(w, 502, fmt.Sprintf("github: %d: %s", status, githubMessage(body)))
	}
}

// findOpenPR looks for an already-open PR for head → base.
func (s *Server) findOpenPR(ctx context.Context, app githubPoster, owner, name, branch, base string) (prResp, bool) {
	raw, err := app.Get(ctx, owner, name, fmt.Sprintf(
		"/repos/%s/%s/pulls?state=open&head=%s:%s&base=%s", owner, name, owner, branch, base))
	if err != nil {
		return prResp{}, false
	}
	var list []struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	if json.Unmarshal(raw, &list) != nil || len(list) == 0 {
		return prResp{}, false
	}
	return prResp{URL: list[0].HTMLURL, Number: list[0].Number, Branch: branch, Base: base, Created: false}, true
}

// githubPoster is the slice of the GitHub App this file needs, so the handler
// can be tested without a real App.
type githubPoster interface {
	InstallationTokenFor(ctx context.Context, owner, repo string) (string, error)
	Post(ctx context.Context, owner, repo, apiPath string, body any) ([]byte, int, error)
	Get(ctx context.Context, owner, repo, apiPath string) ([]byte, error)
}

// githubMessage pulls the human-readable message out of a GitHub error body,
// falling back to the raw body when it is not the expected shape.
func githubMessage(body []byte) string {
	var e struct {
		Message string `json:"message"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(body, &e) == nil && e.Message != "" {
		if len(e.Errors) > 0 && e.Errors[0].Message != "" {
			return e.Message + ": " + e.Errors[0].Message
		}
		return e.Message
	}
	return strings.TrimSpace(string(body))
}

// splitSlug splits "owner/repo".
func splitSlug(slug string) (owner, repo string, ok bool) {
	parts := strings.SplitN(strings.TrimSpace(slug), "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], strings.TrimSuffix(parts[1], ".git"), true
}
