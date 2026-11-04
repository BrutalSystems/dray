package openbao

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// ServiceAccountTokenPath is where Kubernetes puts a pod's service account
// token.
const ServiceAccountTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token" //nolint:gosec // a path, not a credential

// KubernetesLogin signs in to OpenBao's Kubernetes auth method with a pod's
// service account token, so a server needs no stored vault token. Each role
// grants what one app may read; tokens are cached until shortly before they
// expire.
type KubernetesLogin struct {
	// Address is the server.
	Address string
	// Mount is the auth method's mount; defaults to kubernetes.
	Mount string
	// JWTPath is the service account token; defaults to ServiceAccountTokenPath.
	JWTPath    string
	HTTPClient *http.Client
	Now        func() time.Time

	mu     sync.Mutex
	tokens map[string]cachedToken
}

type cachedToken struct {
	value   string
	expires time.Time
}

// Token returns a token-source for role, for Config.Token.
func (k *KubernetesLogin) Token(role string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) { return k.login(ctx, role) }
}

func (k *KubernetesLogin) now() time.Time {
	if k.Now != nil {
		return k.Now()
	}
	return time.Now()
}

func (k *KubernetesLogin) login(ctx context.Context, role string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if t, ok := k.tokens[role]; ok && k.now().Before(t.expires) {
		return t.value, nil
	}
	jwtPath := k.JWTPath
	if jwtPath == "" {
		jwtPath = ServiceAccountTokenPath
	}
	jwt, err := os.ReadFile(jwtPath) //nolint:gosec // the pod's own token
	if err != nil {
		return "", fmt.Errorf("openbao: reading the service account token: %w", err)
	}
	mount := k.Mount
	if mount == "" {
		mount = "kubernetes"
	}
	body, _ := json.Marshal(map[string]string{"role": role, "jwt": strings.TrimSpace(string(jwt))})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(k.Address, "/")+"/v1/auth/"+mount+"/login", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	client := k.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("openbao: signing in as role %s: %w", role, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openbao: signing in as role %s: %w", role, apiError(resp.StatusCode, data))
	}
	var out struct {
		Auth struct {
			ClientToken   string `json:"client_token"`
			LeaseDuration int    `json:"lease_duration"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.Auth.ClientToken == "" {
		return "", fmt.Errorf("openbao: signing in as role %s: no token in the answer", role)
	}
	ttl := time.Duration(out.Auth.LeaseDuration) * time.Second
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	// Renew early, so a token never expires mid-deploy.
	expires := k.now().Add(ttl * 3 / 4)
	if k.tokens == nil {
		k.tokens = map[string]cachedToken{}
	}
	k.tokens[role] = cachedToken{out.Auth.ClientToken, expires}
	return out.Auth.ClientToken, nil
}
