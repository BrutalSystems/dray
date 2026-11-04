package deploy

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BrutalSystems/dray/internal/config"
	"github.com/BrutalSystems/dray/internal/secrets"
)

const digestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const digestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

type fakeRegistry struct {
	tags     map[string]string // repo:tag -> digest
	ensured  []string
	loggedIn bool
	err      error
}

func (r *fakeRegistry) Host() string                { return "reg.example.com" }
func (r *fakeRegistry) Login(context.Context) error { r.loggedIn = true; return nil }
func (r *fakeRegistry) Ensure(_ context.Context, repo string) error {
	r.ensured = append(r.ensured, repo)
	return nil
}
func (r *fakeRegistry) Digest(_ context.Context, repo, tag string) (string, error) {
	return r.tags[repo+":"+tag], r.err
}

type fakeBuilder struct {
	built []BuildSpec
	err   error
	reg   *fakeRegistry // when set with err, the "other" push lands here
}

func (b *fakeBuilder) Build(_ context.Context, s BuildSpec) (string, error) {
	b.built = append(b.built, s)
	if b.err != nil {
		if b.reg != nil {
			repoTag := strings.TrimPrefix(s.Ref, "reg.example.com/")
			b.reg.tags[repoTag] = digestB
		}
		return "", b.err
	}
	return digestB, nil
}

type fakeCluster struct {
	changed   map[string]bool // substring of a doc -> diff says changed
	applied   [][]byte
	state     map[string][]DeploymentState // name -> states returned in turn
	restarted []string
	waited    []string
	undone    []string
	waitErr   error
}

// Diff matches the longest key the doc contains, so "kind: Secret" wins
// over "name: api" for the api-secret Secret.
func (c *fakeCluster) Diff(_ context.Context, docs []byte) (bool, string, error) {
	best := ""
	for sub := range c.changed {
		if bytes.Contains(docs, []byte(sub)) && (len(sub) > len(best) || len(sub) == len(best) && sub < best) {
			best = sub
		}
	}
	if best != "" {
		return c.changed[best], "diff of " + best, nil
	}
	return true, "new", nil
}
func (c *fakeCluster) Apply(_ context.Context, docs []byte) error {
	c.applied = append(c.applied, docs)
	return nil
}
func (c *fakeCluster) Deployment(_ context.Context, name string) (DeploymentState, error) {
	ss := c.state[name]
	if len(ss) == 0 {
		return DeploymentState{Found: true, Generation: 2, ObservedGeneration: 2, Replicas: 1, Updated: 1, Available: 1}, nil
	}
	s := ss[0]
	if len(ss) > 1 {
		c.state[name] = ss[1:]
	}
	return s, nil
}
func (c *fakeCluster) Restart(_ context.Context, name string) error {
	c.restarted = append(c.restarted, name)
	return nil
}
func (c *fakeCluster) Wait(_ context.Context, name string, _ time.Duration) error {
	c.waited = append(c.waited, name)
	return c.waitErr
}
func (c *fakeCluster) Undo(_ context.Context, name string) error {
	c.undone = append(c.undone, name)
	return nil
}

type fakeSource struct {
	commit string
	dirty  bool
	root   string
	opened int
}

func (s *fakeSource) Commit(context.Context) (string, bool, error) { return s.commit, s.dirty, nil }
func (s *fakeSource) Workspace(context.Context, bool) (Workspace, error) {
	s.opened++
	return fakeWS{root: s.root}, nil
}

type fakeWS struct{ root string }

func (w fakeWS) Root() string { return w.root }
func (w fakeWS) Repo() string { return filepath.Join(w.root, "shop") }
func (w fakeWS) Close() error { return nil }

type fakeCI struct {
	commits []string
	runs    []CIRun
	err     error
}

func (c *fakeCI) Commits(context.Context) ([]string, error) { return c.commits, c.err }
func (c *fakeCI) Runs(context.Context) ([]CIRun, error)     { return c.runs, c.err }

