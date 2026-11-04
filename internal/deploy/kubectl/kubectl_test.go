package kubectl

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/BrutalSystems/dray/internal/execx"
)

func fake(code int, stdout, stderr string, seen *[]execx.Cmd) execx.Runner {
	return func(_ context.Context, c execx.Cmd) ([]byte, error) {
		*seen = append(*seen, c)
		if c.Stdin != nil {
			_, _ = io.ReadAll(c.Stdin)
		}
		if code != 0 {
			return []byte(stdout), &execx.Error{Cmd: c.String(), Code: code, Stderr: stderr}
		}
		return []byte(stdout), nil
	}
}

func TestDiffExitCodes(t *testing.T) {
	var seen []execx.Cmd
	diff := "diff -u -N /tmp/LIVE-1/apps.v1.Deployment.shop.api /tmp/MERGED-2/apps.v1.Deployment.shop.api\n--- a\n+++ b\n-  image: old\n+  image: new\n"
	c := &Cluster{Context: "ctx", Namespace: "shop", Run: fake(1, diff, "", &seen)}
	changed, out, err := c.Diff(context.Background(), []byte("doc"))
	if err != nil || !changed || out != "── Deployment/api\n-  image: old\n+  image: new" {
		t.Fatalf("changed %v err %v out %q", changed, err, out)
	}
	got := strings.Join(seen[0].Args, " ")
	if got != "diff --server-side --field-manager=dray --force-conflicts -f - -n shop --context ctx" {
		t.Errorf("args %s", got)
	}
	c.Run = fake(0, "", "", &seen)
	if changed, _, err := c.Diff(context.Background(), nil); changed || err != nil {
		t.Errorf("no diff: %v %v", changed, err)
	}
	c.Run = fake(2, "", "error: forbidden", &seen)
	if _, _, err := c.Diff(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Errorf("a kubectl error must not read as no change: %v", err)
	}
}

func TestDeploymentState(t *testing.T) {
	var seen []execx.Cmd
	c := &Cluster{Context: "ctx", Namespace: "shop", Run: fake(1, "", `Error from server (NotFound): deployments.apps "api" not found`, &seen)}
	st, err := c.Deployment(context.Background(), "api")
	if err != nil || st.Found {
		t.Fatalf("missing deployment: %+v %v", st, err)
	}
	c.Run = fake(1, "", "Unauthorized", &seen)
	if _, err := c.Deployment(context.Background(), "api"); err == nil {
		t.Error("an unreadable deployment must be an error")
	}
	st, err = parseDeployment([]byte(`{"metadata":{"generation":5},"spec":{"replicas":3},
		"status":{"observedGeneration":5,"updatedReplicas":3,"availableReplicas":2,
		"conditions":[{"type":"Progressing","reason":"ProgressDeadlineExceeded"}]}}`))
	if err != nil || !st.Found || st.Replicas != 3 || !st.Rolling() || !st.DeadlineExceeded {
		t.Errorf("%+v %v", st, err)
	}
	st, _ = parseDeployment([]byte(`{"metadata":{"generation":5},"spec":{},"status":{"observedGeneration":5,"updatedReplicas":1,"availableReplicas":1}}`))
	if st.Replicas != 1 || st.Rolling() {
		t.Errorf("default replicas: %+v", st)
	}
}

func TestObjectName(t *testing.T) {
	for in, want := range map[string]string{
		"/tmp/LIVE-1/apps.v1.Deployment.shop.api":      "Deployment/api",
		"/tmp/LIVE-1/v1.ConfigMap.shop.app.config":     "ConfigMap/app.config",
		"/tmp/LIVE-1/networking.k8s.io.v1.Ingress.a.b": "Ingress/b",
		"weird": "weird",
	} {
		if got := objectName(in); got != want {
			t.Errorf("objectName(%q) = %q, want %q", in, got, want)
		}
	}
}
