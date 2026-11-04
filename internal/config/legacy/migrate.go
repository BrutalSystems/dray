package legacy

import (
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/BrutalSystems/dray/internal/config"
)

// Options are what a conversion needs beyond the old config itself.
type Options struct {
	// Machine are the defaults from the old ~/.dray/config.json.
	Machine Defaults
	// Repo is the app's GitHub repository, owner/name.
	Repo string
	// Path is the app's folder inside the repo, when it is not the root.
	Path string
	// Environment names the one environment the app gets; defaults to "prod".
	Environment string
	// RunningCommit returns the commit an image from another repo is running
	// today (from the image tag in the cluster), so its pin changes nothing at
	// cutover. Nil, or "" for an image, leaves the pin for a person to fill in.
	RunningCommit func(image string) string
	// BuildSources finds the other repos an image builds with when its
	// context reaches outside the repo, each pinned to a commit. Nil, or no
	// result, leaves them for a person to list.
	BuildSources func(image Image) []config.BuildSource
}

// Result is a converted config and everything a person must look at.
type Result struct {
	Config   config.RepoConfig
	Comments config.Comments
	Registry config.RegistryEntry
	// Components are draft registry entries for shared services that this
	// app used to build from their repos.
	Components []config.RegistryEntry
	Targets    []config.Target
	Warnings   []Warning
}

// Warning is something the conversion could not decide alone.
type Warning struct {
	Where   string
	Message string
}

func (w Warning) String() string { return w.Where + ": " + w.Message }

