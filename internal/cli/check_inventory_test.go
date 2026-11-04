package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/BrutalSystems/dray/internal/config"
	"github.com/BrutalSystems/dray/internal/config/legacy"
	"github.com/BrutalSystems/dray/internal/render"
	"github.com/BrutalSystems/dray/internal/validate"
)

// TestCheckInventory renders and checks every app in a JS-tool registry,
// converting each config in memory. It runs only when DRAY_LEGACY_REGISTRY
// points at one, so no real app's details ever enter this public repo.
func TestCheckInventory(t *testing.T) {
	regPath := os.Getenv("DRAY_LEGACY_REGISTRY")
	if regPath == "" {
		t.Skip("set DRAY_LEGACY_REGISTRY to run")
	}
	var machine legacy.Defaults
	if data, err := os.ReadFile(filepath.Join(filepath.Dir(regPath), "config.json")); err == nil { //nolint:gosec // the developer's file
		machine, _ = legacy.ParseMachine(data)
	}
	data, err := os.ReadFile(regPath) //nolint:gosec // the developer's file
	if err != nil {
		t.Fatal(err)
	}
	var reg map[string]struct {
		Path   string          `json:"path"`
		Config json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(data, &reg); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(reg))
	for n := range reg {
		names = append(names, n)
	}
	sort.Strings(names)
	checker, err := validate.New(validate.Options{KubernetesVersion: os.Getenv("DRAY_KUBERNETES_VERSION")})
	if err != nil {
		t.Fatal(err)
	}
	var apps, errs, warns int
	for _, name := range names {
		e := reg[name]
		if _, err := os.Stat(e.Path); err != nil {
			t.Logf("%s: not checked out, skipped", name)
			continue
		}
		old, err := legacy.Parse(e.Config)
		if err != nil {
			t.Logf("%s: %v", name, err)
			continue
		}
		c := legacy.Convert(old, legacy.Options{Machine: machine}).Config
		if out := os.Getenv("DRAY_RENDER_OUT"); out != "" {
			writeRendered(t, filepath.Join(out, name+".yaml"), e.Path, &c)
		}
		findings, err := checkRepo(e.Path, &c, checker)
		apps++
		if err != nil {
			t.Logf("%s: render: %v", name, err)
		}
		for _, f := range findings {
			if f.Error {
				errs++
				t.Logf("%s: ERROR %s", name, f)
			} else {
				warns++
				t.Logf("%s: warning %s", name, f)
			}
		}
	}
	t.Logf("%d apps checked: %d errors, %d warnings", apps, errs, warns)
}

// writeRendered writes an app's rendered workloads (placeholder images) to
// one file, for tools such as a policy engine to read.
func writeRendered(t *testing.T, path, dir string, c *config.RepoConfig) {
	t.Helper()
	var all []render.Manifest
	for _, w := range c.Workloads {
		if w.Disabled {
			continue
		}
		refs := map[string]string{}
		for _, img := range c.Images {
			refs[img.Name] = "registry.invalid/" + img.Name + "@" + placeholderDigest
		}
		for _, u := range c.Uses {
			refs[u.LocalName()] = "registry.invalid/" + u.LocalName() + "@" + placeholderDigest
		}
		res, err := render.Workload(dir, c, w, refs)
		if err != nil {
			continue
		}
		all = append(all, res.Manifests...)
	}
	f, err := os.Create(path) //nolint:gosec // the developer's folder
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := render.Write(f, all); err != nil {
		t.Fatal(err)
	}
}
