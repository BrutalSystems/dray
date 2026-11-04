package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runSecrets(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRoot("dray", "test")
	root.AddCommand(NewSecretsCmd())
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"secrets"}, args...))
	err := root.Execute()
	return out.String(), err
}

func TestSecretsImport(t *testing.T) {
	repo := writeRepo(t, `{
  "name": "shop",
  "defaults": {"namespace": "team-a", "context": "main"},
  "secrets": [{"name": "app", "kind": "literal-from-env", "file": ".env.app"}]
}`)
	envFile := filepath.Join(repo, ".env.app")
	if err := os.WriteFile(envFile, []byte("DB_PASSWORD=hunter2\nAPI_KEY=k1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	storeDir := filepath.Join(t.TempDir(), "store")
	if err := os.WriteFile(cfg, []byte("secret_store:\n  type: file\n  file:\n    dir: "+storeDir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	step := func(name string, args []string, want ...string) {
		t.Helper()
		out, err := runSecrets(t, append([]string{"--config", cfg}, args...)...)
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
		if strings.Contains(out, "hunter2") {
			t.Fatalf("%s printed a value:\n%s", name, out)
		}
		for _, w := range want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: missing %q in:\n%s", name, w, out)
			}
		}
	}

	step("dry run", []string{"import", repo}, "shop/app  <- .env.app (env-file)", "keys: API_KEY, DB_PASSWORD", "would write (new)", "Dry run")
	if _, err := os.Stat(filepath.Join(storeDir, "shop", "app.json")); err == nil {
		t.Fatal("a dry run wrote to the store")
	}
	step("write", []string{"import", repo, "--write"}, "written (new)", "1 written")
	step("again", []string{"import", repo, "--write"}, "already in the store with the same values")
	step("list", []string{"list"}, "shop/app")
	step("keys", []string{"keys", "secret:shop/app"}, "API_KEY\nDB_PASSWORD\n")

	if err := os.WriteFile(envFile, []byte("DB_PASSWORD=changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	step("differs", []string{"import", repo, "--write"}, "kept (--overwrite replaces it)", "1 kept")
	step("overwrite", []string{"import", repo, "--write", "--overwrite"}, "written (differs)")
}

func TestSecretsNeedsAStore(t *testing.T) {
	_, err := runSecrets(t, "--config", filepath.Join(t.TempDir(), "none.yaml"), "list")
	if err == nil || !strings.Contains(err.Error(), "no secret store configured") {
		t.Fatalf("got %v", err)
	}
}

func TestSecretsImportReportsUnreadable(t *testing.T) {
	repo := writeRepo(t, `{"name": "shop", "secrets": [{"name": "app", "kind": "literal-from-env", "file": "../outside.env"}]}`)
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte("secret_store:\n  type: file\n  file:\n    dir: "+t.TempDir()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runSecrets(t, "--config", cfg, "import", repo)
	if err == nil || !strings.Contains(out, "cannot read") {
		t.Fatalf("want a read failure, got %v\n%s", err, out)
	}
}
