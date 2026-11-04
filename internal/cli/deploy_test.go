package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BrutalSystems/dray/internal/config"
)

func TestDeployReadsRegistryAndTarget(t *testing.T) {
	reg := t.TempDir()
	files := map[string]string{
		"shop.yaml": "name: shop\nrepo: o/shop\nenvironments:\n  dev: {target: t1, namespace: shop-dev, release: direct, approval: none, allow_dirty: true}\n" +
			"  prod: {target: t1, namespace: shop, release: direct, approval: passkey}\n",
		"targets/t1.yaml":  "name: t1\ncloud: aws\naccount: \"123456789012\"\nregion: us-east-2\ncluster: c1\nplatform: linux/arm64\n",
		"targets/bad.yaml": "name: bad\ncloud: aws\naccount: \"12\"\nregion: x\ncluster: c\n",
	}
	for p, c := range files {
		full := filepath.Join(reg, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	e, err := loadRegistryEntry(reg, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := pickEnv(e, ""); err == nil || !strings.Contains(err.Error(), "dev, prod: name one with --env") {
		t.Errorf("two environments, none named: %v", err)
	}
	name, env, err := pickEnv(e, "dev")
	if err != nil || name != "dev" || !env.AllowDirty {
		t.Errorf("%s %+v %v", name, env, err)
	}
	if tg, err := loadTarget(reg, "t1"); err != nil || tg.Cluster != "c1" {
		t.Errorf("%+v %v", tg, err)
	}
	if _, err := loadTarget(reg, "bad"); err == nil || !strings.Contains(err.Error(), "12-digit") {
		t.Errorf("bad target: %v", err)
	}
	if _, err := loadRegistryEntry(reg, "other"); err == nil || !strings.Contains(err.Error(), "no registry entry for other") {
		t.Errorf("missing entry: %v", err)
	}
}

func TestNeedsStore(t *testing.T) {
	c := &config.RepoConfig{Workloads: []config.Workload{
		{Name: "a"}, {Name: "b", Secrets: []string{"s"}, Manual: true},
	}}
	if needsStore(c, nil) {
		t.Error("only a manual workload uses secrets")
	}
	if !needsStore(c, []string{"b"}) {
		t.Error("b named, and it uses a secret")
	}
}
