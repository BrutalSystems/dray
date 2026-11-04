package config

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// ValidationError lists every problem found, each with where it is.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return "invalid config:\n  - " + strings.Join(e.Problems, "\n  - ")
}

type problems []string

func (p *problems) addf(where, format string, args ...any) {
	*p = append(*p, where+": "+fmt.Sprintf(format, args...))
}

func (p problems) err() error {
	if len(p) == 0 {
		return nil
	}
	return &ValidationError{Problems: p}
}

var (
	dnsLabel   = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	secretName = regexp.MustCompile(`^[a-z0-9]([-.a-z0-9]*[a-z0-9])?$`)
	templateV  = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	semver     = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)
	commitSHA  = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	repoSlug   = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	imageRef   = regexp.MustCompile(`^[a-z0-9.-]+(:[0-9]+)?(/[a-z0-9._-]+)+$`)
	sourcePath = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
)

// Validate checks a repo config's values and the references between them.
func (c *RepoConfig) Validate() error {
	var p problems
	if !dnsLabel.MatchString(c.Name) || len(c.Name) > 63 {
		p.addf("name", "%q must be lowercase letters, digits and dashes, at most 63 characters", c.Name)
	}

	imageNames := map[string]string{} // name -> where it was declared
	declare := func(name, where string) {
		if prev, ok := imageNames[name]; ok {
			p.addf(where, "image name %q is already used by %s", name, prev)
			return
		}
		imageNames[name] = where
	}
	for i, img := range c.Images {
		where := fmt.Sprintf("images[%d]", i)
		if !dnsLabel.MatchString(img.Name) {
			p.addf(where+".name", "%q must be lowercase letters, digits and dashes", img.Name)
		}
		declare(img.Name, where)
		checkRepoPath(&p, where+".dockerfile", img.Dockerfile)
		switch {
		case path.Clean(img.Context) == ".." && len(img.Sources) > 0:
			// The shared folder that holds this repo and its sources.
		case path.Clean(img.Context) == "..":
			p.addf(where+".context", `".." reaches outside the repo; list the other repos the build needs in sources`)
		default:
			checkRepoPath(&p, where+".context", img.Context)
		}
		seenPaths := map[string]bool{}
		for j, s := range img.Sources {
			sw := fmt.Sprintf("%s.sources[%d]", where, j)
			if !repoSlug.MatchString(s.Repo) {
				p.addf(sw+".repo", "%q must be owner/name", s.Repo)
			}
			if !sourcePath.MatchString(s.Path) || s.Path == "." || s.Path == ".." {
				p.addf(sw+".path", "%q must be a single folder name", s.Path)
			}
			if seenPaths[s.Path] {
				p.addf(sw+".path", "folder %q is used twice", s.Path)
			}
			seenPaths[s.Path] = true
			if !commitSHA.MatchString(s.Commit) {
				p.addf(sw+".commit", "pin a commit hash, got %q", s.Commit)
			}
		}
		if img.TemplateVar != "" && !templateV.MatchString(img.TemplateVar) {
			p.addf(where+".template_var", "%q must be upper-case letters, digits and underscores", img.TemplateVar)
		}
		if a := img.BuildArgs; a != nil {
			checkRepoPath(&p, where+".build_args.env_file", a.EnvFile)
			if a.FromSecret != "" {
				if err := a.FromSecret.Validate(); err != nil {
					p.addf(where+".build_args.from_secret", "%v", err)
				}
			}
			if a.EnvFile == "" && a.FromSecret == "" && len(a.Values) == 0 {
				p.addf(where+".build_args", "set env_file, from_secret or values, or remove build_args")
			}
		}
	}
	for i, u := range c.Uses {
		where := fmt.Sprintf("uses[%d]", i)
		if !dnsLabel.MatchString(u.Component) {
			p.addf(where+".component", "%q must be lowercase letters, digits and dashes", u.Component)
		}
		switch {
		case u.Version == "" && u.Commit == "":
			p.addf(where, "pin %s with version or commit", u.Component)
		case u.Version != "" && u.Commit != "":
			p.addf(where, "set version or commit, not both")
		case u.Version != "" && !semver.MatchString(u.Version):
			p.addf(where+".version", "%q is not a version such as 1.8.0", u.Version)
		case u.Commit != "" && !commitSHA.MatchString(u.Commit):
			p.addf(where+".commit", "%q is not a commit hash", u.Commit)
		}
		if u.As != "" && !dnsLabel.MatchString(u.As) {
			p.addf(where+".as", "%q must be lowercase letters, digits and dashes", u.As)
		}
		if u.TemplateVar != "" && !templateV.MatchString(u.TemplateVar) {
			p.addf(where+".template_var", "%q must be upper-case letters, digits and underscores", u.TemplateVar)
		}
		declare(u.LocalName(), where)
	}

	secretNames := map[string]bool{}
	for i, s := range c.Secrets {
		where := fmt.Sprintf("secrets[%d]", i)
		if !secretName.MatchString(s.Name) {
			p.addf(where+".name", "%q is not a valid Kubernetes Secret name", s.Name)
		}
		if secretNames[s.Name] {
			p.addf(where+".name", "secret %q is declared twice", s.Name)
		}
		secretNames[s.Name] = true
		if err := s.From.Validate(); err != nil {
			p.addf(where+".from", "%v", err)
		}
	}

	workloads := map[string]*Workload{}
	for i := range c.Workloads {
		w := &c.Workloads[i]
		where := fmt.Sprintf("workloads[%d]", i)
		if !dnsLabel.MatchString(w.Name) {
			p.addf(where+".name", "%q must be lowercase letters, digits and dashes", w.Name)
		}
		if workloads[w.Name] != nil {
			p.addf(where+".name", "workload %q is declared twice", w.Name)
		}
		workloads[w.Name] = w
		if w.Kind != KindDeployment && w.Kind != KindCronJob {
			p.addf(where+".kind", "%q must be deployment or cronjob", w.Kind)
		}
		if len(w.Manifests) == 0 {
			p.addf(where+".manifests", "list at least one manifest")
		}
		for j, m := range w.Manifests {
			checkRepoPath(&p, fmt.Sprintf("%s.manifests[%d]", where, j), m)
		}
		for j, img := range w.Images {
			if _, ok := imageNames[img]; !ok {
				p.addf(fmt.Sprintf("%s.images[%d]", where, j), "unknown image %q; declare it in images or uses", img)
			}
		}
		for j, s := range w.Secrets {
			if !secretNames[s] {
				p.addf(fmt.Sprintf("%s.secrets[%d]", where, j), "unknown secret %q; declare it in secrets", s)
			}
		}
		if w.RolloutTimeout != nil && w.RolloutTimeout.Duration <= 0 {
			p.addf(where+".rollout_timeout", "must be more than zero")
		}
		if w.Manual && w.Disabled {
			p.addf(where, "a workload is manual or disabled, not both")
		}
	}
	for i, w := range c.Workloads {
		for j, d := range w.DependsOn {
			switch {
			case d == w.Name:
				p.addf(fmt.Sprintf("workloads[%d].depends_on[%d]", i, j), "a workload cannot depend on itself")
			case workloads[d] == nil:
				p.addf(fmt.Sprintf("workloads[%d].depends_on[%d]", i, j), "unknown workload %q", d)
			}
		}
	}
	if cycle := findCycle(c.Workloads); cycle != "" {
		p.addf("workloads", "depends_on forms a cycle: %s", cycle)
	}

	if pl := c.Pilets; pl != nil {
		if len(pl.Names) == 0 {
			p.addf("pilets.names", "list at least one pilet")
		}
		if len(pl.Command) == 0 {
			p.addf("pilets.command", "give the command that publishes one pilet")
		}
		if pl.Env != "" {
			if err := pl.Env.Validate(); err != nil {
				p.addf("pilets.env", "%v", err)
			}
		}
	}
	return p.err()
}

