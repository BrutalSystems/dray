package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BrutalSystems/dray/internal/config"
	"github.com/BrutalSystems/dray/internal/deploy"
	"github.com/BrutalSystems/dray/internal/githubapp"
)

// fakeAPI is one installation's GitHub.
type fakeAPI struct {
	mu      sync.Mutex
	runs    []githubapp.Run
	tarball map[string][]byte // repo@ref -> tgz
	checks  map[int64]*githubapp.Check
	order   []int64
}

func (f *fakeAPI) RunsForCommit(_ context.Context, _, sha string) ([]githubapp.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]githubapp.Run, len(f.runs))
	for i, r := range f.runs {
		r.HeadSHA = sha
		out[i] = r
	}
	return out, nil
}

func (f *fakeAPI) Tarball(_ context.Context, repo, ref string, w io.Writer) error {
	b, ok := f.tarball[repo+"@"+ref]
	if !ok {
		return errors.New("404 Not Found")
	}
	_, err := w.Write(b)
	return err
}

func (f *fakeAPI) CreateCheck(_ context.Context, _, _ string, k githubapp.Check) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.checks == nil {
		f.checks = map[int64]*githubapp.Check{}
	}
	id := int64(len(f.checks) + 1)
	f.checks[id] = &k
	f.order = append(f.order, id)
	return id, nil
}

func (f *fakeAPI) UpdateCheck(_ context.Context, _ string, id int64, k githubapp.Check) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.checks[id]
	c.Status, c.Conclusion, c.Title, c.Summary, c.Text = k.Status, k.Conclusion, k.Title, k.Summary, k.Text
	return nil
}

func (f *fakeAPI) check(t *testing.T, name string) githubapp.Check {
	t.Helper()
	for _, id := range f.order {
		if c := f.checks[id]; c.Name == name {
			return *c
		}
	}
	t.Fatalf("no check %q", name)
	return githubapp.Check{}
}

type oneInstallation struct{ api *fakeAPI }

func (o oneInstallation) Installation(int64) RepoAPI { return o.api }

func sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func event(branch, sha, ev string) []byte {
	return []byte(fmt.Sprintf(`{"action":"completed","workflow_run":{"head_branch":%q,"head_sha":%q,"event":%q},
		"repository":{"full_name":"o/shop","default_branch":"main"},"installation":{"id":7}}`, branch, sha, ev))
}

