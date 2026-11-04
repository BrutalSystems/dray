package legacy

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BrutalSystems/dray/internal/config"
)

var update = flag.Bool("update", false, "rewrite golden files")

func load(t *testing.T, name string) *Config {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // test fixture
	if err != nil {
		t.Fatal(err)
	}
	c, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func machine(t *testing.T) Defaults {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "machine.json"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := ParseMachine(data)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// render writes everything a conversion produces, so one golden file shows
// the whole result for review.
func render(t *testing.T, r *Result) []byte {
	t.Helper()
	var b bytes.Buffer
	section := func(title string, v any, c *config.Comments) {
		out, err := config.EncodeYAML(v, c)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "# --- %s\n%s\n", title, out)
	}
	section(".dray/config.yaml", r.Config, &r.Comments)
	if r.Profile != "" {
		fmt.Fprintf(&b, "# --- personal AWS profile\n%s\n", r.Profile)
	}
	b.WriteString("# --- warnings\n")
	for _, w := range r.Warnings {
		fmt.Fprintf(&b, "- %s\n", w)
	}
	b.WriteString("# --- validation\n")
	if err := r.Config.Validate(); err != nil {
		b.WriteString(err.Error() + "\n")
	} else {
		b.WriteString("ok\n")
	}
	return b.Bytes()
}

func TestConvertGolden(t *testing.T) {
	pins := map[string]string{"queue": "abc1234"}
	for _, name := range []string{"monorepo", "frontend", "envcopies"} {
		t.Run(name, func(t *testing.T) {
			r := Convert(load(t, name+".json"), Options{
				Machine:       machine(t),
				RunningCommit: func(img string) string { return pins[img] },
				BuildSources:  siblingLib,
			})
			got := render(t, r)
			path := filepath.Join("testdata", name+".golden")
			if *update {
				if err := os.WriteFile(path, got, 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path) //nolint:gosec // test fixture
			if err != nil {
				t.Fatalf("%v (run with -update)", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("%s differs from the golden file; run go test ./internal/config/legacy -update and review the diff\n\n%s", name, got)
			}
		})
	}
}

// siblingLib stands in for the CLI's detection of a shared library cloned
// beside the repo.
func siblingLib(img Image) []config.BuildSource {
	if img.Name == "booking-api" {
		return []config.BuildSource{{Repo: "example/shared-lib", Path: "shared-lib", Commit: "def5678"}}
	}
	return nil
}

func TestParentContextWithoutSourcesIsFlagged(t *testing.T) {
	r := Convert(load(t, "envcopies.json"), Options{Machine: machine(t)})
	for _, w := range r.Warnings {
		if strings.Contains(w.Message, "If the build needs other repos beside it, list them in sources") {
			return
		}
	}
	t.Fatalf("want a warning for a parent context with no sources, got %v", r.Warnings)
}

func TestConvertedConfigsRoundTrip(t *testing.T) {
	for _, name := range []string{"monorepo", "frontend"} {
		r := Convert(load(t, name+".json"), Options{
			Machine:       machine(t),
			RunningCommit: func(string) string { return "abc1234" },
		})
		out, err := config.EncodeYAML(r.Config, &r.Comments)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := config.ParseRepoConfig(out); err != nil {
			t.Errorf("%s: written config does not read back: %v\n%s", name, err, out)
		}
	}
}

func TestParseRejectsUnknownKeys(t *testing.T) {
	_, err := Parse([]byte(`{"name":"x","images":[{"name":"a","ecr":"a","source":{"local":true},"buildArg":{}}]}`))
	if err == nil || !strings.Contains(err.Error(), "buildArg") {
		t.Fatalf("want an error naming the unknown key, got %v", err)
	}
}

func TestParseKeepsNotes(t *testing.T) {
	c := load(t, "monorepo.json")
	if c.Notes["_note"] == "" {
		t.Error("top-level _note was dropped")
	}
	if len(c.Workloads[0].Notes) != 1 {
		t.Errorf("workload note: got %v", c.Workloads[0].Notes)
	}
}

func TestEmptyPiletsList(t *testing.T) {
	c := load(t, "envcopies.json")
	if c.Pilets != nil {
		t.Errorf(`"pilets": [] should mean no pilets, got %+v`, c.Pilets)
	}
}

func TestUnpinnedComponentNeedsAPerson(t *testing.T) {
	r := Convert(load(t, "monorepo.json"), Options{Machine: machine(t)})
	err := r.Config.Validate()
	if err == nil || !strings.Contains(err.Error(), "pin queue-service") {
		t.Fatalf("an unpinned component must fail validation, got %v", err)
	}
}

func TestComponentFromGit(t *testing.T) {
	cases := map[string]string{
		"https://github.com/example/queue-service":     "queue-service",
		"https://github.com/example/Queue-Service.git": "queue-service",
		"git@github.com:example/queue.git":             "queue",
		"https://gitlab.com/group/sub/thing/":          "thing",
	}
	for in, want := range cases {
		if got := componentFromGit(in); got != want {
			t.Errorf("componentFromGit(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRealConfigs converts every config in a JS-tool registry. It runs only
// when DRAY_LEGACY_REGISTRY points at one (such as ~/.dray/registry.json), so
// no real app's details ever enter this public repo.
func TestRealConfigs(t *testing.T) {
	path := os.Getenv("DRAY_LEGACY_REGISTRY")
	if path == "" {
		t.Skip("set DRAY_LEGACY_REGISTRY to a JS-tool registry.json to run")
	}
	entries := readRegistry(t, path)
	var failed int
	for name, raw := range entries {
		c, err := Parse(raw)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			failed++
			continue
		}
		r := Convert(c, Options{RunningCommit: func(string) string { return "abc1234" }})
		if err := r.Config.Validate(); err != nil {
			t.Logf("%s: needs a person: %v", name, err)
			failed++
		}
		out, err := config.EncodeYAML(r.Config, &r.Comments)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if r.Config.Validate() == nil {
			if _, err := config.ParseRepoConfig(out); err != nil {
				t.Errorf("%s: written config does not read back: %v", name, err)
			}
		}
		t.Logf("%s: %d warning(s)", name, len(r.Warnings))
	}
	t.Logf("%d of %d configs convert and validate without help", len(entries)-failed, len(entries))
}

func TestSecretSourcesMatchConvertedReferences(t *testing.T) {
	for _, name := range []string{"monorepo", "frontend", "envcopies"} {
		old := load(t, name+".json")
		r := Convert(old, Options{Machine: machine(t)})
		refs := map[string]bool{}
		for _, s := range r.Config.Secrets {
			refs[string(s.From)] = true
		}
		for _, img := range r.Config.Images {
			if img.BuildArgs != nil && img.BuildArgs.FromSecret != "" {
				refs[string(img.BuildArgs.FromSecret)] = true
			}
		}
		if r.Config.Pilets != nil && r.Config.Pilets.Env != "" {
			refs[string(r.Config.Pilets.Env)] = true
		}
		sources := SecretSources(old)
		if len(sources) != len(refs) {
			t.Errorf("%s: %d sources for %d references", name, len(sources), len(refs))
		}
		for _, s := range sources {
			if !refs["secret:"+s.Path] {
				t.Errorf("%s: source %s matches no reference in the converted config (%v)", name, s.Path, refs)
			}
		}
	}
}
