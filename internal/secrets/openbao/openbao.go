// Package openbao is a secret store backed by an OpenBao (or Vault) KV
// version 2 engine, reached over its HTTP API with no client library.
//
// Where a logical path such as shop/app lives inside the mount is set by a
// Layout, a deployment setting, so a repo never names a store location.
package openbao

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BrutalSystems/dray/internal/secrets"
)

// Config says where the store is and how to sign in.
type Config struct {
	// Address is the server's URL, such as https://bao.example.com.
	Address string
	// Mount is the KV v2 engine's mount; defaults to "secret".
	Mount string
	// Layout places logical paths inside the mount; the default puts
	// secret:shop/app at shop/app.
	Layout Layout
	// Namespace is sent as X-Vault-Namespace when set.
	Namespace string
	// Token returns the token to send. Use EnvToken for the CLI.
	Token func(ctx context.Context) (string, error)
	// HTTPClient defaults to a client with a 30-second timeout.
	HTTPClient *http.Client
}

// Store is a KV v2 secret store.
type Store struct {
	cfg  Config
	base string
}

var folderRE = regexp.MustCompile(`^[A-Za-z0-9_][-A-Za-z0-9_.]*(/[A-Za-z0-9_][-A-Za-z0-9_.]*)*$`)

// New checks cfg and returns a store. It makes no request.
func New(cfg Config) (*Store, error) {
	if cfg.Address == "" {
		return nil, errors.New("openbao: no address set")
	}
	if cfg.Mount == "" {
		cfg.Mount = "secret"
	}
	if !folderRE.MatchString(cfg.Mount) {
		return nil, fmt.Errorf("openbao: bad mount %q", cfg.Mount)
	}
	if err := cfg.Layout.check(); err != nil {
		return nil, fmt.Errorf("openbao: %w", err)
	}
	if cfg.Token == nil {
		return nil, errors.New("openbao: no token source set")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Store{cfg: cfg, base: strings.TrimRight(cfg.Address, "/") + "/v1/" + cfg.Mount}, nil
}

func (s *Store) String() string {
	where := s.cfg.Mount + "/" + s.cfg.Layout.template()
	if s.cfg.Layout.Env != "" {
		where += " (env " + s.cfg.Layout.Env + ")"
	}
	return "openbao " + s.cfg.Address + " " + where
}

// Where returns the location of a logical path inside the store, such as
// kv/acme/shop/prod/app, for messages.
func (s *Store) Where(p string) (string, error) {
	loc, err := s.cfg.Layout.location(p)
	if err != nil {
		return "", err
	}
	return s.cfg.Mount + "/" + loc, nil
}

// Get implements secrets.Store. Values that are not strings in the store are
// returned as JSON.
func (s *Store) Get(ctx context.Context, p string) (secrets.Values, error) {
	if err := secrets.CheckPath(p); err != nil {
		return nil, err
	}
	var body struct {
		Data struct {
			Data map[string]json.RawMessage `json:"data"`
		} `json:"data"`
	}
	loc, err := s.cfg.Layout.location(p)
	if err != nil {
		return nil, err
	}
	found, err := s.do(ctx, http.MethodGet, "/data/"+loc, nil, &body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	if !found || body.Data.Data == nil {
		return nil, fmt.Errorf("%s: %w", p, secrets.ErrNotFound)
	}
	out := secrets.Values{}
	for k, raw := range body.Data.Data {
		var str string
		if json.Unmarshal(raw, &str) == nil {
			out[k] = str
		} else {
			out[k] = string(raw)
		}
	}
	return out, nil
}

// Put implements secrets.Store. It writes a new version holding exactly v.
func (s *Store) Put(ctx context.Context, p string, v secrets.Values) error {
	if err := secrets.CheckPath(p); err != nil {
		return err
	}
	if err := secrets.CheckValues(v); err != nil {
		return err
	}
	loc, err := s.cfg.Layout.location(p)
	if err != nil {
		return err
	}
	if _, err := s.do(ctx, http.MethodPost, "/data/"+loc, map[string]any{"data": v}, nil); err != nil {
		return fmt.Errorf("%s: %w", p, err)
	}
	return nil
}

// Delete implements secrets.Store. It removes every version and the
// secret's metadata.
func (s *Store) Delete(ctx context.Context, p string) error {
	if err := secrets.CheckPath(p); err != nil {
		return err
	}
	loc, err := s.cfg.Layout.location(p)
	if err != nil {
		return err
	}
	if _, err := s.do(ctx, http.MethodDelete, "/metadata/"+loc, nil, nil); err != nil {
		return fmt.Errorf("%s: %w", p, err)
	}
	return nil
}

// List implements secrets.Store. It lists each app's folder; with an empty
// prefix the apps are the folders under the layout's base, or the apps named
// in Clients when the layout has a {client} before {app}.
func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	if err := secrets.CheckPrefix(prefix); err != nil {
		return nil, err
	}
	var apps []string
	if prefix != "" {
		app, _, _ := strings.Cut(prefix, "/")
		apps = []string{app}
	} else if base, ok := s.cfg.Layout.appsBase(); ok {
		keys, err := s.list(ctx, base)
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			if a, isFolder := strings.CutSuffix(k, "/"); isFolder {
				apps = append(apps, a)
			}
		}
	} else {
		apps = s.cfg.Layout.knownApps()
	}
	out := []string{}
	for _, app := range apps {
		folder, err := s.cfg.Layout.folder(app)
		if errors.Is(err, errNoClient) {
			continue // an app with no client has no secrets here
		}
		if err != nil {
			return nil, err
		}
		var rel []string
		if err := s.walk(ctx, folder, "", &rel); err != nil {
			return nil, err
		}
		for _, r := range rel {
			p := app + "/" + r
			if secrets.CheckPath(p) == nil && secrets.UnderPrefix(p, prefix) {
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// walk collects the secrets under folder, as paths relative to it.
func (s *Store) walk(ctx context.Context, folder, rel string, out *[]string) error {
	keys, err := s.list(ctx, join(folder, rel))
	if err != nil {
		return err
	}
	for _, k := range keys {
		name, isFolder := strings.CutSuffix(k, "/")
		if isFolder {
			if err := s.walk(ctx, folder, join(rel, name), out); err != nil {
				return err
			}
			continue
		}
		*out = append(*out, join(rel, name))
	}
	return nil
}

func join(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "/" + b
}

func (s *Store) list(ctx context.Context, folder string) ([]string, error) {
	var body struct {
		Data struct {
			Keys []string `json:"keys"`
		} `json:"data"`
	}
	found, err := s.do(ctx, "LIST", "/metadata/"+folder+"/", nil, &body)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", folder, err)
	}
	if !found {
		return nil, nil
	}
	return body.Data.Keys, nil
}

// do sends one request. A 404 returns found=false with no error.
func (s *Store) do(ctx context.Context, method, p string, in, out any) (found bool, err error) {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return false, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.base+p, body)
	if err != nil {
		return false, err
	}
	token, err := s.cfg.Token(ctx)
	if err != nil {
		return false, err
	}
	req.Header.Set("X-Vault-Token", token)
	req.Header.Set("X-Vault-Request", "true")
	if s.cfg.Namespace != "" {
		req.Header.Set("X-Vault-Namespace", s.cfg.Namespace)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.cfg.HTTPClient.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return false, err
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return false, nil
	case resp.StatusCode >= 300:
		return false, apiError(resp.StatusCode, data)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return false, fmt.Errorf("reading the response: %w", err)
		}
	}
	return true, nil
}

// apiError reports the server's error messages, which never hold a value.
func apiError(status int, data []byte) error {
	var e struct {
		Errors []string `json:"errors"`
	}
	_ = json.Unmarshal(data, &e)
	if len(e.Errors) > 0 {
		return fmt.Errorf("openbao: %d %s: %s", status, http.StatusText(status), strings.Join(e.Errors, "; "))
	}
	return fmt.Errorf("openbao: %d %s", status, http.StatusText(status))
}

// EnvToken reads a token the way the bao and vault CLIs do: BAO_TOKEN, then
// VAULT_TOKEN, then the token helper file ~/.vault-token.
func EnvToken(context.Context) (string, error) {
	for _, k := range []string{"BAO_TOKEN", "VAULT_TOKEN"} {
		if v := os.Getenv(k); v != "" {
			return v, nil
		}
	}
	home, err := os.UserHomeDir()
	if err == nil {
		if data, err := os.ReadFile(filepath.Join(home, ".vault-token")); err == nil { //nolint:gosec // the CLIs' own token file
			if t := strings.TrimSpace(string(data)); t != "" {
				return t, nil
			}
		}
	}
	return "", errors.New("openbao: no token; sign in with bao login, or set BAO_TOKEN")
}

// EnvAddress returns BAO_ADDR, then VAULT_ADDR.
func EnvAddress() string {
	if v := os.Getenv("BAO_ADDR"); v != "" {
		return v
	}
	return os.Getenv("VAULT_ADDR")
}