const repoYAML = `name: shop
images:
  - name: api
    repository: shop-api
    build_args:
      values: {MODE: full}
  - name: web
    context: web
    build_per_environment: true
uses:
  - component: jobs
    commit: abc1234
workloads:
  - name: api
    kind: deployment
    images: [api]
    manifests: [k8s/api.yaml]
    secrets: [api-secret]
  - name: web
    kind: deployment
    images: [web]
    manifests: [k8s/web.yaml]
    depends_on: [api]
  - name: nightly
    kind: cronjob
    images: [api]
    manifests: [k8s/nightly.yaml]
    manual: true
  - name: jobs
    kind: deployment
    images: [jobs]
    manifests: [k8s/jobs.yaml]
    manual: true
  - name: old
    kind: deployment
    manifests: [k8s/old.yaml]
    disabled: true
secrets:
  - name: api-secret
    from: secret:shop/api
`

type harness struct {
	o   Options
	reg *fakeRegistry
	b   *fakeBuilder
	cl  *fakeCluster
	src *fakeSource
	ci  *fakeCI
	out *bytes.Buffer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"k8s/api.yaml":     "kind: Deployment\nmetadata: {name: api}\nimage: ${API_IMAGE}\n",
		"k8s/web.yaml":     "kind: Deployment\nmetadata: {name: web}\nimage: ${WEB_IMAGE}\n",
		"k8s/nightly.yaml": "kind: CronJob\nmetadata: {name: nightly}\nimage: ${API_IMAGE}\n",
		"k8s/jobs.yaml":    "kind: Deployment\nimage: ${JOBS_IMAGE}\n",
		"k8s/old.yaml":     "kind: Deployment\n",
	}
	for p, c := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c, err := config.ParseRepoConfig([]byte(repoYAML))
	if err != nil {
		t.Fatal(err)
	}
	store := secrets.NewMemory()
	if err := store.Put(context.Background(), "shop/api", secrets.Values{"TOKEN": "s3cret"}); err != nil {
		t.Fatal(err)
	}
	h := &harness{
		reg: &fakeRegistry{tags: map[string]string{}},
		b:   &fakeBuilder{},
		cl:  &fakeCluster{changed: map[string]bool{}, state: map[string][]DeploymentState{}},
		src: &fakeSource{commit: "1234567", root: t.TempDir()},
		ci:  &fakeCI{},
		out: &bytes.Buffer{},
	}
	h.o = Options{
		Repo: c, RepoDir: dir, EnvName: "prod",
		Env:    config.Environment{Namespace: "shop", Target: "t"},
		Target: config.Target{Name: "t", Cluster: "ctx", Platform: "linux/arm64"},
		Out:    h.out, Registry: h.reg, Builder: h.b, Cluster: h.cl, Source: h.src, CI: h.ci, Secrets: store,
		Now: func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	}
	return h
}

func (h *harness) run(t *testing.T) error {
	t.Helper()
	return Run(context.Background(), h.o)
}

func (h *harness) appliedText() string {
	var b strings.Builder
	for _, d := range h.cl.applied {
		b.Write(d)
		b.WriteString("\n")
	}
	return b.String()
}

