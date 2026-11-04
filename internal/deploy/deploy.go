// Package deploy ships a repo's workloads to one environment: build and push
// the images it needs, fill their digests into the manifests, apply them with
// the secrets they need, and watch each rollout.
//
// It ports the JS tool's deploy (`dray ship`) and keeps its safety rules:
//
//   - A dirty working tree is refused unless asked for, and only an
//     environment that allows it takes such a build.
//   - The CI gate, where the environment requires it, refuses a commit whose
//     CI is red, pending or absent, and refuses when CI cannot be read.
//   - An image already in the registry for this commit is reused, never
//     rebuilt and pushed over.
//   - A rollout is undone only when Kubernetes itself reports it failed
//     (ProgressDeadlineExceeded); a wait that merely timed out leaves it alone.
//   - A Deployment that is already rolling out is waited on, not restarted,
//     so pods are never replaced twice.
//
// Every external system sits behind an interface (ports.go).
package deploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BrutalSystems/dray/internal/config"
	"github.com/BrutalSystems/dray/internal/render"
	"github.com/BrutalSystems/dray/internal/secrets"
	"github.com/BrutalSystems/dray/internal/validate"
)

// DefaultRolloutTimeout matches Kubernetes' own progress deadline. Waiting
// less and calling it a failure gives up while the cluster still considers
// the rollout healthy, as a cold node pulling a large image routinely causes.
const DefaultRolloutTimeout = 600 * time.Second

// Options say what to deploy where, and through which adapters.
type Options struct {
	Repo    *config.RepoConfig
	RepoDir string
	EnvName string
	Env     config.Environment
	Target  config.Target
	// Workloads are deployed by name; empty is every workload that is
	// neither manual nor disabled.
	Workloads []string

	// DryRun builds, pushes and applies nothing; it shows what would change.
	DryRun bool
	// AllowDirty builds from uncommitted changes, for environments that allow it.
	AllowDirty bool
	// SkipCI deploys without the CI gate.
	SkipCI bool
	// InCI says Dray runs inside the CI pipeline itself, which has already
	// decided to deploy; the gate would wait on its own run.
	InCI bool
	// Restart restarts Deployments even when nothing changed, such as after a
	// secret was changed by hand.
	Restart bool

	Out      io.Writer
	Registry Registry
	Builder  Builder
	Cluster  Cluster
	Source   Source
	CI       CI
	// Secrets is the secret store; needed only when a workload uses secrets
	// or an image takes build arguments from one.
	Secrets secrets.Store
	// Checker checks rendered manifests before they are applied; nil skips
	// the check.
	Checker Checker
	// Now is the clock, for dirty build tags.
	Now func() time.Time
}

type image struct {
	cfg    config.Image
	repo   string
	tag    string
	digest string
	build  bool
	args   map[string]string
	// component images are pinned in uses and taken from the registry as
	// they are; Dray never builds them for an app.
	component bool
}

func (i *image) ref(host string) string {
	r := host + "/" + i.repo + ":" + i.tag
	if i.digest != "" {
		r += "@" + i.digest
	}
	return r
}

type state struct {
	o       Options
	commit  string
	dirty   bool
	work    []config.Workload
	images  []*image
	secrets map[string][]byte // name -> Secret manifest
	changed map[string]bool   // workload or "secret/<name>" -> apply changes it
	docs    map[string][]byte // workload -> rendered manifests
	files   map[string][]render.Manifest
	// problems found by a dry run, which reports them and carries on.
	problems []string
	// before is each Deployment's state just before the apply.
	before map[string]DeploymentState
	// claim is set when the namespace must be created or labeled as this app's.
	claim bool
}

// problem stops a real deploy; a dry run reports it and goes on.
func (s *state) problem(err error) error {
	if err == nil || !s.o.DryRun {
		return err
	}
	s.problems = append(s.problems, err.Error())
	s.printf("✗ %v", err)
	return nil
}

