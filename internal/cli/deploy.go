package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BrutalSystems/dray/internal/config"
	"github.com/BrutalSystems/dray/internal/deploy"
	"github.com/BrutalSystems/dray/internal/deploy/docker"
	"github.com/BrutalSystems/dray/internal/deploy/ecr"
	"github.com/BrutalSystems/dray/internal/deploy/ghci"
	"github.com/BrutalSystems/dray/internal/deploy/gitsrc"
	"github.com/BrutalSystems/dray/internal/deploy/kubectl"
	"github.com/BrutalSystems/dray/internal/secrets"
)

// deployFlags are dray deploy's flags.
type deployFlags struct {
	cfgPath, env, repoConfig            string
	workloads                           []string
	dryRun, allowDirty, skipCI, restart bool
}

// NewDeployCmd is `dray deploy` (also `dray ship`, the JS tool's name):
// build, push, apply and roll out a repo's workloads in one environment.
func NewDeployCmd() *cobra.Command {
	var f deployFlags
	cmd := &cobra.Command{
		Use:     "deploy [repo]",
		Aliases: []string{"ship"},
		Short:   "Build, push, apply and roll out a repo's workloads",
		Long: "Deploy a repo (default the current folder) to one of the environments in its\n" +
			".dray/config.yaml:\n" +
			"build and push each image the chosen workloads run (an image already in the\n" +
			"registry for this commit is reused), fill the images into the manifests by\n" +
			"digest, apply them and the secrets they need, and wait for each Deployment to\n" +
			"roll out. A rollout is undone only when Kubernetes reports it failed.\n\n" +
			"Everything about the app comes from the repo. Your own settings\n" +
			"(~/.config/dray/config.yaml, optional) only say which AWS profile to use for\n" +
			"an account (aws_profiles) and, when needed, which kube context a cluster is.\n\n" +
			"--dry-run shows what each workload would change, and builds, pushes and\n" +
			"applies nothing.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return runDeploy(cmd, dir, f)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.cfgPath, "config", "", "your own settings file (default $DRAY_CONFIG or ~/.config/dray/config.yaml)")
	fl.StringVar(&f.env, "env", "", "environment to deploy to (default: the app's only environment)")
	fl.StringVar(&f.repoConfig, "repo-config", "", "read the repo's config from this file instead of .dray/config.yaml, to try a repo before it moves")
	fl.StringArrayVar(&f.workloads, "workload", nil, "deploy only this workload (repeatable; a manual workload ships only when named)")
	fl.BoolVar(&f.dryRun, "dry-run", false, "show what would change; build, push and apply nothing")
	fl.BoolVar(&f.allowDirty, "allow-dirty", false, "build from uncommitted changes (only where the environment sets allow_dirty)")
	fl.BoolVar(&f.skipCI, "skip-ci-check", false, "deploy even if CI is red, pending or absent")
	fl.BoolVar(&f.restart, "restart", false, "restart Deployments even when nothing changed (after a secret was changed by hand)")
	return cmd
}

func runDeploy(cmd *cobra.Command, dir string, f deployFlags) error {
	repoDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	c, err := loadRepoConfig(repoDir, f.repoConfig)
	if err != nil {
		return err
	}
	envName, env, err := pickEnv(c, f.env)
	if err != nil {
		return err
	}
	target := c.Targets[env.Target]
	if env.Release != "direct" {
		return fmt.Errorf("%s's %s environment releases by %s; the CLI deploys only direct environments so far", c.Name, envName, env.Release)
	}
	if env.Approval != "none" {
		return fmt.Errorf("%s's %s environment needs %s approval, which arrives with the Dray server; the CLI cannot approve", c.Name, envName, env.Approval)
	}
	if target.Cloud != "aws" {
		return fmt.Errorf("target %s: only aws targets are supported so far", env.Target)
	}
	u, _, err := loadUserConfig(f.cfgPath)
	if err != nil {
		return err
	}

	var store secrets.Store
	if needsStore(c, f.workloads) {
		if store, err = openStore(repoDir, f.cfgPath, envName); err != nil {
			return err
		}
	}

	inCI := os.Getenv("GITHUB_ACTIONS") == "true"
	profile := u.AWSProfiles[target.Account]
	if inCI {
		// In GitHub Actions credentials arrive through OIDC as environment
		// variables; a named profile does not exist there.
		profile = ""
	}
	kubeContext := target.Cluster
	if k := u.KubeContexts[target.Cluster]; k != "" {
		kubeContext = k
	}
	out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
	o := deploy.Options{
		Repo: c, RepoDir: repoDir, EnvName: envName, Env: env, Target: target, Workloads: f.workloads,
		DryRun: f.dryRun, AllowDirty: f.allowDirty, SkipCI: f.skipCI, InCI: inCI, Restart: f.restart,
		Out:      out,
		Registry: &ecr.Registry{Account: target.Account, Region: target.Region, Profile: profile},
		Builder:  &docker.Builder{Progress: errOut},
		Cluster:  &kubectl.Cluster{Context: kubeContext, Namespace: env.Namespace, Progress: errOut},
		Source:   &gitsrc.Repo{Dir: repoDir, Folder: repoFolder(repoDir), Sources: buildSources(c), Log: out},
		CI:       &ghci.CI{Dir: repoDir},
		Secrets:  store,
	}
	_, _ = fmt.Fprintf(out, "Deploying %s to %s (namespace %s on %s)\n\n", c.Name, envName, env.Namespace, target.Cluster)
	return deploy.Run(cmd.Context(), o)
}

// repoFolder is the folder name the repo's checkout gets in a build
// workspace: its repository's name, else its folder's.
func repoFolder(dir string) string {
	if r := gitHubRepo(dir); r != "" {
		return r[strings.LastIndex(r, "/")+1:]
	}
	return filepath.Base(dir)
}

func loadRepoConfig(repoDir, override string) (*config.RepoConfig, error) {
	if override == "" {
		return config.LoadRepoConfig(repoDir)
	}
	data, err := os.ReadFile(override) //nolint:gosec // the file the user named
	if err != nil {
		return nil, err
	}
	c, err := config.ParseRepoConfig(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", override, err)
	}
	return c, nil
}

func pickEnv(e *config.RepoConfig, name string) (string, config.Environment, error) {
	names := make([]string, 0, len(e.Environments))
	for n := range e.Environments {
		names = append(names, n)
	}
	sort.Strings(names)
	if name == "" {
		if len(names) != 1 {
			return "", config.Environment{}, fmt.Errorf("%s has environments %s: name one with --env", e.Name, strings.Join(names, ", "))
		}
		name = names[0]
	}
	env, ok := e.Environments[name]
	if !ok {
		return "", config.Environment{}, fmt.Errorf("%s has no environment %q (it has %s)", e.Name, name, strings.Join(names, ", "))
	}
	return name, env, nil
}

// needsStore reports whether the deploy may read the secret store: some
// workload uses secrets, or an image takes build arguments from one.
func needsStore(c *config.RepoConfig, names []string) bool {
	for _, img := range c.Images {
		if img.BuildArgs != nil && img.BuildArgs.FromSecret != "" {
			return true
		}
	}
	for _, w := range c.Workloads {
		chosen := len(names) == 0 && !w.Manual && !w.Disabled
		for _, n := range names {
			chosen = chosen || n == w.Name
		}
		if chosen && len(w.Secrets) > 0 {
			return true
		}
	}
	return false
}

// buildSources are the sources of every image, for the build workspace.
func buildSources(c *config.RepoConfig) []config.BuildSource {
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