func TestDeployBuildsPushesAppliesAndRollsOut(t *testing.T) {
	h := newHarness(t)
	// After the apply, each Deployment is rolling: it must be waited on, not restarted.
	rolling := DeploymentState{Found: true, Generation: 3, ObservedGeneration: 2, Replicas: 1}
	h.cl.state["api"] = []DeploymentState{rolling}
	h.cl.state["web"] = []DeploymentState{rolling}
	if err := h.run(t); err != nil {
		t.Fatalf("%v\n%s", err, h.out)
	}
	if len(h.b.built) != 2 || !h.reg.loggedIn || h.src.opened != 1 {
		t.Fatalf("built %d, logged in %v, workspaces %d", len(h.b.built), h.reg.loggedIn, h.src.opened)
	}
	api := h.b.built[0]
	if api.Ref != "reg.example.com/shop-api:1234567" || api.Args["GIT_SHA"] != "1234567" || api.Args["MODE"] != "full" || api.Platform != "linux/arm64" {
		t.Errorf("api build %+v", api)
	}
	if web := h.b.built[1]; web.Ref != "reg.example.com/web:1234567-prod" || web.Context != filepath.Join(h.src.root, "shop", "web") {
		t.Errorf("web build %+v", web)
	}
	text := h.appliedText()
	for _, want := range []string{
		"image: reg.example.com/shop-api:1234567@" + digestB,
		"image: reg.example.com/web:1234567-prod@" + digestB,
		"kind: Secret", "TOKEN: czNjcmV0", // base64 of s3cret
	} {
		if !strings.Contains(text, want) {
			t.Errorf("applied manifests lack %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "nightly") || strings.Contains(text, "JOBS_IMAGE") {
		t.Errorf("manual workloads were deployed:\n%s", text)
	}
	if strings.Contains(h.out.String(), "s3cret") {
		t.Error("a secret value reached the output")
	}
	if len(h.cl.restarted) != 0 || strings.Join(h.cl.waited, ",") != "api,web" {
		t.Errorf("restarted %v, waited %v", h.cl.restarted, h.cl.waited)
	}
}

func TestImageAlreadyInRegistryIsReused(t *testing.T) {
	h := newHarness(t)
	h.reg.tags["shop-api:1234567"] = digestA
	h.reg.tags["web:1234567-prod"] = digestA
	if err := h.run(t); err != nil {
		t.Fatal(err)
	}
	if len(h.b.built) != 0 || h.src.opened != 0 || h.reg.loggedIn {
		t.Errorf("rebuilt an image the registry has: %d builds", len(h.b.built))
	}
	if !strings.Contains(h.appliedText(), "shop-api:1234567@"+digestA) {
		t.Error("the registry's digest was not deployed")
	}
}

func TestConcurrentPushDeploysTheRegistrysImage(t *testing.T) {
	h := newHarness(t)
	h.b.err = errors.New("tag already exists in immutable repository")
	h.b.reg = h.reg
	if err := h.run(t); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "pushed by someone else") {
		t.Error(h.out.String())
	}
}

func TestRegistryUnreadableStops(t *testing.T) {
	h := newHarness(t)
	h.reg.err = errors.New("AccessDenied")
	if err := h.run(t); err == nil || len(h.b.built) != 0 {
		t.Fatalf("err %v, builds %d: an unreadable registry must not read as missing", err, len(h.b.built))
	}
}

func TestDirtyTree(t *testing.T) {
	h := newHarness(t)
	h.src.dirty = true
	if err := h.run(t); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("dirty without --allow-dirty: %v", err)
	}
	h.o.AllowDirty = true
	if err := h.run(t); err == nil || !strings.Contains(err.Error(), "does not take builds from uncommitted changes") {
		t.Fatalf("dirty to an environment without allow_dirty: %v", err)
	}
	h.o.Env.AllowDirty = true
	h.reg.tags["shop-api:1234567"] = digestA // never reused for a dirty build
	if err := h.run(t); err != nil {
		t.Fatal(err)
	}
	if len(h.b.built) != 2 || h.b.built[0].Ref != "reg.example.com/shop-api:1234567-dirty-20260102030405" {
		t.Errorf("dirty builds %+v", h.b.built)
	}
}

