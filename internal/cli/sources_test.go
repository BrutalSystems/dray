package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCopiedFolders(t *testing.T) {
	df := `FROM mcr.microsoft.com/dotnet/sdk:10.0 AS builder
WORKDIR /src
COPY lib/Lib/Lib.csproj lib/Lib/
COPY app/App.sln app/
copy ./app/App/ app/App/
COPY ["lib/Shared/", "lib/Shared/"]
COPY --from=builder /src/out /app
COPY --chown=app:app app/run.sh /run.sh
ADD *.txt /tmp/
RUN dotnet publish
`
	got := copiedFolders([]byte(df))
	want := []string{"lib", "app", "app", "lib", "app"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("copiedFolders = %v, want %v", got, want)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...) //nolint:gosec // test helper, fixed binary
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestMigrateFindsSiblingRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	parent := t.TempDir()
	lib := filepath.Join(parent, "lib")
	app := filepath.Join(parent, "app")
	for _, d := range []string{lib, filepath.Join(app, ".dray")} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	git(t, lib, "init", "-q")
	git(t, lib, "remote", "add", "origin", "git@github.com:example/lib.git")
	git(t, lib, "commit", "-q", "--allow-empty", "-m", "init")
	commit := git(t, lib, "rev-parse", "HEAD")

	files := map[string]string{
		filepath.Join(app, "Dockerfile"): "FROM scratch\nCOPY lib/Lib.csproj lib/\nCOPY app/ app/\n",
		filepath.Join(app, ".dray", "config.json"): `{"name":"app","defaults":{"namespace":"team","context":"main","account":"123456789012","region":"us-east-2"},
		  "images":[{"name":"app","ecr":"app","source":{"local":true},"dockerfile":"Dockerfile","context":".."}],
		  "workloads":[{"name":"app","kind":"deployment","image":"app","manifests":["k8s/app.yaml"]}]}`,
	}
	for p, c := range files {
		if err := os.WriteFile(p, []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runConfig(t, "config", "migrate", app, "--write", "--machine-config", filepath.Join(parent, "none.json")); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(filepath.Join(app, ".dray", "config.yaml")) //nolint:gosec // test file
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"repo: example/lib", "path: lib", "commit: " + commit} {
		if !strings.Contains(string(written), want) {
			t.Errorf("missing %q in:\n%s", want, written)
		}
	}
}
