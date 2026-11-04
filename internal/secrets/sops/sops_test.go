package sops

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BrutalSystems/dray/internal/secrets"
	"github.com/BrutalSystems/dray/internal/secrets/storetest"
)

// fakeSops "decrypts" by reading the file as it is.
func fakeSops(t *testing.T) Runner {
	return func(_ context.Context, dir string, args ...string) ([]byte, error) {
		t.Helper()
		if args[0] != "-d" {
			t.Fatalf("unexpected sops call %v", args)
		}
		return os.ReadFile(filepath.Join(dir, strings.TrimPrefix(args[len(args)-1], "./"))) //nolint:gosec // test files
	}
}

func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, c := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestFormats(t *testing.T) {
	dir := repo(t, map[string]string{ //nolint:gosec // test fixtures, not credentials
		"k8s/secret.enc.yaml": "apiVersion: v1\nkind: Secret\nmetadata:\n  name: app\ndata:\n  DB_URL: cG9zdGdyZXM6Ly94\nstringData:\n  TOKEN: abc\n",
		"build.enc.env":       "VITE_API=\"https://api\"\nVITE_KEY='k'\nOTHER=x\n# note\n",
		"pilets.enc.yaml":     `{"FEED_KEY":"f","PORT":8080}`,
		".env.secret":         "\xef\xbb\xbfA=1\n  # comment\n\nB= spaced \"q\" \nC=x=y\n",
	})
	s := &Store{Dir: dir, Run: fakeSops(t), Sources: map[string]Source{
		"shop/app":       {File: "k8s/secret.enc.yaml", Format: Manifest, SecretName: "app"},
		"shop/build-web": {File: "build.enc.env", Format: Dotenv, Prefix: "VITE_"},
		"shop/pilets":    {File: "pilets.enc.yaml", Format: ExecEnv},
		"shop/env":       {File: ".env.secret", Format: EnvFile},
	}}
	want := map[string]secrets.Values{
		"shop/app":       {"DB_URL": "postgres://x", "TOKEN": "abc"},
		"shop/build-web": {"VITE_API": "https://api", "VITE_KEY": "k"},
		"shop/pilets":    {"FEED_KEY": "f", "PORT": "8080"},
		"shop/env":       {"A": "1", "B": " spaced \"q\" ", "C": "x=y"},
	}
	ctx := context.Background()
	for p, w := range want {
		got, err := s.Get(ctx, p)
		if err != nil {
			t.Errorf("%s: %v", p, err)
			continue
		}
		if !got.Equal(w) {
			t.Errorf("%s = %q, want %q", p, got, w)
		}
	}
	list, _ := s.List(ctx, "shop")
	if len(list) != 4 {
		t.Errorf("List = %v", list)
	}
	if _, err := s.Get(ctx, "shop/none"); !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	storetest.RunReadOnly(t, s)
}

func TestRefusals(t *testing.T) {
	outside := repo(t, map[string]string{"x.env": "A=1\n"})
	dir := repo(t, map[string]string{
		"two.yaml":   "kind: Secret\nmetadata: {name: a}\n---\nkind: Secret\nmetadata: {name: b}\n",
		"other.yaml": "kind: Secret\nmetadata: {name: other}\nstringData: {A: '1'}\n",
		"bare.env":   "A=1\nFROM_ENV\n",
		"nested":     `{"A":{"b":1}}`,
	})
	if err := os.Symlink(filepath.Join(outside, "x.env"), filepath.Join(dir, "link.env")); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		src  Source
		want string
	}{
		"several secrets":   {Source{File: "two.yaml", Format: Manifest, SecretName: "a"}, "holds 2 Secrets (a, b)"},
		"wrong secret name": {Source{File: "other.yaml", Format: Manifest, SecretName: "app"}, `named "other", not "app"`},
		"value from env":    {Source{File: "bare.env", Format: EnvFile}, "FROM_ENV has no value"},
		"nested exec-env":   {Source{File: "nested", Format: ExecEnv}, "nested value"},
		"path out":          {Source{File: "../x.env", Format: EnvFile}, "outside"},
		"symlink out":       {Source{File: "link.env", Format: EnvFile}, "leads outside"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := &Store{Dir: dir, Run: fakeSops(t), Sources: map[string]Source{"shop/app": tc.src}}
			_, err := s.Get(context.Background(), "shop/app")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error containing %q, got %v", tc.want, err)
			}
		})
	}
}

// TestRealSops encrypts with a throwaway age key and decrypts with the real
// sops binary, when sops and age-keygen are installed.
func TestRealSops(t *testing.T) {
	for _, bin := range []string{"sops", "age-keygen"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "key.txt")
	out, err := exec.Command("age-keygen", "-o", key).CombinedOutput() //nolint:gosec // test
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	pub := ""
	for _, line := range strings.Split(string(out), "\n") {
		if p, ok := strings.CutPrefix(line, "Public key: "); ok {
			pub = p
		}
	}
	plain := filepath.Join(dir, "secret.yaml")
	if err := os.WriteFile(plain, []byte("apiVersion: v1\nkind: Secret\nmetadata:\n  name: app\nstringData:\n  A: \"1\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	enc, err := exec.Command("sops", "-e", "--age", pub, "--encrypted-regex", "^(data|stringData)$", plain).Output() //nolint:gosec // test
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secret.enc.yaml"), enc, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SOPS_AGE_KEY_FILE", key)
	s := &Store{Dir: dir, Sources: map[string]Source{"shop/app": {File: "secret.enc.yaml", Format: Manifest, SecretName: "app"}}}
	v, err := s.Get(context.Background(), "shop/app")
	if err != nil {
		t.Fatal(err)
	}
	if !v.Equal(secrets.Values{"A": "1"}) {
		t.Errorf("got %q", v)
	}
}