func TestCIGate(t *testing.T) {
	green := []CIRun{{HeadSha: "c2", Status: "completed", Conclusion: "success", WorkflowName: "ci"}}
	cases := map[string]struct {
		ci     fakeCI
		inCI   bool
		skip   bool
		wantOK bool
		want   string
	}{
		"green ancestor": {ci: fakeCI{commits: []string{"c1", "c2"}, runs: green}, wantOK: true},
		"red":            {ci: fakeCI{commits: []string{"c1"}, runs: []CIRun{{HeadSha: "c1", Status: "completed", Conclusion: "failure", WorkflowName: "ci"}}}, want: "CI failed for c1: ci=failure"},
		"pending":        {ci: fakeCI{commits: []string{"c1"}, runs: []CIRun{{HeadSha: "c1", Status: "in_progress", WorkflowName: "ci"}}}, want: "still running"},
		"absent":         {ci: fakeCI{commits: []string{"c1"}}, want: "no CI run found"},
		"unreadable":     {ci: fakeCI{err: errors.New("gh is not signed in")}, want: "could not check CI"},
		"inside CI":      {ci: fakeCI{err: errors.New("never asked")}, inCI: true, wantOK: true},
		"skipped":        {ci: fakeCI{err: errors.New("never asked")}, skip: true, wantOK: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.o.Env.CIRequired = true
			h.o.InCI, h.o.SkipCI = tc.inCI, tc.skip
			*h.ci = tc.ci
			err := h.run(t)
			if tc.wantOK != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("got %v, want ok=%v %q", err, tc.wantOK, tc.want)
			}
			if !tc.wantOK && (len(h.b.built) > 0 || len(h.cl.applied) > 0) {
				t.Error("a blocked deploy built or applied something")
			}
		})
	}
}

func TestRolloutRules(t *testing.T) {
	steady := DeploymentState{Found: true, Generation: 2, ObservedGeneration: 2, Replicas: 2, Updated: 2, Available: 2}
	t.Run("unchanged workloads are left alone", func(t *testing.T) {
		h := newHarness(t)
		h.reg.tags["shop-api:1234567"], h.reg.tags["web:1234567-prod"] = digestA, digestA
		h.cl.changed = map[string]bool{"kind: Secret\n": false, "name: api": false, "name: web": false}
		if err := h.run(t); err != nil {
			t.Fatal(err)
		}
		if len(h.cl.applied)+len(h.cl.restarted)+len(h.cl.waited) != 0 {
			t.Errorf("applied %d, restarted %v, waited %v", len(h.cl.applied), h.cl.restarted, h.cl.waited)
		}
	})
	t.Run("a changed secret restarts the workloads using it", func(t *testing.T) {
		h := newHarness(t)
		h.cl.changed = map[string]bool{"kind: Secret\n": true, "name: api": false, "name: web": false}
		h.cl.state["api"] = []DeploymentState{steady}
		if err := h.run(t); err != nil {
			t.Fatal(err)
		}
		if strings.Join(h.cl.restarted, ",") != "api" || strings.Join(h.cl.waited, ",") != "api" {
			t.Errorf("restarted %v, waited %v", h.cl.restarted, h.cl.waited)
		}
	})
	t.Run("--restart restarts what is not rolling", func(t *testing.T) {
		h := newHarness(t)
		h.o.Restart = true
		h.cl.changed = map[string]bool{"kind: Secret\n": false, "name: api": false, "name: web": false}
		if err := h.run(t); err != nil {
			t.Fatal(err)
		}
		if strings.Join(h.cl.restarted, ",") != "api,web" {
			t.Errorf("restarted %v", h.cl.restarted)
		}
	})
	t.Run("a timed-out wait leaves the rollout alone", func(t *testing.T) {
		h := newHarness(t)
		h.cl.waitErr = errors.New("timed out waiting")
		if err := h.run(t); err != nil {
			t.Fatalf("a slow rollout is not a failure: %v", err)
		}
		if len(h.cl.undone) != 0 || !strings.Contains(h.out.String(), "Still rolling out: api, web") {
			t.Errorf("undone %v\n%s", h.cl.undone, h.out)
		}
	})
	t.Run("a rollout Kubernetes reports failed is undone", func(t *testing.T) {
		h := newHarness(t)
		h.cl.waitErr = errors.New("timed out waiting")
		h.cl.state["api"] = []DeploymentState{steady, {Found: true, DeadlineExceeded: true}}
		err := h.run(t)
		if err == nil || !strings.Contains(err.Error(), "failed and was undone") || strings.Join(h.cl.undone, ",") != "api" {
			t.Fatalf("err %v, undone %v", err, h.cl.undone)
		}
		if len(h.cl.waited) != 1 {
			t.Error("deploy went on after a failed rollout")
		}
	})
}

