package cli

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/BrutalSystems/dray/internal/config"
	"github.com/BrutalSystems/dray/internal/config/legacy"
)

// siblingSources finds the other repos an image builds with when its context
// is the folder above the repo: the folders its Dockerfile copies from that
// sit beside the repo as git checkouts with a GitHub remote. Each is pinned to
// the commit checked out locally; migrate warns so a person confirms it.
func siblingSources(repoDir string) func(legacy.Image) []config.BuildSource {
	return func(img legacy.Image) []config.BuildSource {
		if path.Clean(img.Context) != ".." {
			return nil
		}
		dockerfile := img.Dockerfile
		if dockerfile == "" {
			dockerfile = "Dockerfile"
		}
		data, err := os.ReadFile(filepath.Join(repoDir, dockerfile)) //nolint:gosec // a file inside the repo the user named
		if err != nil {
			return nil
		}
		self, err := filepath.Abs(repoDir)
		if err != nil {
			return nil
		}
		var out []config.BuildSource
		seen := map[string]bool{}
		for _, seg := range copiedFolders(data) {
			if seen[seg] {
				continue
			}
			seen[seg] = true
			dir := filepath.Join(filepath.Dir(self), seg)
			if dir == self || !isRepoRoot(dir) {
				continue
			}
			repo := gitHubRepo(dir)
			commit := gitOutput(dir, "rev-parse", "HEAD")
			if repo == "" || commit == "" {
				continue
			}
			out = append(out, config.BuildSource{Repo: repo, Path: seg, Commit: commit})
		}
		return out
	}
}

// copiedFolders returns the first folder of every source path in the
// Dockerfile's COPY and ADD lines, skipping copies from other build stages.
func copiedFolders(dockerfile []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(dockerfile))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		if op := strings.ToUpper(fields[0]); op != "COPY" && op != "ADD" {
			continue
		}
		args := fields[1:]
		fromStage := false
		var paths []string
		for _, a := range args {
			if strings.HasPrefix(a, "--from") {
				fromStage = true
			}
			if strings.HasPrefix(a, "--") {
				continue
			}
			a = strings.Trim(a, `[]",`)
			if a != "" {
				paths = append(paths, a)
			}
		}
		if fromStage || len(paths) < 2 {
			continue
		}
		for _, src := range paths[:len(paths)-1] {
			src = strings.TrimPrefix(src, "./")
			first, _, _ := strings.Cut(src, "/")
			if first != "" && first != "." && first != ".." && !strings.ContainsAny(first, "*?$") {
				out = append(out, first)
			}
		}
	}
	return out
}

func isRepoRoot(dir string) bool {
	top := gitOutput(dir, "rev-parse", "--show-toplevel")
	if top == "" {
		return false
	}
	a, err1 := filepath.EvalSymlinks(top)
	b, err2 := filepath.EvalSymlinks(dir)
	return err1 == nil && err2 == nil && a == b
}

func gitOutput(dir string, args ...string) string {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output() //nolint:gosec // fixed git subcommands, no shell
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
