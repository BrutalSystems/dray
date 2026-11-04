package render

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BrutalSystems/dray/internal/config"
)

const digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestImageVar(t *testing.T) {
	cases := map[[2]string]string{
		{"api", ""}:            "API_IMAGE",
		{"booking-api", ""}:    "BOOKING_API_IMAGE",
		{"web.v2--x", ""}:      "WEB_V2_X_IMAGE",
		{"api", "BACKEND_IMG"}: "BACKEND_IMG",
	}
	for in, want := range cases {
		if got := ImageVar(in[0], in[1]); got != want {
			t.Errorf("ImageVar(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestStringLeavesOtherPlaceholders(t *testing.T) {
	in := "image: ${API_IMAGE}\nargs: [\"echo ${HOME} $API_IMAGE ${API_IMAGE}\"]\n"
	got := string(String([]byte(in), map[string]string{"API_IMAGE": "r/api@" + digest}))
	want := "image: r/api@" + digest + "\nargs: [\"echo ${HOME} $API_IMAGE r/api@" + digest + "\"]\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func shop() *config.RepoConfig {
	return &config.RepoConfig{
		Name:   "shop",
		Images: []config.Image{{Name: "api"}, {Name: "web", TemplateVar: "FRONT"}},
		Uses:   []config.Use{{Component: "queue-service", Version: "1.0.0", As: "queue"}},
		Workloads: []config.Workload{
			{Name: "api", Kind: config.KindDeployment, Images: []string{"api", "queue"}, Manifests: []string{"k8s/api.yaml"}},
			{Name: "web", Kind: config.KindDeployment, Images: []string{"web"}, Manifests: []string{"k8s/web.yaml", "k8s/svc.yaml"}},
		},
	}
}

func TestVars(t *testing.T) {
	c := shop()
	vars, err := Vars(c, c.Workloads[0], map[string]string{"api": "r/api@" + digest, "queue": "r/q@" + digest})
	if err != nil {
		t.Fatal(err)
	}
	if vars["API_IMAGE"] == "" || vars["QUEUE_IMAGE"] == "" || len(vars) != 2 {
		t.Errorf("vars = %v", vars)
	}
	if _, err := Vars(c, c.Workloads[0], map[string]string{"api": "x"}); err == nil || !strings.Contains(err.Error(), "no image reference for queue") {
		t.Errorf("want a missing-reference error, got %v", err)
	}
}

func writeRepo(t *testing.T, files map[string]string) string {
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

func TestWorkload(t *testing.T) {
	dir := writeRepo(t, map[string]string{
		"k8s/web.yaml": "apiVersion: apps/v1\nkind: Deployment\nspec:\n  image: ${FRONT}\n  sidecar: ${OTHER_IMAGE}\n",
		"k8s/svc.yaml": "apiVersion: v1\nkind: Service\n---\napiVersion: v1\nkind: ConfigMap",
	})
	c := shop()
	res, err := Workload(dir, c, c.Workloads[1], map[string]string{"web": "r/web@" + digest})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(res.Manifests[0].Content, []byte("image: r/web@"+digest)) {
		t.Errorf("not stamped:\n%s", res.Manifests[0].Content)
	}
	if len(res.Unfilled) != 1 || res.Unfilled[0] != "k8s/web.yaml: ${OTHER_IMAGE}" {
		t.Errorf("Unfilled = %v", res.Unfilled)
	}
	var b bytes.Buffer
	if err := Write(&b, res.Manifests); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "---\n# Source: k8s/svc.yaml\napiVersion: v1\nkind: Service") || !strings.HasSuffix(b.String(), "kind: ConfigMap\n") {
		t.Errorf("stream:\n%s", b.String())
	}
}

func TestWorkloadRefusals(t *testing.T) {
	outside := writeRepo(t, map[string]string{"secret.yaml": "kind: Secret\n"})
	dir := writeRepo(t, map[string]string{"k8s/bad.yaml": "kind: [unclosed\n"})
	if err := os.Symlink(filepath.Join(outside, "secret.yaml"), filepath.Join(dir, "k8s", "link.yaml")); err != nil {
		t.Fatal(err)
	}
	c := &config.RepoConfig{Name: "shop"}
	cases := map[string]struct{ manifest, want string }{
		"symlink out of the repo": {"k8s/link.yaml", "escapes"},
		"path out of the repo":    {"../secret.yaml", "escapes"},
		"missing file":            {"k8s/none.yaml", "no such file"},
		"bad yaml":                {"k8s/bad.yaml", "document 1 does not parse"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			w := config.Workload{Name: "w", Kind: config.KindDeployment, Manifests: []string{tc.manifest}}
			_, err := Workload(dir, c, w, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestIsDigestRef(t *testing.T) {
	if !IsDigestRef("r.example/api@" + digest) {
		t.Error("digest ref not recognized")
	}
	for _, ref := range []string{"r/api:abc123", "r/api@sha256:abc", "r/api@sha256:" + strings.Repeat("G", 64), "r/api"} {
		if IsDigestRef(ref) {
			t.Errorf("%q is not a digest ref", ref)
		}
	}
}
