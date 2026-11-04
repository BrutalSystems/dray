package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BrutalSystems/dray/internal/config"
	"github.com/BrutalSystems/dray/internal/deploy"
	"github.com/BrutalSystems/dray/internal/deploy/gitsrc"
	"github.com/BrutalSystems/dray/internal/githubapp"
	"github.com/BrutalSystems/dray/internal/secrets"
)

// Adapters are the systems one deploy talks to, made for one app and
// environment: in the cluster, the builder, ECR, kubectl in-cluster, and
// the secret store with Dray's own identity.
type Adapters struct {
	Registry deploy.Registry
	Builder  deploy.Builder
	Cluster  deploy.Cluster
	// Secrets may be nil when the app uses none.
	Secrets secrets.Store
}

// AdapterFactory makes the adapters for an app's environment. out receives
// build and rollout progress, which ends up in the check run's log.
type AdapterFactory func(c *config.RepoConfig, env string, e config.Environment, t config.Target, out io.Writer) (Adapters, error)

// Deployer deploys a commit's apps after CI passed.
type Deployer struct {
	Adapters AdapterFactory
	// TempDir holds downloaded commits; empty is the system's.
	TempDir string
	// Note, when set, records each environment's outcome (Server.Note).
	Note func(Record)
}

// Deploy is a Server's Deploy function: download the commit, then deploy
// every on_merge: deploy environment of every app in it.
func (d *Deployer) Deploy(ctx context.Context, api RepoAPI, t Trigger) {
	_ = d.deploy(ctx, api, t)
}