func post(t *testing.T, h http.Handler, kind string, body []byte, sig string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", kind)
	req.Header.Set("X-Hub-Signature-256", sig)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestWebhookTriggersOnlyGreenPushesToMain(t *testing.T) {
	secret := []byte("whsec")
	api := &fakeAPI{runs: []githubapp.Run{{Name: "ci", Status: "completed", Conclusion: "success"}}}
	var mu sync.Mutex
	var deployed []string
	s := &Server{WebhookSecret: secret, GitHub: oneInstallation{api}, Deploy: func(_ context.Context, _ RepoAPI, tr Trigger) {
		mu.Lock()
		deployed = append(deployed, tr.SHA)
		mu.Unlock()
	}}
	h := s.Handler()
	send := func(kind string, body []byte) int { return post(t, h, kind, body, sign(secret, body)) }

	if code := post(t, h, "workflow_run", event("main", "a1", "push"), "sha256=00"); code != http.StatusUnauthorized {
		t.Errorf("bad signature: %d", code)
	}
	if code := send("ping", []byte(`{}`)); code != http.StatusOK {
		t.Errorf("ping: %d", code)
	}
	for _, body := range [][]byte{event("feature", "b1", "push"), event("main", "b2", "pull_request")} {
		if code := send("workflow_run", body); code != http.StatusNoContent {
			t.Errorf("ignored event: %d", code)
		}
	}
	if code := send("workflow_run", event("main", "a1", "push")); code != http.StatusAccepted {
		t.Errorf("push to main: %d", code)
	}
	s.Wait()
	send("workflow_run", event("main", "a1", "push")) // the commit's second workflow finishing
	s.Wait()
	if strings.Join(deployed, ",") != "a1" {
		t.Errorf("deployed %v, want a1 once", deployed)
	}

	api.runs = []githubapp.Run{{Name: "ci", Status: "completed", Conclusion: "success"}, {Name: "e2e", Status: "in_progress"}}
	send("workflow_run", event("main", "c1", "push"))
	s.Wait()
	api.runs[1].Status, api.runs[1].Conclusion = "completed", "failure"
	send("workflow_run", event("main", "c1", "push"))
	s.Wait()
	if strings.Join(deployed, ",") != "a1" {
		t.Errorf("a pending or failed commit deployed: %v", deployed)
	}
}

func TestQueueDeploysTheLatest(t *testing.T) {
	api := &fakeAPI{runs: []githubapp.Run{{Status: "completed", Conclusion: "success"}}}
	release := make(chan struct{})
	var mu sync.Mutex
	var deployed []string
	s := &Server{GitHub: oneInstallation{api}, Deploy: func(_ context.Context, _ RepoAPI, tr Trigger) {
		if tr.SHA == "first" {
			<-release
		}
		mu.Lock()
		deployed = append(deployed, tr.SHA)
		mu.Unlock()
	}}
	s.Enqueue(Trigger{Repo: "o/shop", SHA: "first", Installation: 7})
	time.Sleep(20 * time.Millisecond)
	s.Enqueue(Trigger{Repo: "o/shop", SHA: "second", Installation: 7})
	s.Enqueue(Trigger{Repo: "o/shop", SHA: "third", Installation: 7})
	close(release)
	s.Wait()
	if strings.Join(deployed, ",") != "first,third" {
		t.Errorf("deployed %v, want first,third", deployed)
	}
}

// tgz builds a GitHub-style tarball: everything under owner-repo-sha/.
func tgz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	_ = tw.WriteHeader(&tar.Header{Name: "pax_global_header", Typeflag: tar.TypeXGlobalHeader})
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: "o-shop-abc1234/" + name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type reg struct{}

func (reg) Host() string                                           { return "reg.example.com" }
func (reg) Login(context.Context) error                            { return nil }
func (reg) Ensure(context.Context, string) error                   { return nil }
func (reg) Digest(context.Context, string, string) (string, error) { return digest, nil }

type cluster struct {
	mu      sync.Mutex
	applied []string
}

func (c *cluster) ReadNamespace(context.Context) (deploy.NamespaceState, error) {
	return deploy.NamespaceState{Exists: true, Owner: "shop"}, nil
}
func (c *cluster) Claim(context.Context, string) error                { return nil }
func (c *cluster) Diff(context.Context, []byte) (bool, string, error) { return true, "", nil }
func (c *cluster) Apply(_ context.Context, d []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.applied = append(c.applied, string(d))
	return nil
}
func (c *cluster) Deployment(context.Context, string) (deploy.DeploymentState, error) {
	return deploy.DeploymentState{}, nil
}
func (c *cluster) Restart(context.Context, string) error             { return nil }
func (c *cluster) Wait(context.Context, string, time.Duration) error { return nil }
func (c *cluster) Undo(context.Context, string) error                { return nil }

const shopConfig = `name: shop
images:
  - name: api
workloads:
  - name: api
    kind: deployment
    images: [api]
    manifests: [k8s/api.yaml]
targets:
  t: {cloud: aws, account: "123456789012", region: us-east-2, cluster: c}
environments:
  dev: {target: t, namespace: shop-dev, release: direct, approval: none, on_merge: deploy}
  prod: {target: t, namespace: shop, release: direct, approval: passkey, on_merge: deploy}
  qa: {target: t, namespace: shop-qa, release: direct, approval: none}
`

func TestDeployerDeploysEachOnMergeEnvironment(t *testing.T) {
	api := &fakeAPI{
		runs: []githubapp.Run{{Name: "ci", Status: "completed", Conclusion: "success"}},
		tarball: map[string][]byte{"o/shop@abc1234def": tgz(t, map[string]string{
			".dray/config.yaml":                shopConfig,
			"k8s/api.yaml":                     "kind: Deployment\nmetadata: {name: api}\nimage: ${API_IMAGE}\n",
			"web/.dray/config.yaml":            "name: bad\nunknown: key\n",
			"node_modules/x/.dray/config.yaml": "never read",
		})},
	}
	cl := &cluster{}
	var envs []string
	d := &Deployer{Adapters: func(c *config.RepoConfig, env string, _ config.Environment, tg config.Target, _ io.Writer) (Adapters, error) {
		envs = append(envs, c.Name+"/"+env+"@"+tg.Cluster)
		return Adapters{Registry: reg{}, Cluster: cl}, nil
	}}
	if err := d.deploy(context.Background(), api, Trigger{Repo: "o/shop", SHA: "abc1234def", Installation: 7}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(envs, ",") != "shop/dev@c" {
		t.Errorf("adapters made for %v; only dev deploys on merge without approval", envs)
	}
	if k := api.check(t, "dray: shop → dev"); k.Conclusion != "success" || !strings.Contains(k.Text, "applied api") {
		t.Errorf("dev check %+v", k)
	}
	if k := api.check(t, "dray: shop → prod"); k.Conclusion != "neutral" || !strings.Contains(k.Summary, "approval passkey") {
		t.Errorf("prod check %+v", k)
	}
	if k := api.check(t, "dray: web"); k.Conclusion != "failure" || !strings.Contains(k.Summary, "unknown") {
		t.Errorf("broken config check %+v", k)
	}
	if len(cl.applied) != 1 || !strings.Contains(cl.applied[0], "image: reg.example.com/api:abc1234@"+digest) {
		t.Errorf("applied %v", cl.applied)
	}
}

func TestDeployerReportsADownloadFailure(t *testing.T) {
	api := &fakeAPI{}
	d := &Deployer{Adapters: func(*config.RepoConfig, string, config.Environment, config.Target, io.Writer) (Adapters, error) {
		t.Fatal("no adapters without a commit")
		return Adapters{}, nil
	}}
	if err := d.deploy(context.Background(), api, Trigger{Repo: "o/shop", SHA: "missing"}); err == nil {
		t.Fatal("no error")
	}
	if k := api.check(t, "dray"); k.Conclusion != "failure" || !strings.Contains(k.Summary, "404") {
		t.Errorf("%+v", k)
	}
}
