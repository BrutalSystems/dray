package buildkit

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BrutalSystems/dray/internal/deploy"
)

func TestArgs(t *testing.T) {
	got := strings.Join(Args("tcp://bk:1234", deploy.BuildSpec{
		Context: "/w", Dockerfile: "/w/app/Dockerfile.prod", Ref: "reg/app:abc", Platform: "linux/arm64",
		Args: map[string]string{"B": "2", "A": "1"},
	}, "/tmp/m.json", ""), " ")
	want := "--addr tcp://bk:1234 build --frontend dockerfile.v0 --local context=/w --local dockerfile=/w/app " +
		"--opt filename=Dockerfile.prod --output type=image,name=reg/app:abc,push=true --metadata-file /tmp/m.json " +
		"--opt platform=linux/arm64 --opt build-arg:A=1 --opt build-arg:B=2"
	if got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
	cached := strings.Join(Args("a", deploy.BuildSpec{Context: "/w", Dockerfile: "/w/Dockerfile", Ref: "r"}, "m", "reg/cache:app"), " ")
	if !strings.Contains(cached, "--import-cache type=registry,ref=reg/cache:app --export-cache type=registry,ref=reg/cache:app,mode=max,image-manifest=true,oci-mediatypes=true") {
		t.Errorf("cache flags: %s", cached)
	}
}

// TestBuildAgainstARealDaemon builds and pushes a tiny image with a real
// BuildKit daemon and registry when DRAY_TEST_BUILDKIT_ADDR and
// DRAY_TEST_REGISTRY are set (the registry as the daemon reaches it) and
// DRAY_TEST_REGISTRY_LOCAL (as this test reaches it).
func TestBuildAgainstARealDaemon(t *testing.T) {
	addr, reg, local := os.Getenv("DRAY_TEST_BUILDKIT_ADDR"), os.Getenv("DRAY_TEST_REGISTRY"), os.Getenv("DRAY_TEST_REGISTRY_LOCAL")
	if addr == "" || reg == "" || local == "" {
		t.Skip("set DRAY_TEST_BUILDKIT_ADDR, DRAY_TEST_REGISTRY and DRAY_TEST_REGISTRY_LOCAL")
	}
	if _, err := exec.LookPath("buildctl"); err != nil {
		t.Skip("no buildctl")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o750); err != nil {
		t.Fatal(err)
	}
	df := "FROM scratch\nARG GIT_SHA\nCOPY hello.txt /hello.txt\nLABEL sha=$GIT_SHA\n"
	if err := os.WriteFile(filepath.Join(dir, "app", "Dockerfile"), []byte(df), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := &Builder{Addr: addr, Progress: io.Discard}
	digest, err := b.Build(context.Background(), deploy.BuildSpec{
		Context: dir, Dockerfile: filepath.Join(dir, "app", "Dockerfile"), Ref: reg + "/dray-test:abc1234",
		Platform: "linux/arm64", Args: map[string]string{"GIT_SHA": "abc1234"},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodHead, "http://"+local+"/v2/dray-test/manifests/abc1234", nil) //nolint:noctx,gosec // test against the local registry the developer named
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.index.v1+json")
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // same
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if got := resp.Header.Get("Docker-Content-Digest"); got != digest {
		t.Errorf("registry has %s, build reported %s", got, digest)
	}
}