// Convert turns a JS-tool config into Dray v2's config and a draft registry
// entry. It never guesses silently: anything it changes or cannot carry over
// becomes a warning.
func Convert(old *Config, opts Options) *Result {
	r := &Result{}
	warn := func(where, format string, args ...any) {
		r.Warnings = append(r.Warnings, Warning{Where: where, Message: fmt.Sprintf(format, args...)})
	}
	envName := opts.Environment
	if envName == "" {
		envName = "prod"
	}
	repoDefaults := opts.Machine
	if old.Defaults != nil {
		repoDefaults = repoDefaults.merge(*old.Defaults)
	}

	c := &r.Config
	c.Name = old.Name
	keys := make([]string, 0, len(old.Notes))
	for k := range old.Notes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		r.Comments.Head = append(r.Comments.Head, "Note carried over from the old config ("+k+"):", old.Notes[k])
	}

	appSecret := func(name string) config.SecretRef {
		return config.SecretRef("secret:" + SecretPath(old.Name, name))
	}

	// Images: built here, or pinned components when they came from another repo.
	for i, img := range old.Images {
		where := fmt.Sprintf("images[%d] %s", i, img.Name)
		if img.Source.Git != "" {
			comp, repo := componentFromGit(img.Source.Git)
			u := config.Use{Component: comp}
			if comp != img.Name {
				u.As = img.Name
			}
			u.TemplateVar = img.TemplateVar
			if opts.RunningCommit != nil {
				u.Commit = opts.RunningCommit(img.Name)
			}
			if u.Commit == "" {
				warn(where, "was built from %s at %q; pin the commit or version it runs today in uses (D14)", img.Source.Git, refOr(img.Source.Ref))
			} else {
				warn(where, "was built from %s at %q; pinned to the commit running today, %s (D14)", img.Source.Git, refOr(img.Source.Ref), u.Commit)
			}
			c.Uses = append(c.Uses, u)
			if repo != "" && !hasComponent(r.Components, comp) {
				r.Components = append(r.Components, config.RegistryEntry{
					Name: comp, Repo: repo, Kind: config.KindComponent, ReleaseOn: "tag",
				})
			}
			if img.Latest {
				warn(where, "the mutable :latest tag is gone; workloads pin a digest (D4)")
			}
			continue
		}
		n := config.Image{Name: img.Name, TemplateVar: img.TemplateVar}
		if img.ECR != "" && img.ECR != img.Name {
			n.Repository = img.ECR
		}
		if img.Dockerfile != "" && img.Dockerfile != "Dockerfile" {
			n.Dockerfile = img.Dockerfile
		}
		if img.Context != "" && img.Context != "." {
			n.Context = img.Context
		}
		if strings.HasPrefix(img.Dockerfile, "..") || strings.HasPrefix(img.Dockerfile, "/") {
			warn(where, "its Dockerfile %s is outside this repo; move it into the repo", img.Dockerfile)
		}
		if strings.HasPrefix(img.Context, "..") || strings.HasPrefix(img.Context, "/") {
			var found []config.BuildSource
			if opts.BuildSources != nil {
				found = opts.BuildSources(img)
			}
			if len(found) > 0 {
				n.Sources = found
				for _, s := range found {
					warn(where, "builds with %s beside this repo; pinned to commit %s from your local checkout. Check it matches what is deployed (D18)", s.Repo, s.Commit)
				}
			} else {
				warn(where, "builds from %s, the folder above the repo; Dray builds there with only this repo's checkout. If the build needs other repos beside it, list them in sources, each pinned to a commit (D18)", img.Context)
			}
		}
		if a := img.BuildArgs; a != nil {
			ba := &config.BuildArgs{EnvFile: a.EnvFile, Prefix: a.Prefix, Values: a.Values}
			if a.SopsEnvFile != "" {
				ba.FromSecret = appSecret("build-" + img.Name)
				warn(where, "build values from %s now come from %s; import them with dray secrets import. They end up in the image, and in a front end they are public: check none is a real secret", a.SopsEnvFile, ba.FromSecret)
			}
			n.BuildArgs = ba
			if a.EnvFile != "" || a.SopsEnvFile != "" {
				n.BuildPerEnvironment = true
				warn(where, "bakes environment settings in at build time, so it is rebuilt per environment instead of promoted (D13)")
			}
		}
		if img.Latest {
			warn(where, "the mutable :latest tag is gone; workloads pin a digest (D4)")
		}
		if img.DepsImage != nil {
			warn(where, "the cached deps image (%s) is gone; BuildKit's layer cache replaces it. If the Dockerfile starts FROM %s, switch it to a multi-stage build", img.DepsImage.Tag, img.DepsImage.Tag)
		}
		if strings.HasSuffix(img.Name, "-prod") {
			warn(where, "looks like a copy for another environment; map it to an environment instead (D11)")
		}
		c.Images = append(c.Images, n)
		for _, note := range img.Notes {
			r.Comments.AddItem("images", len(c.Images)-1, note)
		}
	}

	// Secrets: same names, values from the secret store.
	for i, s := range old.Secrets {
		c.Secrets = append(c.Secrets, config.Secret{Name: s.Name, From: appSecret(s.Name)})
		for _, note := range s.Notes {
			r.Comments.AddItem("secrets", i, note)
		}
		if len(s.Reflector) > 0 {
			warn(fmt.Sprintf("secrets[%d] %s", i, s.Name), "was copied to other namespaces with reflector annotations; use a ClusterExternalSecret instead")
		}
	}
	if len(old.Secrets) > 0 {
		warn("secrets", "%d secret(s) now come from the secret store; import their values with dray secrets import (D5)", len(old.Secrets))
	}

	// Workloads.
	for i, w := range old.Workloads {
		where := fmt.Sprintf("workloads[%d] %s", i, w.Name)
		n := config.Workload{
			Name: w.Name, Kind: config.WorkloadKind(w.Kind), Manifests: w.Manifests,
			Manual: w.Manual, Disabled: w.Disabled,
		}
		switch {
		case len(w.StampImages) > 0:
			n.Images = w.StampImages
		case w.Image != "":
			n.Images = []string{w.Image}
		}
		for _, d := range w.DependsOn {
			if s, ok := strings.CutPrefix(d, "secret:"); ok {
				n.Secrets = append(n.Secrets, s)
			} else {
				n.DependsOn = append(n.DependsOn, d)
			}
		}
		if w.RolloutTimeoutSeconds > 0 {
			n.RolloutTimeout = &config.Duration{Duration: time.Duration(w.RolloutTimeoutSeconds) * time.Second}
		}
		if w.Namespace != "" && w.Namespace != repoDefaults.Namespace {
			warn(where, "deploys to namespace %s instead of %s; map it to its own environment (D11)", w.Namespace, repoDefaults.Namespace)
		}
		if w.Context != "" || w.Account != "" || w.Region != "" || w.Profile != "" || w.Platform != "" {
			warn(where, "overrides the cluster, account or credentials; targets are per environment now, so map it to its own environment")
		}
		if w.CIGate != nil {
			warn(where, "has its own CI gate setting; the gate is per environment now (ci_required)")
		}
		if strings.HasSuffix(w.Name, "-prod") {
			warn(where, "looks like a copy for another environment; map it to an environment instead (D11)")
		}
		if w.Kind == "cronjob" {
			for _, img := range n.Images {
				if latestImage(old, img) {
					warn(where, "used :latest to track new builds; it now pins a digest and is redeployed with its image (D4)")
					break
				}
			}
		}
		c.Workloads = append(c.Workloads, n)
		for _, note := range w.Notes {
			r.Comments.AddItem("workloads", i, note)
		}
	}

	if p := old.Pilets; p != nil && len(p.Names) > 0 {
		c.Pilets = &config.Pilets{Names: p.Names, Command: p.Command}
		if p.SecretsFile != "" {
			c.Pilets.Env = appSecret("pilets")
			warn("pilets", "publish values from %s now come from %s; import them with dray secrets import", p.SecretsFile, c.Pilets.Env)
		}
	}

	// Registry entry and target.
	target := config.Target{
		Name: repoDefaults.Context, Cloud: "aws", Account: repoDefaults.Account, Region: repoDefaults.Region,
		Cluster: repoDefaults.Context, Profile: repoDefaults.Profile, Platform: repoDefaults.Platform,
	}
	if target.Name == "" {
		target.Name = "default"
		warn("defaults", "no kube context set; name the target")
	}
	r.Targets = []config.Target{target}
	env := config.Environment{
		Target: target.Name, Namespace: repoDefaults.Namespace, NamespaceMode: "shared",
		Release: "direct", Approval: "none",
	}
	if repoDefaults.CIGate != nil && *repoDefaults.CIGate {
		env.CIRequired = true
	}
	if env.Namespace == "" {
		warn("defaults", "no namespace set; fill it in")
	}
	r.Registry = config.RegistryEntry{
		Name: old.Name, Repo: opts.Repo, Path: opts.Path,
		Environments: map[string]config.Environment{envName: env},
	}
	if opts.Repo == "" {
		warn("registry", "set repo to the app's GitHub repository (owner/name)")
	}
	warn("registry", "the %s environment keeps today's behavior: namespace %s marked shared (D15), direct apply, no approval. Tighten it once the app is on Dray", envName, env.Namespace)
	return r
}

func refOr(ref string) string {
	if ref == "" {
		return "HEAD"
	}
	return ref
}

// componentFromGit names a component after its repository: jobs-service for
// https://github.com/org/jobs-service(.git). It also returns owner/name when
// the URL is on GitHub.
func componentFromGit(raw string) (name, repo string) {
	s := strings.TrimSuffix(strings.TrimSpace(raw), "/")
	s = strings.TrimSuffix(s, ".git")
	if strings.HasPrefix(s, "git@") {
		if _, after, ok := strings.Cut(s, ":"); ok {
			repo = after
		}
	} else if u, err := url.Parse(s); err == nil {
		repo = strings.TrimPrefix(u.Path, "/")
	}
	name = path.Base(s)
	if strings.Count(repo, "/") != 1 {
		repo = ""
	}
	return strings.ToLower(name), repo
}

func hasComponent(list []config.RegistryEntry, name string) bool {
	for _, e := range list {
		if e.Name == name {
			return true
		}
	}
	return false
}

func latestImage(c *Config, name string) bool {
	for _, img := range c.Images {
		if img.Name == name {
			return img.Latest
		}
	}
	return false
}
