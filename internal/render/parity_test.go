package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/BrutalSystems/dray/internal/config/legacy"
)

// TestParityWithJSTool renders every workload of every repo in a JS-tool
// registry twice, once with the JS tool's own code and once with this
// package, and requires the same bytes. It then renders by digest and requires
// that only the image references changed.
//
// It runs only when DRAY_LEGACY_REGISTRY points at a registry.json and
// DRAY_JS_TOOL at a checkout of the JS tool, so no real app's details enter
// this public repo. The machine config defaults to config.json beside the
// registry; set DRAY_LEGACY_MACHINE to override.
func TestParityWithJSTool(t *testing.T) {
	regPath, toolDir := os.Getenv("DRAY_LEGACY_REGISTRY"), os.Getenv("DRAY_JS_TOOL")
	if regPath == "" || toolDir == "" {
		t.Skip("set DRAY_LEGACY_REGISTRY and DRAY_JS_TOOL to run")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	machinePath := os.Getenv("DRAY_LEGACY_MACHINE")
	if machinePath == "" {
		machinePath = filepath.Join(filepath.Dir(regPath), "config.json")
	}
	var machine legacy.Defaults
	if data, err := os.ReadFile(machinePath); err == nil { //nolint:gosec // path given by the developer running the test
		if machine, err = legacy.ParseMachine(data); err != nil {
			t.Fatal(err)
		}
	} else {
		machinePath = "-"
	}

	data, err := os.ReadFile(regPath) //nolint:gosec // path given by the developer running the test
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

	const tag = "parity0000000"
	var repos, workloads, files int
	for _, name := range names {
		entry := reg[name]
		if _, err := os.Stat(entry.Path); err != nil {
			t.Logf("%s: not checked out here, skipped", name)
			continue
		}
		old, err := legacy.Parse(entry.Config)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		js := jsRender(t, toolDir, regPath, machinePath, name, tag)
		conv := legacy.Convert(old, legacy.Options{Machine: machine})
		host := ""
		for _, tg := range conv.Config.Targets {
			if tg.Account != "" {
				host = fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com", tg.Account, tg.Region)
			}
		}
		ecr := map[string]string{}
		for _, img := range old.Images {
			ecr[img.Name] = host + "/" + img.ECR
		}
		repos++
		for _, w := range conv.Config.Workloads {
			if w.Disabled {
				continue
			}
			want, ok := js[w.Name]
			if !ok {
				t.Errorf("%s/%s: the JS tool rendered no such workload", name, w.Name)
				continue
			}
			tagRefs, digestRefs := map[string]string{}, map[string]string{}
			for _, img := range w.Images {
				tagRefs[img] = ecr[img] + ":" + tag
				digestRefs[img] = ecr[img] + "@" + digest
			}
			got, err := Workload(entry.Path, &conv.Config, w, tagRefs)
			if want.Error != "" {
				if err == nil {
					t.Errorf("%s/%s: the JS tool failed (%s), this did not", name, w.Name, want.Error)
				} else {
					t.Logf("%s/%s: both fail: %v", name, w.Name, err)
				}
				continue
			}
			if err != nil {
				t.Errorf("%s/%s: %v", name, w.Name, err)
				continue
			}
			workloads++
			if fmt.Sprint(got.Vars) != fmt.Sprint(want.Vars) {
				t.Errorf("%s/%s: placeholders differ\n go: %v\n js: %v", name, w.Name, got.Vars, want.Vars)
			}
			if len(got.Manifests) != len(want.Files) {
				t.Errorf("%s/%s: %d files, JS tool %d", name, w.Name, len(got.Manifests), len(want.Files))
				continue
			}
			byDigest, err := Workload(entry.Path, &conv.Config, w, digestRefs)
			if err != nil {
				t.Errorf("%s/%s by digest: %v", name, w.Name, err)
				continue
			}
			for i, m := range got.Manifests {
				files++
				if m.Path != want.Files[i].Path || string(m.Content) != want.Files[i].Text {
					t.Errorf("%s/%s: %s differs from the JS tool's rendering", name, w.Name, m.Path)
				}
				back := bytes.ReplaceAll(byDigest.Manifests[i].Content, []byte("@"+digest), []byte(":"+tag))
				if !bytes.Equal(back, m.Content) {
					t.Errorf("%s/%s: %s changed beyond the image references when rendered by digest", name, w.Name, m.Path)
				}
			}
			for _, u := range got.Unfilled {
				t.Logf("%s/%s: unfilled %s", name, w.Name, u)
			}
		}
	}
	t.Logf("%d repos, %d workloads, %d files rendered", repos, workloads, files)
}

type jsWorkload struct {
	Vars  map[string]string `json:"vars"`
	Error string            `json:"error"`
	Files []struct {
		Path string `json:"path"`
		Text string `json:"text"`
	} `json:"files"`
}

func jsRender(t *testing.T, toolDir, regPath, machinePath, repo, tag string) map[string]jsWorkload {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("testdata", "jsrender.js"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", script, toolDir, regPath, machinePath, repo, tag) //nolint:gosec // developer-run test, fixed script
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s: JS tool: %v\n%s", repo, err, stderr.String())
	}
	var res map[string]jsWorkload
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("%s: %v", repo, err)
	}
	return res
}