// checkRepoPath rejects paths that could leave the repo: absolute paths and
// any ".." step. Empty is allowed; callers apply defaults.
func checkRepoPath(p *problems, where, v string) {
	if v == "" {
		return
	}
	if path.IsAbs(v) || strings.HasPrefix(v, "\\") {
		p.addf(where, "%q must be relative to the repo root", v)
		return
	}
	for _, part := range strings.Split(strings.ReplaceAll(v, "\\", "/"), "/") {
		if part == ".." {
			p.addf(where, "%q must stay inside the repo", v)
			return
		}
	}
}

func findCycle(ws []Workload) string {
	deps := map[string][]string{}
	for _, w := range ws {
		deps[w.Name] = w.DependsOn
	}
	names := make([]string, 0, len(deps))
	for n := range deps {
		names = append(names, n)
	}
	sort.Strings(names)
	const (
		unvisited = iota
		visiting
		done
	)
	state := map[string]int{}
	var stack []string
	var visit func(string) string
	visit = func(n string) string {
		switch state[n] {
		case visiting:
			for i, s := range stack {
				if s == n {
					return strings.Join(append(append([]string{}, stack[i:]...), n), " -> ")
				}
			}
		case done:
			return ""
		}
		state[n] = visiting
		stack = append(stack, n)
		for _, d := range deps[n] {
			if _, ok := deps[d]; !ok || d == n {
				continue
			}
			if c := visit(d); c != "" {
				return c
			}
		}
		stack = stack[:len(stack)-1]
		state[n] = done
		return ""
	}
	for _, n := range names {
		if c := visit(n); c != "" {
			return c
		}
	}
	return ""
}