func (d *Deployer) deploy(ctx context.Context, api RepoAPI, t Trigger) error {
	tmp, err := os.MkdirTemp(d.TempDir, "dray-commit-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	repoName := t.Repo[strings.LastIndex(t.Repo, "/")+1:]
	repoDir := filepath.Join(tmp, repoName)
	if err := download(ctx, api, t.Repo, t.SHA, repoDir); err != nil {
		return d.fail(ctx, api, t, "dray", "Could not download the commit", err)
	}
	apps, err := findApps(repoDir)
	if err != nil {
		return d.fail(ctx, api, t, "dray", "Could not read the commit", err)
	}
	runs, err := api.RunsForCommit(ctx, t.Repo, t.SHA)
	if err != nil {
		return err
	}
	for _, appDir := range apps {
		c, err := config.LoadRepoConfig(appDir)
		if err != nil {
			rel, _ := filepath.Rel(repoDir, appDir)
			_ = d.fail(ctx, api, t, "dray: "+rel, "The Dray config does not load", err)
			continue
		}
		for _, envName := range onMerge(c) {
			d.deployEnv(ctx, api, t, tmp, appDir, c, envName, runs)
		}
	}
	return nil
}

// fail reports an error on the commit as a failed check.
func (d *Deployer) fail(ctx context.Context, api RepoAPI, t Trigger, name, title string, err error) error {
	_, cerr := api.CreateCheck(ctx, t.Repo, t.SHA, githubapp.Check{
		Name: name, Status: "completed", Conclusion: "failure", Title: title, Summary: err.Error(),
	})
	return errors.Join(err, cerr)
}

func onMerge(c *config.RepoConfig) []string {
	var out []string
	for n, e := range c.Environments {
		if e.OnMerge == "deploy" {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func (d *Deployer) deployEnv(ctx context.Context, api RepoAPI, t Trigger, root, appDir string, c *config.RepoConfig, envName string, runs []githubapp.Run) {
	env := c.Environments[envName]
	name := "dray: " + c.Name + " → " + envName
	id, err := api.CreateCheck(ctx, t.Repo, t.SHA, githubapp.Check{
		Name: name, Status: "in_progress", Title: "Deploying " + c.Name + " to " + envName,
	})
	if err != nil {
		return
	}
	finish := func(conclusion, title, summary, log string) {
		_ = api.UpdateCheck(ctx, t.Repo, id, githubapp.Check{
			Status: "completed", Conclusion: conclusion, Title: title, Summary: summary, Text: fence(log),
		})
		if d.Note != nil {
			d.Note(Record{Repo: t.Repo, SHA: t.SHA, What: c.Name + " → " + envName, Result: conclusion + ": " + title,
				URL: fmt.Sprintf("https://github.com/%s/runs/%d", t.Repo, id)})
		}
	}
	if env.Release != "direct" || env.Approval != "none" {
		finish("neutral", "Not deployed: needs a release Dray cannot do on merge yet",
			fmt.Sprintf("%s uses release %s with approval %s; deploy on merge handles release direct with approval none so far.", envName, env.Release, env.Approval), "")
		return
	}
	var out bytes.Buffer
	ad, err := d.Adapters(c, envName, env, c.Targets[env.Target], &out)
	if err != nil {
		finish("failure", "Could not set up the deploy", err.Error(), out.String())
		return
	}
	o := deploy.Options{
		Repo: c, RepoDir: appDir, EnvName: envName, Env: env, Target: c.Targets[env.Target], Out: &out,
		Registry: ad.Registry, Builder: ad.Builder, Cluster: ad.Cluster, Secrets: ad.Secrets,
		Source: &commitSource{api: api, sha: t.SHA, root: root, app: appDir, sources: sourcesOf(c)},
		CI:     &commitCI{sha: t.SHA, runs: runs},
	}
	if err := deploy.Run(ctx, o); err != nil {
		finish("failure", "Deploy failed", err.Error(), out.String())
		return
	}
	finish("success", "Deployed "+c.Name+" to "+envName, "Deployed "+shortSHA(t.SHA)+".", out.String())
}

func fence(log string) string {
	if strings.TrimSpace(log) == "" {
		return ""
	}
	return "```\n" + strings.ReplaceAll(log, "```", "ʼʼʼ") + "\n```"
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// download unpacks a commit into dir.
func download(ctx context.Context, api RepoAPI, repo, ref, dir string) error {
	f, err := os.CreateTemp("", "dray-tarball-*.tgz")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()); _ = f.Close() }()
	if err := api.Tarball(ctx, repo, ref, f); err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	zr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("reading %s@%s: %w", repo, ref, err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return gitsrc.ExtractStrip(root, zr, 1)
}

// findApps are the folders holding a .dray/config.yaml: the repo itself, or
// apps in subfolders of a monorepo. Dependency folders are skipped.
func findApps(repoDir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(repoDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", ".git", "vendor", "bin", "obj", "dist":
				return filepath.SkipDir
			}
			if rel, _ := filepath.Rel(repoDir, p); strings.Count(rel, string(filepath.Separator)) > 3 {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "config.yaml" && filepath.Base(filepath.Dir(p)) == ".dray" {
			out = append(out, filepath.Dir(filepath.Dir(p)))
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

func sourcesOf(c *config.RepoConfig) []config.BuildSource {
	var out []config.BuildSource
	seen := map[string]bool{}
	for _, img := range c.Images {
		for _, s := range img.Sources {
			if !seen[s.Path] {
				seen[s.Path] = true
				out = append(out, s)
			}
		}
	}
	return out
}

// commitSource is the downloaded commit as a deploy.Source. A commit is never
// dirty; build sources are downloaded beside the repo at their pinned commits.
type commitSource struct {
	api       RepoAPI
	sha       string
	root, app string
	sources   []config.BuildSource
}

func (s *commitSource) Commit(context.Context) (string, bool, error) {
	return shortSHA(s.sha), false, nil
}

func (s *commitSource) Workspace(ctx context.Context, dirty bool) (deploy.Workspace, error) {
	if dirty {
		return nil, errors.New("a commit from GitHub has no uncommitted changes")
	}
	for _, src := range s.sources {
		dir := filepath.Join(s.root, src.Path)
		if _, err := os.Stat(dir); err == nil {
			continue
		}
		if err := download(ctx, s.api, src.Repo, src.Commit, dir); err != nil {
			return nil, fmt.Errorf("source %s@%s: %w", src.Repo, shortSHA(src.Commit), err)
		}
	}
	return workspace{root: s.root, repo: s.app}, nil
}

type workspace struct{ root, repo string }

func (w workspace) Root() string { return w.root }
func (w workspace) Repo() string { return w.repo }
func (w workspace) Close() error { return nil }

// commitCI answers the deploy's CI gate with the runs already read.
type commitCI struct {
	sha  string
	runs []githubapp.Run
}

func (c *commitCI) Commits(context.Context) ([]string, error) { return []string{c.sha}, nil }

func (c *commitCI) Runs(context.Context) ([]deploy.CIRun, error) {
	out := make([]deploy.CIRun, len(c.runs))
	for i, r := range c.runs {
		out[i] = deploy.CIRun{HeadSha: r.HeadSHA, Status: r.Status, Conclusion: r.Conclusion, WorkflowName: r.Name, URL: r.HTMLURL}
	}
	return out, nil
}
