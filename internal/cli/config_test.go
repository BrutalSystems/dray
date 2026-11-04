package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runConfig(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRoot("dray", "test")
	root.AddCommand(NewConfigCmd())
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func writeRepo(t *testing.T, legacyJSON string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".dray"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".dray", "config.json"), []byte(legacyJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

const legacyRepo = `{
  "name": "shop",
  "defaults": {"namespace": "team-a", "context": "main", "account": "111111111111", "region": "us-east-2"},
  "images": [{"name": "api", "ecr": "api", "source": {"local": true}, "dockerfile": "Dockerfile", "context": "."}],
  "workloads": [{"name": "api", "kind": "deployment", "image": "api", "manifests": [".k8s/api.yaml"]}]
}`

func TestMigrateDryRunWritesNothing(t *testing.T) {
	dir := writeRepo(t, legacyRepo)
	out, err := runConfig(t, "config", "migrate", dir, "--repo", "example/shop", "--machine-config", filepath.Join(dir, "none.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "name: shop") || !strings.Contains(out, "repo: example/shop") {
		t.Errorf("unexpected output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".dray", "config.yaml")); !os.IsNotExist(err) {
		t.Error("migrate without --write must not write config.yaml")
	}
}

func TestMigrateWriteThenValidate(t *testing.T) {
	dir := writeRepo(t, legacyRepo)
	if _, err := runConfig(t, "config", "migrate", dir, "--write", "--repo", "example/shop", "--machine-config", filepath.Join(dir, "none.json")); err != nil {
		t.Fatal(err)
	}
	out, err := runConfig(t, "config", "validate", dir)
	if err != nil {
		t.Fatalf("validate after migrate: %v", err)
	}
	if !strings.Contains(out, "ok") {
		t.Errorf("validate output: %q", out)
	}
}

func TestMigrateRefusesToWriteAnInvalidResult(t *testing.T) {
	dir := writeRepo(t, `{"name":"shop","images":[{"name":"api","ecr":"api","source":{"local":true},"context":".."}],
	  "workloads":[{"name":"api","kind":"deployment","image":"api","manifests":["a.yaml"]}]}`)
	_, err := runConfig(t, "config", "migrate", dir, "--write", "--repo", "example/shop", "--machine-config", filepath.Join(dir, "none.json"))
	if err == nil || !strings.Contains(err.Error(), "not written") {
		t.Fatalf("want a refusal, got %v", err)
	}
}

func TestSchemaCommand(t *testing.T) {
	for _, which := range []string{"config", "registry"} {
		out, err := runConfig(t, "config", "schema", which)
		if err != nil || !strings.Contains(out, `"$schema"`) {
			t.Errorf("schema %s: err=%v out=%.80s", which, err, out)
		}
	}
}
