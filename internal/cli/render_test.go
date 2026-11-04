package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const renderDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func runRender(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := NewRoot("dray", "test")
	root.AddCommand(NewRenderCmd())
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(append([]string{"render"}, args...))
	err := root.Execute()
	return out.String(), errb.String(), err
}

func renderRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		".dray/config.yaml": `name: shop
images:
  - name: api
workloads:
  - name: api
    kind: deployment
    images: [api]
    manifests: [k8s/api.yaml]
  - name: old
    kind: cronjob
    manifests: [k8s/old.yaml]
    disabled: true
`,
		"k8s/api.yaml": "apiVersion: apps/v1\nkind: Deployment\nimage: ${API_IMAGE}\n",
		"k8s/old.yaml": "kind: CronJob\n",
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
	return dir
}

func TestRenderCommand(t *testing.T) {
	dir := renderRepo(t)
	out, stderr, err := runRender(t, dir, "--image", "api=r.example/api@"+renderDigest)
	if err != nil {
		t.Fatal(err)
	}
	want := "# Source: k8s/api.yaml\napiVersion: apps/v1\nkind: Deployment\nimage: r.example/api@" + renderDigest + "\n"
	if out != want || stderr != "" {
		t.Fatalf("out:\n%s\nstderr:\n%s", out, stderr)
	}

	_, stderr, err = runRender(t, dir, "--image", "api=r.example/api:abc1234")
	if err != nil || !strings.Contains(stderr, "referenced by tag") {
		t.Errorf("want a tag warning, got %v / %q", err, stderr)
	}
}

func TestRenderCommandRefusals(t *testing.T) {
	dir := renderRepo(t)
	cases := map[string]struct {
		args []string
		want string
	}{
		"missing image":     {[]string{dir}, "no image reference for api"},
		"bad image flag":    {[]string{dir, "--image", "api"}, "want name=ref"},
		"disabled workload": {[]string{dir, "--workload", "old"}, "is disabled"},
		"unknown workload":  {[]string{dir, "--workload", "nope"}, "unknown workload"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := runRender(t, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error containing %q, got %v", tc.want, err)
			}
		})
	}
}