// Validate checks a registry entry.
func (e *RegistryEntry) Validate() error {
	var p problems
	if !dnsLabel.MatchString(e.Name) || len(e.Name) > 63 {
		p.addf("name", "%q must be lowercase letters, digits and dashes, at most 63 characters", e.Name)
	}
	if !repoSlug.MatchString(e.Repo) {
		p.addf("repo", "%q must be owner/name", e.Repo)
	}
	checkRepoPath(&p, "path", e.Path)
	for i, t := range e.ThirdParty {
		if !imageRef.MatchString(t) {
			p.addf(fmt.Sprintf("third_party[%d]", i), "%q must be an image without tag or digest, such as docker.io/library/postgres", t)
		}
	}
	switch e.EffectiveKind() {
	case KindApp:
		if e.ReleaseOn != "" {
			p.addf("release_on", "only components have release_on")
		}
		if len(e.Environments) == 0 {
			p.addf("environments", "an app needs at least one environment")
		}
	case KindComponent:
		if e.ReleaseOn != "tag" {
			p.addf("release_on", "a component must set release_on: tag")
		}
		if len(e.Environments) != 0 {
			p.addf("environments", "a component never deploys on its own; apps pin it with uses")
		}
	default:
		p.addf("kind", "%q must be app or component", e.Kind)
	}

	envNames := make([]string, 0, len(e.Environments))
	for n := range e.Environments {
		envNames = append(envNames, n)
	}
	sort.Strings(envNames)
	for _, n := range envNames {
		env := e.Environments[n]
		where := "environments." + n
		if !dnsLabel.MatchString(n) {
			p.addf(where, "environment names are lowercase letters, digits and dashes")
		}
		if env.Target == "" {
			p.addf(where+".target", "name the target cluster")
		}
		if !dnsLabel.MatchString(env.Namespace) || len(env.Namespace) > 63 {
			p.addf(where+".namespace", "%q is not a valid namespace", env.Namespace)
		}
		oneOf(&p, where+".namespace_mode", env.NamespaceMode, true, "owned", "shared")
		oneOf(&p, where+".release", env.Release, false, "direct", "gitops", "feed")
		oneOf(&p, where+".approval", env.Approval, false, "none", "passkey", "chat")
		oneOf(&p, where+".on_merge", env.OnMerge, true, "off", "deploy")
		if env.PromoteFrom != "" {
			if env.PromoteFrom == n {
				p.addf(where+".promote_from", "an environment cannot promote from itself")
			} else if _, ok := e.Environments[env.PromoteFrom]; !ok {
				p.addf(where+".promote_from", "unknown environment %q", env.PromoteFrom)
			}
		}
	}
	for _, n := range envNames {
		seen := map[string]bool{n: true}
		for cur := e.Environments[n].PromoteFrom; cur != ""; cur = e.Environments[cur].PromoteFrom {
			if seen[cur] {
				p.addf("environments."+n+".promote_from", "promotion forms a loop through %q", cur)
				break
			}
			seen[cur] = true
		}
	}
	return p.err()
}

func oneOf(p *problems, where, v string, optional bool, allowed ...string) {
	if v == "" && optional {
		return
	}
	for _, a := range allowed {
		if v == a {
			return
		}
	}
	p.addf(where, "%q must be one of %s", v, strings.Join(allowed, ", "))
}

var (
	accountID = regexp.MustCompile(`^[0-9]{12}$`)
	regionRE  = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]+$`)
	platform  = regexp.MustCompile(`^[a-z0-9]+/[a-z0-9]+(/[a-z0-9]+)?$`)
)

// Validate checks a target.
func (t *Target) Validate() error {
	var p problems
	if !dnsLabel.MatchString(t.Name) {
		p.addf("name", "%q must be lowercase letters, digits and dashes", t.Name)
	}
	oneOf(&p, "cloud", t.Cloud, false, "aws")
	if t.Cloud == "aws" {
		if !accountID.MatchString(t.Account) {
			p.addf("account", "%q is not a 12-digit AWS account ID", t.Account)
		}
		if !regionRE.MatchString(t.Region) {
			p.addf("region", "%q is not a region such as us-east-2", t.Region)
		}
	}
	if t.Cluster == "" {
		p.addf("cluster", "name the Kubernetes context")
	}
	if t.Platform != "" && !platform.MatchString(t.Platform) {
		p.addf("platform", "%q is not a platform such as linux/arm64", t.Platform)
	}
	return p.err()
}