func (s *state) printf(format string, a ...any) {
	_, _ = fmt.Fprintf(s.o.Out, format+"\n", a...)
}

// Run deploys.
func Run(ctx context.Context, o Options) error {
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	s := &state{o: o, secrets: map[string][]byte{}, changed: map[string]bool{}, docs: map[string][]byte{}, before: map[string]DeploymentState{}}
	steps := []func(context.Context) error{
		s.selectWorkloads, s.checkSource, s.gate, s.checkNamespace, s.planImages, s.readSecrets,
	}
	// Render and check before building, so a bad manifest stops the deploy in
	// seconds rather than after the builds; a real deploy renders again once
	// the images' digests are known.
	steps = append(steps, s.render, s.check)
	if !o.DryRun {
		steps = append(steps, s.build, s.render)
	}
	steps = append(steps, s.diff)
	if !o.DryRun {
		steps = append(steps, s.apply, s.rollout)
	}
	for _, step := range steps {
		if err := step(ctx); err != nil {
			return err
		}
	}
	if o.DryRun {
		s.printf("\nDry run: nothing was built, pushed or applied.")
		if n := len(s.problems); n > 0 {
			return fmt.Errorf("the dry run found %d problem(s) a deploy would stop on:\n  - %s", n, strings.Join(s.problems, "\n  - "))
		}
	}
	return nil
}

// selectWorkloads picks the workloads to deploy, as the JS tool did: named
// ones (manual included, disabled refused), or every workload that is
// neither manual nor disabled. They are ordered so dependencies come first.
func (s *state) selectWorkloads(context.Context) error {
	c := s.o.Repo
	var picked []config.Workload
	if len(s.o.Workloads) == 0 {
		for _, w := range c.Workloads {
			if !w.Disabled && !w.Manual {
				picked = append(picked, w)
			}
		}
	} else {
		for _, n := range s.o.Workloads {
			i := indexOf(c.Workloads, n)
			if i < 0 {
				return fmt.Errorf("%s has no workload %q", c.Name, n)
			}
			if c.Workloads[i].Disabled {
				return fmt.Errorf("workload %s is disabled in %s's config", n, c.Name)
			}
			picked = append(picked, c.Workloads[i])
		}
	}
	if len(picked) == 0 {
		return fmt.Errorf("%s has no workload to deploy", c.Name)
	}
	s.work = topoSort(picked)
	return nil
}

func indexOf(ws []config.Workload, name string) int {
	for i, w := range ws {
		if w.Name == name {
			return i
		}
	}
	return -1
}

func topoSort(ws []config.Workload) []config.Workload {
	byName := map[string]config.Workload{}
	for _, w := range ws {
		byName[w.Name] = w
	}
	var out []config.Workload
	seen := map[string]bool{}
	var visit func(w config.Workload)
	visit = func(w config.Workload) {
		if seen[w.Name] {
			return
		}
		seen[w.Name] = true
		for _, d := range w.DependsOn {
			if dep, ok := byName[d]; ok {
				visit(dep)
			}
		}
		out = append(out, w)
	}
	for _, w := range ws {
		visit(w)
	}
	return out
}

func (s *state) checkSource(ctx context.Context) error {
	commit, dirty, err := s.o.Source.Commit(ctx)
	if err != nil {
		return err
	}
	s.commit, s.dirty = commit, dirty
	if dirty {
		if !s.o.AllowDirty {
			return fmt.Errorf("the working tree has uncommitted changes (commit them, or pass --allow-dirty for an environment that allows it)")
		}
		if !s.o.Env.AllowDirty {
			return fmt.Errorf("environment %s does not take builds from uncommitted changes; commit them first", s.o.EnvName)
		}
		s.printf("! building from uncommitted changes on top of %s", commit)
	}
	return nil
}

