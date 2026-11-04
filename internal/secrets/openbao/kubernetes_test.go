package openbao

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestKubernetesLogin(t *testing.T) {
	var logins int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		if r.URL.Path != "/v1/auth/kubernetes/login" || b["jwt"] != "pod-jwt" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errors":["bad request"]}`))
			return
		}
		if b["role"] != "kv-acme-shop-prod" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"errors":["permission denied"]}`))
			return
		}
		atomic.AddInt32(&logins, 1)
		_, _ = w.Write([]byte(`{"auth":{"client_token":"t-` + b["role"] + `","lease_duration":3600}}`))
	}))
	defer srv.Close()
	jwt := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(jwt, []byte("pod-jwt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	k := &KubernetesLogin{Address: srv.URL, JWTPath: jwt, Now: func() time.Time { return now }}
	src := k.Token("kv-acme-shop-prod")
	ctx := context.Background()
	for range 3 {
		if tok, err := src(ctx); err != nil || tok != "t-kv-acme-shop-prod" {
			t.Fatalf("%q %v", tok, err)
		}
	}
	if logins != 1 {
		t.Errorf("signed in %d times, want 1 (cached)", logins)
	}
	now = now.Add(50 * time.Minute) // past three quarters of the hour
	if _, err := src(ctx); err != nil || logins != 2 {
		t.Errorf("no fresh sign-in near expiry: %v, %d", err, logins)
	}
	if _, err := k.Token("kv-other-prod")(ctx); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("a refused role: %v", err)
	}
}
