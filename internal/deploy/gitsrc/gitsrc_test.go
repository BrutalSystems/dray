package gitsrc

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BrutalSystems/dray/internal/config"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...) //nolint:gosec // test helper, fixed program
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newRepo(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	git(t, filepath.Dir(dir), "init", "-q", dir)
	for p, c := range files {
		write(t, filepath.Join(dir, p), c)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "one")
	return git(t, dir, "rev-parse", "HEAD")
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test
	if err != nil {
		return "<missing>"
	}
	return string(b)
}

func TestWorkspaceExportsCommitsNotTheWorkingTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	parent := t.TempDir()
	app := filepath.Join(parent, "checkout-name")
	newRepo(t, app, map[string]string{"Dockerfile": "FROM x\n", ".gitignore": "local.env\n"})
	lib := filepath.Join(parent, "lib")
	pinned := newRepo(t, lib, map[string]string{"lib.txt": "v1\n"})
	write(t, filepath.Join(lib, "lib.txt"), "v2\n")
	git(t, lib, "commit", "-q", "-am", "two")

	write(t, filepath.Join(app, "local.env"), "IGNORED=1\n")
	r := &Repo{Dir: app, Folder: "shop", Sources: []config.BuildSource{{Repo: "o/lib", Path: "lib", Commit: pinned}}}
	ctx := context.Background()
	short, dirty, err := r.Commit(ctx)
	if err != nil || dirty || len(short) < 7 {
		t.Fatalf("commit %q dirty %v err %v (an ignored file is not a change)", short, dirty, err)
	}
	write(t, filepath.Join(app, "Dockerfile"), "FROM changed\n")
	if _, dirty, _ := r.Commit(ctx); !dirty {
		t.Fatal("a modified file was not seen as dirty")
	}

	ws, err := r.Workspace(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if ws.Repo() != filepath.Join(ws.Root(), "shop") {
		t.Errorf("repo folder %s", ws.Repo())
	}
	if got := read(t, filepath.Join(ws.Repo(), "Dockerfile")); got != "FROM x\n" {
		t.Errorf("Dockerfile = %q: the workspace must hold the commit, not the working tree", got)
	}
	if got := read(t, filepath.Join(ws.Repo(), "local.env")); got != "<missing>" {
		t.Error("an ignored file reached the workspace")
	}
	if got := read(t, filepath.Join(ws.Root(), "lib", "lib.txt")); got != "v1\n" {
		t.Errorf("source = %q, want the pinned commit's v1", got)
	}
	root := ws.Root()
	if err := ws.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Error("the workspace was not removed")
	}

	dws, err := r.Workspace(ctx, true)
	if err == nil {
		t.Errorf("a dirty build with sources from a folder not named shop was accepted: %s", dws.Repo())
	}
}

func TestExtractRefusesEscapes(t *testing.T) {
	for _, name := range []string{"../evil", "/abs", "a/../../evil"} {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		_ = tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Size: 1, Mode: 0o644})
		_, _ = tw.Write([]byte("x"))
		_ = tw.Close()
		root, err := os.OpenRoot(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err := Extract(root, &buf); err == nil {
			t.Errorf("%s was extracted", name)
		}
		_ = root.Close()
	}
	// A symlink may be created, but nothing is written through it.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/tmp"})
	_ = tw.WriteHeader(&tar.Header{Name: "link/evil", Typeflag: tar.TypeReg, Size: 1, Mode: 0o644})
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()
	root, _ := os.OpenRoot(t.TempDir())
	defer func() { _ = root.Close() }()
	if err := Extract(root, &buf); err == nil {
		t.Error("a file was written through a symlink")
	}
}