func (s *state) gate(ctx context.Context) error {
	if !s.o.Env.CIRequired || s.o.DryRun {
		return nil
	}
	if s.o.InCI {
		s.printf("CI gate: running inside CI, which already decided to deploy")
		return nil
	}
	if s.o.SkipCI {
		s.printf("!! CI gate SKIPPED (--skip-ci-check): deploying unverified code")
		return nil
	}
	if s.dirty {
		return fmt.Errorf("the CI gate needs a pushed commit: no CI run can exist for uncommitted changes")
	}
	v, err := checkCI(ctx, s.o.CI)
	if err != nil {
		return err
	}
	if !v.OK {
		msg := "the CI gate stopped this deploy: " + v.Detail
		if v.URL != "" {
			msg += "\n  " + v.URL
		}
		return errors.New(msg + "\n  Pass --skip-ci-check to deploy anyway.")
	}
	s.printf("✓ %s", v.Detail)
	return nil
}

// planImages decides each image's tag and whether it must be built: an image
// the registry already holds for this commit is reused.
func (s *state) planImages(ctx context.Context) error {
	c := s.o.Repo
	seen := map[string]bool{}
	for _, w := range s.work {
		for _, name := range w.Images {
			if seen[name] {
				continue
			}
			seen[name] = true
			i := imageIndex(c.Images, name)
			if i < 0 {
				img, err := s.component(ctx, name)
				if err != nil {
					return err
				}
				s.images = append(s.images, img)
				continue
			}
			img := &image{cfg: c.Images[i], repo: c.Images[i].Repository, tag: s.commit}
			if img.repo == "" {
				img.repo = img.cfg.Name
			}
			if img.cfg.BuildPerEnvironment {
				img.tag += "-" + s.o.EnvName
			}
			if s.dirty {
				// Each dirty build gets its own tag: registries keep tags immutable.
				img.tag += "-dirty-" + s.o.Now().UTC().Format("20060102150405")
				img.build = true
			} else {
				d, err := s.o.Registry.Digest(ctx, img.repo, img.tag)
				if err != nil {
					return err
				}
				img.digest, img.build = d, d == ""
			}
			if img.build {
				args, err := buildArgs(ctx, img.cfg, s.o.RepoDir, s.commit, s.o.Secrets)
				if err := s.problem(err); err != nil {
					return err
				}
				img.args = args
			}
			s.images = append(s.images, img)
		}
	}
	for _, img := range s.images {
		switch {
		case img.component:
			s.printf("image %s: component, deploying %s", img.cfg.Name, img.ref(s.o.Registry.Host()))
		case img.build:
			s.printf("image %s: build %s", img.cfg.Name, img.ref(s.o.Registry.Host()))
		default:
			s.printf("image %s: already built, reusing %s", img.cfg.Name, img.ref(s.o.Registry.Host()))
		}
	}
	return nil
}

// component resolves an image pinned in uses to the registry's image for its
// version or commit.
func (s *state) component(ctx context.Context, name string) (*image, error) {
	for _, u := range s.o.Repo.Uses {
		if u.LocalName() != name {
			continue
		}
		img := &image{cfg: config.Image{Name: name, TemplateVar: u.TemplateVar}, repo: u.RepositoryName(), tag: u.Tag(), component: true}
		d, err := s.o.Registry.Digest(ctx, img.repo, img.tag)
		if err != nil {
			return nil, err
		}
		if d == "" {
			// A dry run goes on with the tag, to show the rest.
			return img, s.problem(fmt.Errorf("component %s: %s:%s is not in the registry, and Dray does not build components for an app (D14)", u.Component, img.repo, img.tag))
		}
		img.digest = d
		return img, nil
	}
	return nil, fmt.Errorf("unknown image %q", name)
}

func imageIndex(imgs []config.Image, name string) int {
	for i, img := range imgs {
		if img.Name == name {
			return i
		}
	}
	return -1
}

