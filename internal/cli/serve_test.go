package cli

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BrutalSystems/dray/internal/config"
	"github.com/BrutalSystems/dray/internal/secrets/openbao"
)

func TestAppRole(t *testing.T) {
	c := &config.RepoConfig{Name: "shop", SecretStore: &config.SecretStore{Type: "openbao", OpenBao: &config.OpenBaoStore{Client: "acme"}}}
	if r, err := appRole(c, "prod"); err != nil || r != "kv-acme-shop-prod" {
		t.Errorf("%q %v", r, err)
	}
	t.Setenv("DRAY_VAULT_APP_ROLE", "dray-{app}")
	if r, _ := appRole(c, "prod"); r != "dray-shop" {
		t.Errorf("custom template: %q", r)
	}
	t.Setenv("DRAY_VAULT_APP_ROLE", "")
	c.SecretStore.OpenBao.Client = ""
	if _, err := appRole(c, "prod"); err == nil {
		t.Error("a {client} role without a client was accepted")
	}
}

func TestLoadAppsFromTheVault(t *testing.T) {
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	keyPEM := strings.TrimSuffix(string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})), "\n")
	apps := map[string]map[string]string{
		"acme/dray/prod/github-app-a": {"app_id": "11", "slug": "a-dray", "private_key": keyPEM, "webhook_secret": "wa"},
		"acme/dray/prod/github-app-b": {"app_id": "22", "slug": "b-dray", "private_key": keyPEM, "webhook_secret": "wb"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/auth/kubernetes/login" {
			var b map[string]string
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b["role"] != "kv-acme-dray-prod" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = w.Write([]byte(`{"auth":{"client_token":"server-token","lease_duration":3600}}`))
			return
		}
		if r.Header.Get("X-Vault-Token") != "server-token" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		v, ok := apps[strings.TrimPrefix(r.URL.Path, "/v1/kv/data/")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"data": v}})
	}))
	defer srv.Close()
	jwt := filepath.Join(t.TempDir(), "token")
	_ = os.WriteFile(jwt, []byte("pod-jwt"), 0o600)
	t.Setenv("DRAY_VAULT_ROLE", "kv-acme-dray-prod")
	t.Setenv("DRAY_GITHUB_APPS", "acme/dray/prod/github-app-a, acme/dray/prod/github-app-b")
	got, err := loadApps(context.Background(), &openbao.KubernetesLogin{Address: srv.URL, JWTPath: jwt})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || string(got[11].WebhookSecret) != "wa" || string(got[22].WebhookSecret) != "wb" || got[22].GitHub == nil {
		t.Errorf("apps %+v", got)
	}
	t.Setenv("DRAY_GITHUB_APPS", "acme/dray/prod/missing")
	if _, err := loadApps(context.Background(), &openbao.KubernetesLogin{Address: srv.URL, JWTPath: jwt}); err == nil {
		t.Error("a missing App was not an error")
	}
}