func TestSelection(t *testing.T) {
	h := newHarness(t)
	h.o.Workloads = []string{"nightly"}
	if err := h.run(t); err != nil {
		t.Fatalf("a manual workload ships when named: %v", err)
	}
	if !strings.Contains(h.appliedText(), "name: nightly") || len(h.cl.waited) != 0 {
		t.Error("nightly not applied, or a cronjob was waited on")
	}
	for names, want := range map[string]string{
		"old":     "disabled",
		"missing": `no workload "missing"`,
		"jobs":    "shared component",
	} {
		h := newHarness(t)
		h.o.Workloads = []string{names}
		if err := h.run(t); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", names, err, want)
		}
	}
}

func TestDryRun(t *testing.T) {
	h := newHarness(t)
	h.o.DryRun = true
	h.o.Repo.Images[1].BuildArgs = &config.BuildArgs{EnvFile: ".env.production", Prefix: "VITE_"}
	err := h.run(t)
	if err == nil || !strings.Contains(err.Error(), "1 problem(s)") || !strings.Contains(err.Error(), ".env.production does not exist") {
		t.Fatalf("got %v", err)
	}
	if len(h.b.built)+len(h.cl.applied)+len(h.cl.waited) != 0 || h.reg.loggedIn {
		t.Error("a dry run built, applied or waited")
	}
	for _, want := range []string{"image api: build reg.example.com/shop-api:1234567", "workload api: changes", "Dry run: nothing"} {
		if !strings.Contains(h.out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, h.out)
		}
	}
}

func TestBuildArgsEnvFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("VITE_A=\"1\"\nVITE_B='two'\nOTHER=x\n# c\nVITE_C = 3 \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	img := config.Image{Name: "web", BuildArgs: &config.BuildArgs{EnvFile: ".env", Prefix: "VITE_", Values: map[string]string{"VITE_C": "override"}}}
	args, err := buildArgs(context.Background(), img, dir, "abc", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"GIT_SHA": "abc", "VITE_A": "1", "VITE_B": "two", "VITE_C": "override"}
	if len(args) != len(want) {
		t.Fatalf("got %v", args)
	}
	for k, v := range want {
		if args[k] != v {
			t.Errorf("%s = %q, want %q", k, args[k], v)
		}
	}
	img.BuildArgs = &config.BuildArgs{EnvFile: ".env", Prefix: "NONE_"}
	if _, err := buildArgs(context.Background(), img, dir, "abc", nil); err == nil {
		t.Error("an env file with no matching values was accepted")
	}
	img.BuildArgs = &config.BuildArgs{EnvFile: "../outside.env"}
	if _, err := buildArgs(context.Background(), img, dir, "abc", nil); err == nil {
		t.Error("an env file outside the repo was read")
	}
}

func TestBuildSpecStaysInWorkspace(t *testing.T) {
	ws := fakeWS{root: "/w"}
	img := &image{cfg: config.Image{Name: "x", Context: "../.."}, repo: "x", tag: "t"}
	if _, err := buildSpec(ws, img, "h", ""); err == nil {
		t.Error("a context above the workspace was accepted")
	}
	img.cfg.Context = ".."
	s, err := buildSpec(ws, img, "h", "")
	if err != nil || s.Context != "/w" || s.Dockerfile != "/w/shop/Dockerfile" {
		t.Errorf("%+v %v", s, err)
	}
}