func (s *state) build(ctx context.Context) error {
	var todo []*image
	for _, img := range s.images {
		if img.build {
			todo = append(todo, img)
		}
	}
	if len(todo) == 0 {
		return nil
	}
	// A builder that holds something for the deploy, such as a build pod,
	// gives it back when the builds are done.
	if c, ok := s.o.Builder.(io.Closer); ok {
		defer func() { _ = c.Close() }()
	}
	if err := s.o.Registry.Login(ctx); err != nil {
		return err
	}
	ws, err := s.o.Source.Workspace(ctx, s.dirty)
	if err != nil {
		return err
	}
	defer func() { _ = ws.Close() }()
	for _, img := range todo {
		if err := s.o.Registry.Ensure(ctx, img.repo); err != nil {
			return err
		}
		spec, err := buildSpec(ws, img, s.o.Registry.Host(), s.o.Target.Platform)
		if err != nil {
			return err
		}
		s.printf("\n── building %s", spec.Ref)
		digest, err := s.o.Builder.Build(ctx, spec)
		if err != nil {
			// Another deploy of the same commit may have pushed first; the
			// registry's image is then the one to deploy.
			if !s.dirty {
				if d, derr := s.o.Registry.Digest(ctx, img.repo, img.tag); derr == nil && d != "" {
					s.printf("%s was pushed by someone else meanwhile; deploying that image", spec.Ref)
					img.digest = d
					continue
				}
			}
			return fmt.Errorf("image %s: %w", img.cfg.Name, err)
		}
		img.digest = digest
		s.printf("pushed %s", img.ref(s.o.Registry.Host()))
	}
	return nil
}

