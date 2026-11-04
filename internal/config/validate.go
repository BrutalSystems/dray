package config

import (
	"fmt"
	"net/url"
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
	repoName   = regexp.MustCompile(`^[a-z0-9]+([._/-][a-z0-9]+)*$`)
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
		case path.Clean(img.Context) == "..":
			// The build folder that holds this repo's checkout, named after
			// its repository, and its sources, if any; nothing else.
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
		if u.Repository != "" && !repoName.MatchString(u.Repository) {
			p.addf(where+".repository", "%q is not a container repository name", u.Repository)
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
		for i, n := range pl.Names {
			if !dnsLabel.MatchString(n) {
				p.addf(fmt.Sprintf("pilets.names[%d]", i), "%q must be lowercase letters, digits and dashes", n)
			}
		}
		if pl.Path != "" {
			checkRepoPath(&p, "pilets.path", strings.ReplaceAll(pl.Path, "{name}", "x"))
		}
		if pl.Env != "" && len(pl.Command) == 0 {
			p.addf("pilets.env", "env is passed to command; without a command the key comes from the environment's feed")
		}
		if pl.Env != "" {
			if err := pl.Env.Validate(); err != nil {
				p.addf("pilets.env", "%v", err)
			}
		}
	}
	c.checkDeploy(&p)
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

// checkTarget checks one target.
func checkTarget(p *problems, where string, t Target) {
	oneOf(p, where+".cloud", t.Cloud, false, "aws")
	if t.Cloud == "aws" {
		if !accountID.MatchString(t.Account) {
			p.addf(where+".account", "%q is not a 12-digit AWS account ID", t.Account)
		}
		if !regionRE.MatchString(t.Region) {
			p.addf(where+".region", "%q is not a region such as us-east-2", t.Region)
		}
	}
	if t.Cluster == "" {
		p.addf(where+".cluster", "name the Kubernetes cluster")
	}
	if t.Platform != "" && !platform.MatchString(t.Platform) {
		p.addf(where+".platform", "%q is not a platform such as linux/arm64", t.Platform)
	}
}

// checkDeploy checks where the app deploys: environments, targets, the
// secret store and the third-party images.
func (c *RepoConfig) checkDeploy(p *problems) {
	for i, t := range c.ThirdParty {
		if !imageRef.MatchString(t) {
			p.addf(fmt.Sprintf("third_party[%d]", i), "%q must be an image without tag or digest, such as docker.io/library/postgres", t)
		}
	}
	for _, n := range sortedNames(c.Targets) {
		if !dnsLabel.MatchString(n) {
			p.addf("targets."+n, "target names are lowercase letters, digits and dashes")
		}
		checkTarget(p, "targets."+n, c.Targets[n])
	}
	envNames := sortedNames(c.Environments)
	for _, n := range envNames {
		env := c.Environments[n]
		where := "environments." + n
		if !dnsLabel.MatchString(n) {
			p.addf(where, "environment names are lowercase letters, digits and dashes")
		}
		if env.Release == "feed" {
			checkFeed(p, where, env)
		} else {
			if _, ok := c.Targets[env.Target]; !ok {
				p.addf(where+".target", "%q is not in targets", env.Target)
			}
			if !dnsLabel.MatchString(env.Namespace) || len(env.Namespace) > 63 {
				p.addf(where+".namespace", "%q is not a valid namespace", env.Namespace)
			}
			if env.Feed != nil {
				p.addf(where+".feed", "only a release: feed environment has a feed")
			}
		}
		oneOf(p, where+".namespace_mode", env.NamespaceMode, true, "owned", "shared")
		oneOf(p, where+".release", env.Release, false, "direct", "gitops", "feed")
		oneOf(p, where+".approval", env.Approval, false, "none", "passkey", "chat")
		oneOf(p, where+".on_merge", env.OnMerge, true, "off", "deploy")
		if env.PromoteFrom != "" {
			if env.PromoteFrom == n {
				p.addf(where+".promote_from", "an environment cannot promote from itself")
			} else if _, ok := c.Environments[env.PromoteFrom]; !ok {
				p.addf(where+".promote_from", "unknown environment %q", env.PromoteFrom)
			}
		}
	}
	for _, n := range envNames {
		seen := map[string]bool{n: true}
		for cur := c.Environments[n].PromoteFrom; cur != ""; cur = c.Environments[cur].PromoteFrom {
			if seen[cur] {
				p.addf("environments."+n+".promote_from", "promotion forms a loop through %q", cur)
				break
			}
			seen[cur] = true
		}
	}
	if st := c.SecretStore; st != nil {
		oneOf(p, "secret_store.type", st.Type, false, "openbao", "file")
		switch st.Type {
		case "openbao":
			if st.File != nil {
				p.addf("secret_store.file", "the store is openbao; remove file")
			}
			if o := st.OpenBao; o != nil && o.Client != "" && !dnsLabel.MatchString(o.Client) {
				p.addf("secret_store.openbao.client", "%q must be lowercase letters, digits and dashes", o.Client)
			}
		case "file":
			if st.File == nil || st.File.Dir == "" {
				p.addf("secret_store.file.dir", "name the folder")
			}
			if st.OpenBao != nil {
				p.addf("secret_store.openbao", "the store is file; remove openbao")
			}
		}
	}
}

func checkFeed(p *problems, where string, env Environment) {
	f := env.Feed
	if f == nil {
		p.addf(where+".feed", "a release: feed environment needs a feed (url and api_key)")
		return
	}
	if u, err := url.Parse(f.URL); err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Host == "" {
		p.addf(where+".feed.url", "%q is not an http(s) URL", f.URL)
	}
	if err := f.APIKey.Validate(); err != nil {
		p.addf(where+".feed.api_key", "%v", err)
	} else if !strings.Contains(string(f.APIKey), "#") {
		p.addf(where+".feed.api_key", "name the key within the secret: %s#<KEY>", f.APIKey)
	}
	oneOf(p, where+".feed.version", f.Version, true, "commit", "package")
	if env.Target != "" || env.Namespace != "" {
		p.addf(where, "a release: feed environment has no target or namespace")
	}
}

func sortedNames[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
