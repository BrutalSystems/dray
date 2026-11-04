package openbao

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/BrutalSystems/dray/internal/secrets"
	"github.com/BrutalSystems/dray/internal/secrets/storetest"
)

// server returns an OpenBao to test against: DRAY_TEST_OPENBAO_ADDR and
// DRAY_TEST_OPENBAO_TOKEN when set (CI runs a dev server), otherwise a dev
// server started from the bao binary. Never a real one: the test writes and
// deletes under a random prefix, but it should not touch shared data.
func server(t *testing.T) (addr, token string) {
	t.Helper()
	if addr := os.Getenv("DRAY_TEST_OPENBAO_ADDR"); addr != "" {
		return addr, os.Getenv("DRAY_TEST_OPENBAO_TOKEN")
	}
	bin, err := exec.LookPath("bao")
	if err != nil {
		t.Skip("no OpenBao: install bao, or set DRAY_TEST_OPENBAO_ADDR and DRAY_TEST_OPENBAO_TOKEN")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listen := l.Addr().String()
	_ = l.Close()
	token = "dray-test-root"
	cmd := exec.Command(bin, "server", "-dev", "-dev-root-token-id="+token, "-dev-listen-address="+listen) //nolint:gosec // test-only dev server
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			_ = sc.Text() // drain the server's output
		}
	}()
	addr = "http://" + listen
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(addr + "/v1/sys/health") //nolint:noctx // local test server
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return addr, token
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("OpenBao dev server did not start")
	return "", ""
}

func must(s string, err error) string {
	if err != nil {
		panic(err)
	}
	return s
}

func randomPrefix() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "dray-test/" + hex.EncodeToString(b[:])
}

func TestConformance(t *testing.T) {
	addr, token := server(t)
	storetest.Run(t, func(t *testing.T) secrets.Store {
		s, err := New(Config{
			Address: addr, Layout: Layout{Template: randomPrefix() + "/{app}/{name}"},
			Token: func(context.Context) (string, error) { return token, nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestNonStringValuesReadAsJSON(t *testing.T) {
	addr, token := server(t)
	s, err := New(Config{Address: addr, Layout: Layout{Template: randomPrefix() + "/{app}/{name}"}, Token: func(context.Context) (string, error) { return token, nil }})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.do(ctx, http.MethodPost, "/data/"+must(s.cfg.Layout.location("shop/app")), map[string]any{"data": map[string]any{"PORT": 8080, "ON": true, "S": "x"}}, nil); err != nil {
		t.Fatal(err)
	}
	v, err := s.Get(ctx, "shop/app")
	if err != nil {
		t.Fatal(err)
	}
	if !v.Equal(secrets.Values{"PORT": "8080", "ON": "true", "S": "x"}) {
		t.Errorf("got %v", v)
	}
}

func TestErrorsNameNoToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(w, `{"errors":["permission denied"]}`)
	}))
	defer srv.Close()
	s, err := New(Config{Address: srv.URL, Token: func(context.Context) (string, error) { return "s.supersecret", nil }})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Get(context.Background(), "shop/app")
	if err == nil || !strings.Contains(err.Error(), "403 Forbidden: permission denied") || strings.Contains(err.Error(), "supersecret") {
		t.Fatalf("got %v", err)
	}
}

func TestConfigChecks(t *testing.T) {
	tok := func(context.Context) (string, error) { return "", nil }
	bad := []Config{
		{Token: tok},
		{Address: "http://x"},
		{Address: "http://x", Mount: "../sys", Token: tok},
		{Address: "http://x", Layout: Layout{Template: "a/../{app}/{name}"}, Token: tok},
		{Address: "http://x", Layout: Layout{Template: "{name}/{app}"}, Token: tok},
		{Address: "http://x", Layout: Layout{Template: "{app}/{app}/{name}"}, Token: tok},
		{Address: "http://x", Layout: Layout{Template: "dray/{name}"}, Token: tok},
		{Address: "http://x", Layout: Layout{Template: "{app}/{env}/{name}", Env: "Prod!"}, Token: tok},
	}
	for _, c := range bad {
		if _, err := New(c); err == nil {
			t.Errorf("New(%+v) succeeded", c)
		}
	}
	s, err := New(Config{Address: "https://bao.example.com/", Layout: Layout{Template: "dray/{app}/{name}"}, Token: tok})
	if err != nil {
		t.Fatal(err)
	}
	if s.String() != "openbao https://bao.example.com/ secret/dray/{app}/{name}" || s.base != "https://bao.example.com/v1/secret" {
		t.Errorf("String %q base %q", s.String(), s.base)
	}
}

func TestLayout(t *testing.T) {
	l := Layout{Template: "{client}/{app}/{env}/{name}", Clients: map[string]string{"shop": "acme"}, Env: "prod"}
	if err := l.check(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{"shop/app": "acme/shop/prod/app", "shop/feeds/dev": "acme/shop/prod/feeds/dev"}
	for in, want := range cases {
		if got := must(l.location(in)); got != want {
			t.Errorf("location(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := l.location("other/app"); err == nil || !strings.Contains(err.Error(), "no client set for app other") {
		t.Errorf("unknown client: %v", err)
	}
	l.Env = ""
	if _, err := l.location("shop/app"); err == nil || !strings.Contains(err.Error(), "--env") {
		t.Errorf("no env: %v", err)
	}
	if _, ok := l.appsBase(); ok {
		t.Error("a {client} before {app} has no fixed base")
	}
	if b, ok := (Layout{Template: "dray/{app}/{name}"}).appsBase(); !ok || b != "dray" {
		t.Errorf("appsBase = %q %v", b, ok)
	}
	if b, ok := (Layout{}).appsBase(); !ok || b != "" {
		t.Errorf("default appsBase = %q %v", b, ok)
	}
}

// TestStandardLayout runs the conformance suite with secrets per client and
// environment, as kv/<client>/<app>/<env>/<name>.
func TestStandardLayout(t *testing.T) {
	addr, token := server(t)
	storetest.Run(t, func(t *testing.T) secrets.Store {
		root := randomPrefix()
		s, err := New(Config{
			Address: addr,
			Layout: Layout{
				Template: root + "/{client}/{app}/{env}/{name}",
				Clients:  map[string]string{"shop": "acme", "shopping": "acme", "other": "beta"},
				Env:      "prod",
			},
			Token: func(context.Context) (string, error) { return token, nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}