// buildSpec places an image's context and Dockerfile in the workspace. The
// context may reach the workspace root (a context of ".." for builds with
// sources beside the repo) but no further.
func buildSpec(ws Workspace, img *image, host, platform string) (BuildSpec, error) {
	ctxRel := img.cfg.Context
	if ctxRel == "" {
		ctxRel = "."
	}
	df := img.cfg.Dockerfile
	if df == "" {
		df = "Dockerfile"
	}
	ctxDir := filepath.Join(ws.Repo(), filepath.FromSlash(ctxRel))
	if !within(ws.Root(), ctxDir) {
		return BuildSpec{}, fmt.Errorf("image %s: context %s reaches outside the build workspace", img.cfg.Name, ctxRel)
	}
	dfPath := filepath.Join(ws.Repo(), filepath.FromSlash(df))
	if !within(ws.Repo(), dfPath) {
		return BuildSpec{}, fmt.Errorf("image %s: dockerfile %s is outside the repo", img.cfg.Name, df)
	}
	return BuildSpec{Context: ctxDir, Dockerfile: dfPath, Ref: host + "/" + img.repo + ":" + img.tag, Platform: platform, Args: img.args}, nil
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// readSecrets builds the Kubernetes Secrets the chosen workloads need from the
// secret store. Values stay in memory and reach the cluster on stdin.
func (s *state) readSecrets(ctx context.Context) error {
	var names []string
	seen := map[string]bool{}
	for _, w := range s.work {
		for _, n := range w.Secrets {
			if !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	if s.o.Secrets == nil {
		return fmt.Errorf("the workloads need secrets (%s) but no secret store is configured", strings.Join(names, ", "))
	}
	for _, n := range names {
		var from config.SecretRef
		for _, sec := range s.o.Repo.Secrets {
			if sec.Name == n {
				from = sec.From
			}
		}
		if from == "" {
			return fmt.Errorf("secret %s is not declared in secrets", n)
		}
		v, err := secrets.Lookup(ctx, s.o.Secrets, from)
		if err != nil {
			return fmt.Errorf("secret %s (%s): %w", n, from, err)
		}
		doc, err := secretManifest(n, v)
		if err != nil {
			return err
		}
		s.secrets[n] = doc
		s.printf("secret %s: from %s (keys: %s)", n, from, strings.Join(v.Keys(), ", "))
	}
	return nil
}

// Checker checks one rendered manifest file.
type Checker interface {
	Check(file string, content []byte) []validate.Finding
}

// check runs the Checker on every rendered manifest. An error stops a deploy
// before anything is applied (a dry run reports it and goes on); a warning is
// shown.
func (s *state) check(context.Context) error {
	if s.o.Checker == nil {
		return nil
	}
	var errs []string
	for _, w := range s.work {
		for _, m := range s.files[w.Name] {
			for _, f := range s.o.Checker.Check(m.Path, m.Content) {
				if f.Error {
					errs = append(errs, f.String())
				} else {
					s.printf("! %s", f)
				}
			}
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return s.problem(fmt.Errorf("the manifests have %d problem(s):\n    %s", len(errs), strings.Join(errs, "\n    ")))
}

func (s *state) render(context.Context) error {
	refs := map[string]string{}
	for _, img := range s.images {
		refs[img.cfg.Name] = img.ref(s.o.Registry.Host())
	}
	for _, w := range s.work {
		res, err := render.Workload(s.o.RepoDir, s.o.Repo, w, refs)
		if err != nil {
			return err
		}
		for _, u := range res.Unfilled {
			s.printf("! workload %s: %s has no value", w.Name, u)
		}
		var buf bytes.Buffer
		if err := render.Write(&buf, res.Manifests); err != nil {
			return err
		}
		s.docs[w.Name] = buf.Bytes()
		if s.files == nil {
			s.files = map[string][]render.Manifest{}
		}
		s.files[w.Name] = res.Manifests
	}
	return nil
}

// diff asks the cluster what applying would change. Secret diffs are never
// printed, only whether they change.
func (s *state) diff(ctx context.Context) error {
	s.printf("")
	for _, n := range sortedKeys(s.secrets) {
		changed, _, err := s.o.Cluster.Diff(ctx, s.secrets[n])
		if err != nil {
			return fmt.Errorf("secret %s: %w", n, err)
		}
		s.changed["secret/"+n] = changed
		s.printf("secret %s: %s", n, changeWord(changed))
	}
	for _, w := range s.work {
		changed, diff, err := s.o.Cluster.Diff(ctx, s.docs[w.Name])
		if err != nil {
			return fmt.Errorf("workload %s: %w", w.Name, err)
		}
		s.changed[w.Name] = changed
		s.printf("workload %s: %s", w.Name, changeWord(changed))
		if changed && s.o.DryRun {
			s.printf("%s", indent(diff))
		}
	}
	return nil
}

func changeWord(changed bool) string {
	if changed {
		return "changes"
	}
	return "unchanged"
}

func indent(s string) string {
	s = strings.TrimRight(s, "\n")
	return "    " + strings.ReplaceAll(s, "\n", "\n    ")
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// checkNamespace enforces one owner per namespace. An owned namespace (the
// default) is labeled with the app on its first deploy; a namespace labeled
// for another app is refused, whatever the mode.
func (s *state) checkNamespace(ctx context.Context) error {
	ns := s.o.Env.Namespace
	st, err := s.o.Cluster.ReadNamespace(ctx)
	if err != nil {
		return err
	}
	app := s.o.Repo.Name
	if st.Owner != "" && st.Owner != app {
		return fmt.Errorf("namespace %s belongs to app %s (label %s); %s cannot deploy there", ns, st.Owner, OwnerLabel, app)
	}
	shared := s.o.Env.NamespaceMode == "shared"
	switch {
	case shared && !st.Exists:
		return s.problem(fmt.Errorf("shared namespace %s does not exist; Dray creates only namespaces an app owns", ns))
	case shared, st.Owner == app:
	case !st.Exists:
		s.claim = true
		s.printf("namespace %s: does not exist; it will be created for %s", ns, app)
	default:
		s.claim = true
		s.printf("namespace %s: not claimed yet; it will be labeled %s=%s", ns, OwnerLabel, app)
	}
	return nil
}

func (s *state) apply(ctx context.Context) error {
	if s.claim {
		if err := s.o.Cluster.Claim(ctx, s.o.Repo.Name); err != nil {
			return fmt.Errorf("claiming namespace %s: %w", s.o.Env.Namespace, err)
		}
		s.printf("claimed namespace %s for %s", s.o.Env.Namespace, s.o.Repo.Name)
	}
	// Note each Deployment's generation before anything is applied: the
	// rollout step compares against it to tell whether the apply itself
	// started a rollout.
	for _, w := range s.work {
		if w.Kind != config.KindDeployment {
			continue
		}
		st, err := s.o.Cluster.Deployment(ctx, w.Name)
		if err != nil {
			return err
		}
		s.before[w.Name] = st
	}
	for _, n := range sortedKeys(s.secrets) {
		if !s.changed["secret/"+n] {
			continue
		}
		if err := s.o.Cluster.Apply(ctx, s.secrets[n]); err != nil {
			return fmt.Errorf("secret %s: %w", n, err)
		}
		s.printf("applied secret %s", n)
	}
	for _, w := range s.work {
		if !s.changed[w.Name] {
			continue
		}
		if err := s.o.Cluster.Apply(ctx, s.docs[w.Name]); err != nil {
			return fmt.Errorf("workload %s: %w", w.Name, err)
		}
		s.printf("applied %s", w.Name)
	}
	return nil
}

// rollout waits for each Deployment that changed. A Deployment whose rollout
// the apply did not start (only a config map, service or secret changed, or
// --restart) is restarted first, unless it is rolling already; one the apply
// created or changed is only waited on.
func (s *state) rollout(ctx context.Context) error {
	var unfinished []string
	for _, w := range s.work {
		if w.Kind != config.KindDeployment {
			continue
		}
		need := s.changed[w.Name] || s.o.Restart
		for _, n := range w.Secrets {
			need = need || s.changed["secret/"+n]
		}
		if !need {
			continue
		}
		st, err := s.o.Cluster.Deployment(ctx, w.Name)
		if err != nil {
			return err
		}
		if !st.Found {
			s.printf("! workload %s: no Deployment named %s in the namespace; nothing to roll out", w.Name, w.Name)
			continue
		}
		// The apply started a rollout when it created the Deployment or
		// changed its spec (the generation moved). Asking only whether it is
		// still rolling is not enough: by the time an earlier workload has
		// rolled out, this one may have finished too.
		b := s.before[w.Name]
		started := !b.Found || st.Generation > b.Generation
		if !started && !st.Rolling() {
			if err := s.o.Cluster.Restart(ctx, w.Name); err != nil {
				return err
			}
			s.printf("restarted %s", w.Name)
		}
		timeout := DefaultRolloutTimeout
		if w.RolloutTimeout != nil && w.RolloutTimeout.Duration > 0 {
			timeout = w.RolloutTimeout.Duration
		}
		s.printf("\n── waiting for %s (up to %s)", w.Name, timeout)
		werr := s.o.Cluster.Wait(ctx, w.Name, timeout)
		if werr == nil {
			s.printf("✓ %s rolled out", w.Name)
			continue
		}
		// Stopping waiting is not a failed rollout. Undo only when Kubernetes
		// itself gave up; when unsure, leave running code running.
		after, err := s.o.Cluster.Deployment(ctx, w.Name)
		if err == nil && after.DeadlineExceeded {
			s.printf("✗ Kubernetes reports the %s rollout failed (ProgressDeadlineExceeded); undoing it", w.Name)
			if uerr := s.o.Cluster.Undo(ctx, w.Name); uerr != nil {
				return fmt.Errorf("rollout of %s failed, and undoing it failed too: %w", w.Name, uerr)
			}
			return fmt.Errorf("rollout of %s failed and was undone: %w", w.Name, werr)
		}
		s.printf("! %s: stopped waiting, but the cluster still reports the rollout progressing; leaving it alone", w.Name)
		unfinished = append(unfinished, w.Name)
	}
	if len(unfinished) > 0 {
		s.printf("\nStill rolling out: %s. Check with: kubectl rollout status deployment/<name> -n %s --context %s",
			strings.Join(unfinished, ", "), s.o.Env.Namespace, s.o.Target.Cluster)
	}
	return nil
}
