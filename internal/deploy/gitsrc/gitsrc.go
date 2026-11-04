// Package gitsrc is the deploy.Source adapter for a local git checkout.
//
// A clean build never uses the working tree: the commit is exported into a
// fresh folder named after the repository, and each build source (D18) is
// exported next to it at its pinned commit, so a context of ".." holds exactly
// the repo and its sources. A source is taken from a local checkout beside the
// repo when that checkout has the commit, otherwise fetched from GitHub.
package gitsrc

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/BrutalSystems/dray/internal/config"
	"github.com/BrutalSystems/dray/internal/deploy"
	"github.com/BrutalSystems/dray/internal/execx"
)

// Repo is a local checkout.
type Repo struct {
	// Dir is the checkout.
	Dir string
	// Folder is the repo's folder name in a workspace: its repository's name.
	Folder string
	// Sources are the other repos the builds need beside it.
	Sources []config.BuildSource
	// Run runs git; nil is execx.Run.
	Run execx.Runner
	// Log receives notes, such as a source fetched from GitHub; nil discards.
	Log io.Writer

	head string
}

var _ deploy.Source = (*Repo)(nil)

func (r *Repo) git(ctx context.Context, dir string, args ...string) (string, error) {
	run := r.Run
	if run == nil {
		run = execx.Run
	}
	out, err := run(ctx, execx.Cmd{Name: "git", Args: append([]string{"-C", dir}, args...)})
	return strings.TrimSpace(string(out)), err
}

func (r *Repo) logf(format string, a ...any) {
	if r.Log != nil {
		_, _ = fmt.Fprintf(r.Log, format+"\n", a...)
	}
}

// Commit is HEAD in short form (as the JS tool tagged images) and whether
// the app's folder has uncommitted changes, untracked files included. Only
// Dir counts: for an app in a subfolder of a monorepo, changes elsewhere in
// the repo are not part of what gets built.
func (r *Repo) Commit(ctx context.Context) (string, bool, error) {
	head, err := r.git(ctx, r.Dir, "rev-parse", "HEAD")
	if err != nil {
		return "", false, err
	}
	short, err := r.git(ctx, r.Dir, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", false, err
	}
	status, err := r.git(ctx, r.Dir, "status", "--porcelain", "--", ".")
	if err != nil {
		return "", false, err
	}
	r.head = head
	return short, status != "", nil
}

type workspace struct {
	root, repo string
	cleanup    func() error
}

func (w *workspace) Root() string { return w.root }
func (w *workspace) Repo() string { return w.repo }
func (w *workspace) Close() error {
	if w.cleanup == nil {
		return nil
	}
	return w.cleanup()
}

// Workspace lays out the build. With dirty set it is the working tree and the
// checkouts beside it, as they are.
func (r *Repo) Workspace(ctx context.Context, dirty bool) (deploy.Workspace, error) {
	if dirty {
		return r.dirtyWorkspace(ctx)
	}
	if r.head == "" {
		if _, _, err := r.Commit(ctx); err != nil {
			return nil, err
		}
	}
	tmp, err := os.MkdirTemp("", "dray-ws-")
	if err != nil {
		return nil, err
	}
	ws := &workspace{root: tmp, repo: filepath.Join(tmp, r.Folder), cleanup: func() error { return os.RemoveAll(tmp) }}
	fail := func(err error) (deploy.Workspace, error) {
		_ = ws.Close()
		return nil, err
	}
	if err := r.export(ctx, r.Dir, r.head, tmp, r.Folder); err != nil {
		return fail(err)
	}
	for _, s := range r.Sources {
		if s.Path == r.Folder {
			return fail(fmt.Errorf("source %s: path %s is the repo's own folder", s.Repo, s.Path))
		}
		if err := r.source(ctx, tmp, s); err != nil {
			return fail(fmt.Errorf("source %s at %s: %w", s.Repo, s.Commit, err))
		}
	}
	return ws, nil
}

func (r *Repo) dirtyWorkspace(ctx context.Context) (deploy.Workspace, error) {
	dir, err := filepath.Abs(r.Dir)
	if err != nil {
		return nil, err
	}
	if filepath.Base(dir) != r.Folder && len(r.Sources) > 0 {
		return nil, fmt.Errorf("building uncommitted changes with sources needs the checkout in a folder named %s", r.Folder)
	}
	root := filepath.Dir(dir)
	for _, s := range r.Sources {
		local := filepath.Join(root, s.Path)
		head, err := r.git(ctx, local, "rev-parse", "HEAD")
		if err != nil {
			return nil, fmt.Errorf("source %s: no checkout at %s", s.Repo, local)
		}
		if !strings.HasPrefix(head, s.Commit) {
			r.logf("! source %s: building with %s as checked out (%.7s), not the pinned %.7s", s.Repo, local, head, s.Commit)
		}
	}
	return &workspace{root: root, repo: dir}, nil
}

// source exports a build source at its pinned commit, from the checkout
// beside the repo when it has that commit, otherwise from GitHub.
func (r *Repo) source(ctx context.Context, root string, s config.BuildSource) error {
	local := filepath.Join(filepath.Dir(r.Dir), s.Path)
	if _, err := r.git(ctx, local, "cat-file", "-e", s.Commit+"^{commit}"); err == nil {
		return r.export(ctx, local, s.Commit, root, s.Path)
	}
	clone, err := os.MkdirTemp("", "dray-src-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(clone) }()
	r.logf("fetching %s at %.7s from GitHub", s.Repo, s.Commit)
	for _, args := range [][]string{
		{"init", "-q"},
		{"fetch", "-q", "--depth", "1", "https://github.com/" + s.Repo + ".git", s.Commit},
	} {
		if _, err := r.git(ctx, clone, args...); err != nil {
			return err
		}
	}
	return r.export(ctx, clone, "FETCH_HEAD", root, s.Path)
}

// export writes the tree of commit in repo into root/folder.
func (r *Repo) export(ctx context.Context, repo, commit, root, folder string) error {
	f, err := os.CreateTemp("", "dray-archive-*.tar")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	defer func() { _ = os.Remove(name) }()
	if _, err := r.git(ctx, repo, "archive", "--format=tar", "-o", name, commit); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, folder), 0o750); err != nil {
		return err
	}
	dst, err := os.OpenRoot(filepath.Join(root, folder))
	if err != nil {
		return err
	}
	defer func() { _ = dst.Close() }()
	src, err := os.Open(name) //nolint:gosec // our own temp file
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	return Extract(dst, src)
}

// Extract unpacks a tar stream into root. Writes go through the os.Root, so
// no entry or symlink can place a file outside it.
func Extract(root *os.Root, r io.Reader) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := path.Clean(h.Name)
		if name == "." {
			continue
		}
		if !fs.ValidPath(name) {
			return fmt.Errorf("archive entry %q leaves the folder", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, 0o750); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := root.MkdirAll(path.Dir(name), 0o750); err != nil {
				return err
			}
			mode := os.FileMode(0o640)
			if h.Mode&0o111 != 0 {
				mode = 0o750
			}
			out, err := root.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, tr) //nolint:gosec // a git archive of a repo we build anyway
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := root.MkdirAll(path.Dir(name), 0o750); err != nil {
				return err
			}
			if err := root.Symlink(h.Linkname, name); err != nil {
				return err
			}
		case tar.TypeXGlobalHeader:
			// git archive's pax header (the commit id); not a file.
		default:
			return fmt.Errorf("archive entry %q has unsupported type %c", h.Name, h.Typeflag)
		}
	}
}
