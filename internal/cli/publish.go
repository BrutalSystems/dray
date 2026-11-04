package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/BrutalSystems/dray/internal/config"
	"github.com/BrutalSystems/dray/internal/deploy/gitsrc"
	"github.com/BrutalSystems/dray/internal/execx"
	"github.com/BrutalSystems/dray/internal/pilet"
	"github.com/BrutalSystems/dray/internal/secrets"
)

type publishFlags struct {
	cfgPath, env, repoConfig string
	pilets                   []string
	dryRun, allowDirty       bool
}

// NewPublishCmd is `dray publish`: publish a repo's pilets to a feed.
func NewPublishCmd() *cobra.Command {
	var f publishFlags
	cmd := &cobra.Command{
		Use:   "publish [repo]",
		Short: "Publish a repo's pilets to a feed",
		Long: "Publish a repo's pilets (default the current folder) to the feed of one of its\n" +
			"release: feed environments. Each pilet is built with the repo's build, packed\n" +
			"with the pilet CLI and uploaded with the feed's key from the secret store.\n\n" +
			"With the feed's version: commit (the default), a pilet at 1.2.0 is published as\n" +
			"1.2.0-<env>.<commit>, so every commit is a new version; with version: package\n" +
			"it is published as package.json says.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return runPublish(cmd, dir, f)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.cfgPath, "config", "", "your own settings file (default $DRAY_CONFIG or ~/.config/dray/config.yaml)")
	fl.StringVar(&f.env, "env", "", "feed environment to publish to (default: the app's only one)")
	fl.StringVar(&f.repoConfig, "repo-config", "", "read the repo's config from this file instead of .dray/config.yaml")
	fl.StringArrayVar(&f.pilets, "pilet", nil, "publish only this pilet (repeatable)")
	fl.BoolVar(&f.dryRun, "dry-run", false, "build and pack, but upload nothing")
	fl.BoolVar(&f.allowDirty, "allow-dirty", false, "publish uncommitted changes (only where the environment sets allow_dirty)")
	return cmd
}

func runPublish(cmd *cobra.Command, dir string, f publishFlags) error {
	repoDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	c, err := loadRepoConfig(repoDir, f.repoConfig)
	if err != nil {
		return err
	}
	if c.Pilets == nil {
		return fmt.Errorf("%s publishes no pilets", c.Name)
	}
	envName, env, err := pickFeedEnv(c, f.env)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()

	commit, dirty, err := (&gitsrc.Repo{Dir: repoDir}).Commit(cmd.Context())
	if err != nil {
		return err
	}
	if dirty && (!f.allowDirty || !env.AllowDirty) {
		return fmt.Errorf("the working tree has uncommitted changes; commit them (or pass --allow-dirty to an environment that sets allow_dirty)")
	}
	suffix := ""
	if env.Feed.Version != "package" {
		suffix = envName + "." + commit
		if dirty {
			suffix += ".dirty." + time.Now().UTC().Format("20060102150405")
		}
	} else if dirty {
		return fmt.Errorf("environment %s publishes package.json versions; uncommitted changes cannot be published there", envName)
	}

	if len(c.Pilets.Command) > 0 {
		if f.dryRun {
			return fmt.Errorf("%s publishes with its own command, which has no dry run", c.Name)
		}
		store, err := openStore(repoDir, f.cfgPath, envName)
		if err != nil {
			return err
		}
		return publishWithCommand(cmd, repoDir, c, f.pilets, store)
	}
	var apiKey string
	if !f.dryRun {
		store, err := openStore(repoDir, f.cfgPath, envName)
		if err != nil {
			return err
		}
		key, err := secrets.Lookup(cmd.Context(), store, env.Feed.APIKey)
		if err != nil {
			return fmt.Errorf("feed key %s: %w", env.Feed.APIKey, err)
		}
		for _, v := range key {
			apiKey = v
		}
	}
	_, _ = fmt.Fprintf(out, "Publishing %s pilets to %s (%s)\n", c.Name, envName, env.Feed.URL)
	res, err := pilet.Publish(cmd.Context(), pilet.Options{
		RepoDir: repoDir, Pilets: *c.Pilets, Names: f.pilets,
		FeedURL: env.Feed.URL, APIKey: apiKey, Suffix: suffix, DryRun: f.dryRun,
		Out: out, Progress: cmd.ErrOrStderr(),
	})
	if err != nil {
		return err
	}
	if f.dryRun {
		_, _ = fmt.Fprintf(out, "\nDry run: %d pilet(s) built and packed, nothing uploaded.\n", len(res))
	}
	return nil
}

// pickFeedEnv picks a release: feed environment.
func pickFeedEnv(c *config.RepoConfig, name string) (string, config.Environment, error) {
	var feeds []string
	for n, e := range c.Environments {
		if e.Release == "feed" {
			feeds = append(feeds, n)
		}
	}
	sort.Strings(feeds)
	if name == "" {
		if len(feeds) != 1 {
			if len(feeds) == 0 {
				return "", config.Environment{}, fmt.Errorf("%s has no release: feed environment to publish pilets to", c.Name)
			}
			return "", config.Environment{}, fmt.Errorf("%s publishes pilets to %s: name one with --env", c.Name, strings.Join(feeds, ", "))
		}
		name = feeds[0]
	}
	env, ok := c.Environments[name]
	if !ok || env.Release != "feed" {
		return "", config.Environment{}, fmt.Errorf("%s has no feed environment %q (it has %s)", c.Name, name, strings.Join(feeds, ", "))
	}
	return name, env, nil
}

// publishWithCommand is the escape hatch: the repo's own publish command, run
// once per pilet with Env's values in its environment.
func publishWithCommand(cmd *cobra.Command, repoDir string, c *config.RepoConfig, names []string, store secrets.Store) error {
	p := c.Pilets
	if len(names) == 0 {
		names = p.Names
	}
	var env []string
	if p.Env != "" {
		v, err := secrets.Lookup(cmd.Context(), store, p.Env)
		if err != nil {
			return fmt.Errorf("pilets.env %s: %w", p.Env, err)
		}
		for k, val := range v {
			env = append(env, k+"="+val)
		}
	}
	for _, n := range names {
		args := make([]string, len(p.Command))
		for i, a := range p.Command {
			args[i] = strings.ReplaceAll(a, "{name}", n)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\n── %s: %s\n", n, strings.Join(args, " "))
		if _, err := execx.Run(cmd.Context(), execx.Cmd{Name: args[0], Args: args[1:], Dir: repoDir, Env: env, Stream: os.Stderr}); err != nil {
			return fmt.Errorf("pilet %s: %w", n, err)
		}
	}
	return nil
}
