// Package githubapp talks to GitHub as a GitHub App over its REST API: it
// verifies webhooks, mints installation tokens (D7), reads a commit's CI runs,
// downloads a commit, and writes check runs. No GitHub SDK: plain HTTP.
package githubapp

import (
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is GitHub's API.
const DefaultBaseURL = "https://api.github.com"

// VerifySignature checks a webhook's X-Hub-Signature-256 header against the
// body and the webhook secret, in constant time.
func VerifySignature(secret, body []byte, header string) bool {
	sig, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	got, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

// ParseKey reads an App's private key (PEM, PKCS#1 or PKCS#8).
func ParseKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("the GitHub App key is not PEM")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("the GitHub App key: %w", err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("the GitHub App key is not an RSA key")
	}
	return rk, nil
}

// App is a GitHub App.
type App struct {
	ID      int64
	Key     *rsa.PrivateKey
	BaseURL string
	HTTP    *http.Client
	Now     func() time.Time

	mu     sync.Mutex
	tokens map[int64]token
}

type token struct {
	value   string
	expires time.Time
}

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *App) base() string {
	if a.BaseURL != "" {
		return strings.TrimSuffix(a.BaseURL, "/")
	}
	return DefaultBaseURL
}

func (a *App) client() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	return http.DefaultClient
}

// jwt is the App's own short-lived token, used only to mint installation
// tokens.
func (a *App) jwt() (string, error) {
	now := a.now()
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"iat": now.Add(-60 * time.Second).Unix(), // allow for clock drift
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": strconv.FormatInt(a.ID, 10),
	})
	if err != nil {
		return "", err
	}
	signing := header + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.Key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + enc.EncodeToString(sig), nil
}

// Installation is the App acting in one installation, with a token minted
// for it and refreshed before it expires.
func (a *App) Installation(id int64) *Client {
	return &Client{app: a, installation: id}
}

func (a *App) installationToken(ctx context.Context, id int64) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if t, ok := a.tokens[id]; ok && a.now().Before(t.expires.Add(-2*time.Minute)) {
		return t.value, nil
	}
	jwt, err := a.jwt()
	if err != nil {
		return "", err
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	path := fmt.Sprintf("/app/installations/%d/access_tokens", id)
	if err := a.do(ctx, http.MethodPost, path, "Bearer "+jwt, nil, &out); err != nil {
		return "", fmt.Errorf("minting a token for installation %d: %w", id, err)
	}
	if a.tokens == nil {
		a.tokens = map[int64]token{}
	}
	a.tokens[id] = token{out.Token, out.ExpiresAt}
	return out.Token, nil
}

// apiError is a GitHub error, reported by status and GitHub's message; it
// never includes a token.
type apiError struct {
	Status  string
	Message string
}

func (e *apiError) Error() string { return "GitHub: " + e.Status + ": " + e.Message }

func (a *App) do(ctx context.Context, method, path, auth string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base()+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", auth)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var m struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &m)
		return &apiError{Status: resp.Status, Message: m.Message}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Client calls the API as one installation.
type Client struct {
	app          *App
	installation int64
}

func (c *Client) call(ctx context.Context, method, path string, in, out any) error {
	tok, err := c.app.installationToken(ctx, c.installation)
	if err != nil {
		return err
	}
	return c.app.do(ctx, method, path, "token "+tok, in, out)
}

// Run is one GitHub Actions workflow run.
type Run struct {
	Name       string `json:"name"`
	HeadSHA    string `json:"head_sha"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HTMLURL    string `json:"html_url"`
}

// RunsForCommit are the workflow runs for one commit.
func (c *Client) RunsForCommit(ctx context.Context, repo, sha string) ([]Run, error) {
	var out struct {
		WorkflowRuns []Run `json:"workflow_runs"`
	}
	path := fmt.Sprintf("/repos/%s/actions/runs?head_sha=%s&per_page=100", repo, url.QueryEscape(sha))
	if err := c.call(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.WorkflowRuns, nil
}

// Tarball downloads a commit's tree as a gzipped tar. GitHub answers with a
// redirect to a short-lived download URL, which the HTTP client follows.
func (c *Client) Tarball(ctx context.Context, repo, ref string, w io.Writer) error {
	tok, err := c.app.installationToken(ctx, c.installation)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.app.base()+"/repos/"+repo+"/tarball/"+url.PathEscape(ref), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+tok)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.app.client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return &apiError{Status: resp.Status, Message: "downloading " + repo + "@" + ref}
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

// CheckRun is a check run already on a commit.
type CheckRun struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	StartedAt  time.Time `json:"started_at"`
	HTMLURL    string    `json:"html_url"`
	App        struct {
		ID int64 `json:"id"`
	} `json:"app"`
}

// ChecksForCommit are the check runs on a commit, from every App.
func (c *Client) ChecksForCommit(ctx context.Context, repo, sha string) ([]CheckRun, error) {
	var out struct {
		CheckRuns []CheckRun `json:"check_runs"`
	}
	path := fmt.Sprintf("/repos/%s/commits/%s/check-runs?per_page=100", repo, url.PathEscape(sha))
	if err := c.call(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.CheckRuns, nil
}

// Check is a check run's state.
type Check struct {
	Name       string
	Status     string // queued, in_progress, completed
	Conclusion string // success, failure, neutral, cancelled (when completed)
	Title      string
	Summary    string
	Text       string
}

func (k Check) body() map[string]any {
	b := map[string]any{"status": k.Status}
	if k.Name != "" {
		b["name"] = k.Name
	}
	if k.Conclusion != "" {
		b["conclusion"] = k.Conclusion
	}
	if k.Title != "" || k.Summary != "" {
		out := map[string]any{"title": k.Title, "summary": k.Summary}
		if k.Text != "" {
			out["text"] = tail(k.Text, 60000)
		}
		b["output"] = out
	}
	return b
}

// tail keeps the end of s, which is where a failing deploy says why.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…\n" + s[len(s)-n:]
}

// CreateCheck starts a check run on a commit and returns its ID.
func (c *Client) CreateCheck(ctx context.Context, repo, sha string, k Check) (int64, error) {
	b := k.body()
	b["head_sha"] = sha
	var out struct {
		ID int64 `json:"id"`
	}
	if err := c.call(ctx, http.MethodPost, "/repos/"+repo+"/check-runs", b, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// UpdateCheck changes a check run.
func (c *Client) UpdateCheck(ctx context.Context, repo string, id int64, k Check) error {
	return c.call(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/check-runs/%d", repo, id), k.body(), nil)
}
