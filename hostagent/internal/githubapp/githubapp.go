// Package githubapp mints GitHub App installation access tokens, host-side.
//
// The Delivery issue pull is initiated by the host agent — trusted host-side
// code, not a network-isolated sandbox — so it authenticates to GitHub directly
// rather than through the security gateway (the gateway exists to keep API keys
// out of the *sandboxed* agents). A GitHub App gives per-repository, fine-grained
// (Issues: Read) access with short-lived tokens: we sign an App JWT with the
// App's private key (RS256), look up the installation covering a repo, and
// exchange the JWT for a ~1h installation token, cached until it nears expiry.
//
// Stdlib only (crypto/rsa, crypto/x509) — no third-party JWT dependency.
package githubapp

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const apiBase = "https://api.github.com"

// App holds an App's identity and mints installation tokens for repos.
type App struct {
	appID string
	key   *rsa.PrivateKey
	http  *http.Client

	mu     sync.Mutex
	instID map[string]int64      // "owner/repo" -> installation id
	tokens map[int64]cachedToken // installation id -> token
}

type cachedToken struct {
	token   string
	expires time.Time
}

// New parses a PKCS#1 or PKCS#8 PEM private key and returns an App. appID is the
// numeric GitHub App ID (the `iss` of the JWT).
func New(appID, pemKey string) (*App, error) {
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return nil, fmt.Errorf("github app id is required")
	}
	block, _ := pem.Decode([]byte(pemKey))
	if block == nil {
		return nil, fmt.Errorf("invalid private key PEM")
	}
	var key *rsa.PrivateKey
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		key = k
	} else if k8, err8 := x509.ParsePKCS8PrivateKey(block.Bytes); err8 == nil {
		rk, ok := k8.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("private key is not RSA")
		}
		key = rk
	} else {
		return nil, fmt.Errorf("parse private key: %v", err)
	}
	return &App{
		appID:  appID,
		key:    key,
		http:   &http.Client{Timeout: 20 * time.Second},
		instID: map[string]int64{},
		tokens: map[int64]cachedToken{},
	}, nil
}

// AppID returns the configured App ID (for status display).
func (a *App) AppID() string { return a.appID }

// b64url encodes without padding (JWT).
func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// jwt builds a signed App JWT (RS256), valid for ~9 minutes.
func (a *App) jwt(now time.Time) (string, error) {
	header := b64url([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{
		"iat": now.Add(-30 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": a.appID,
	})
	signingInput := header + "." + b64url(claims)
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("sign jwt: %w", err)
	}
	return signingInput + "." + b64url(sig), nil
}

// InstallationToken returns a valid installation access token for owner/repo,
// minting and caching one as needed.
func (a *App) InstallationToken(ctx context.Context, owner, repo string) (string, error) {
	now := time.Now()
	slug := owner + "/" + repo

	a.mu.Lock()
	id, haveID := a.instID[slug]
	if haveID {
		if t, ok := a.tokens[id]; ok && now.Before(t.expires.Add(-2*time.Minute)) {
			a.mu.Unlock()
			return t.token, nil
		}
	}
	a.mu.Unlock()

	jwt, err := a.jwt(now)
	if err != nil {
		return "", err
	}

	if !haveID {
		id, err = a.installationID(ctx, jwt, owner, repo)
		if err != nil {
			return "", err
		}
		a.mu.Lock()
		a.instID[slug] = id
		a.mu.Unlock()
	}

	tok, exp, err := a.mintToken(ctx, jwt, id)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	a.tokens[id] = cachedToken{token: tok, expires: exp}
	a.mu.Unlock()
	return tok, nil
}

func (a *App) installationID(ctx context.Context, jwt, owner, repo string) (int64, error) {
	var out struct {
		ID int64 `json:"id"`
	}
	if err := a.do(ctx, "GET", apiBase+"/repos/"+owner+"/"+repo+"/installation", jwt, &out); err != nil {
		return 0, fmt.Errorf("no installation for %s/%s (is the App installed on that repo?): %w", owner, repo, err)
	}
	if out.ID == 0 {
		return 0, fmt.Errorf("no installation for %s/%s", owner, repo)
	}
	return out.ID, nil
}

func (a *App) mintToken(ctx context.Context, jwt string, instID int64) (string, time.Time, error) {
	var out struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", apiBase, instID)
	if err := a.do(ctx, "POST", url, jwt, &out); err != nil {
		return "", time.Time{}, err
	}
	exp, _ := time.Parse(time.RFC3339, out.ExpiresAt)
	if exp.IsZero() {
		exp = time.Now().Add(50 * time.Minute)
	}
	return out.Token, exp, nil
}

// Get fetches an api.github.com path (e.g. "/repos/owner/repo/issues?state=open")
// authenticated with an installation token scoped to owner/repo, returning the
// raw response body.
func (a *App) Get(ctx context.Context, owner, repo, apiPath string) ([]byte, error) {
	tok, err := a.InstallationToken(ctx, owner, repo)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", apiBase+apiPath, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("github GET %s: %s: %s", apiPath, resp.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// Post performs an installation-authenticated POST and returns the response
// body. The status is returned alongside the body so callers can distinguish
// outcomes GitHub expresses as errors but that are not failures here — notably
// 422 when a pull request for the branch already exists.
func (a *App) Post(ctx context.Context, owner, repo, apiPath string, body any) ([]byte, int, error) {
	tok, err := a.InstallationToken(ctx, owner, repo)
	if err != nil {
		return nil, 0, err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", apiBase+apiPath, bytes.NewReader(raw))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "token "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return out, resp.StatusCode, nil
}

// InstallationTokenFor exposes an installation token to callers that must
// authenticate outside this package — pushing over HTTPS, which git performs.
func (a *App) InstallationTokenFor(ctx context.Context, owner, repo string) (string, error) {
	return a.InstallationToken(ctx, owner, repo)
}

// do performs a GitHub App-JWT-authenticated request and decodes JSON into v.
func (a *App) do(ctx context.Context, method, url, jwt string, v any) error {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("github %s %s: %s: %s", method, url, resp.Status, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, v)
}
