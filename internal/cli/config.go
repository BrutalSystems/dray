package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BrutalSystems/dray/internal/config"
	"github.com/BrutalSystems/dray/internal/config/legacy"
	"github.com/BrutalSystems/dray/schema"
)

// NewConfigCmd is `dray config`: check, convert and describe config files.
func NewConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Check, convert and describe Dray's config files",
	}
	cmd.AddCommand(newConfigValidateCmd(), newConfigMigrateCmd(), newConfigSchemaCmd())
	return cmd
}

func newConfigValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate [repo]",
		Short: "Check a repo's .dray/config.yaml",
		Long: "Check a repo's .dray/config.yaml (path is the repo, default the current folder).\n" +
			"Unknown keys, wrong types and broken references are errors.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			if _, err := config.LoadRepoConfig(dir); err != nil {
				return err
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: ok\n", filepath.Join(dir, config.RepoConfigPath))
			return err
		},
	}
}

func newConfigMigrateCmd() *cobra.Command {
	var (
		write   bool
		env     string
		pins    []string
		machine string
	)
	cmd := &cobra.Command{
		Use:   "migrate [repo]",
		Short: "Convert a repo's .dray/config.json from the JS tool",
		Long: "Convert a repo's .dray/config.json (from the JS dray) to .dray/config.yaml,\n" +
			"with the app's environment and target filled in from the old defaults.\n" +
			"Everything the conversion changed or could not decide is listed as a warning.\n" +
			"Nothing is written unless you pass --write.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			data, err := os.ReadFile(filepath.Join(dir, ".dray", "config.json"))
			if err != nil {
				return err
			}
			old, err := legacy.Parse(data)
			if err != nil {
				return err
			}
			opts := legacy.Options{Environment: env}
			if machine == "" {
				if home, err := os.UserHomeDir(); err == nil {
					machine = filepath.Join(home, ".dray", "config.json")
				}
			}
			if d, err := readMachineDefaults(machine); err == nil {
				opts.Machine = d
			}
			pinned := map[string]string{}
			for _, p := range pins {
				name, commit, ok := strings.Cut(p, "=")
				if !ok {
					return fmt.Errorf("--pin %q: use image=commit", p)
				}
				pinned[name] = commit
			}
			opts.RunningCommit = func(image string) string { return pinned[image] }
			opts.BuildSources = siblingSources(dir)

			res := legacy.Convert(old, opts)
			out, err := config.EncodeYAML(res.Config, &res.Comments)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			valid := res.Config.Validate()
			if write {
				if valid != nil {
					return fmt.Errorf("not written, the converted config does not validate yet:\n%w", valid)
				}
				path := filepath.Join(dir, config.RepoConfigPath)
				if err := os.WriteFile(path, out, 0o600); err != nil { //nolint:gosec // writing into the repo the user named is the point
					return err
				}
				_, _ = fmt.Fprintf(w, "wrote %s\n\n", path)
			} else {
				_, _ = fmt.Fprintf(w, "# --- %s\n%s\n", config.RepoConfigPath, out)
			}
			ew := cmd.ErrOrStderr()
			if len(res.Warnings) > 0 {
				_, _ = fmt.Fprintf(ew, "%d thing(s) to check:\n", len(res.Warnings))
				for _, wn := range res.Warnings {
					_, _ = fmt.Fprintf(ew, "  - %s\n", wn)
				}
			}
			if valid != nil && !write {
				_, _ = fmt.Fprintf(ew, "\nThe converted config does not validate yet:\n%v\n", valid)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&write, "write", false, "write .dray/config.yaml into the repo")
	cmd.Flags().StringVar(&env, "env", "prod", "name of the environment the app gets")
	cmd.Flags().StringArrayVar(&pins, "pin", nil, "pin an image built from another repo to a commit: image=commit (repeatable)")
	cmd.Flags().StringVar(&machine, "machine-config", "", "the JS tool's machine config (default ~/.dray/config.json)")
	return cmd
}

func newConfigSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Print the JSON Schema for .dray/config.yaml",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := cmd.OutOrStdout().Write(schema.Config)
			return err
		},
	}
}

func readMachineDefaults(path string) (legacy.Defaults, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the JS tool's own config file
	if err != nil {
		return legacy.Defaults{}, err
	}
	return legacy.ParseMachine(data)
}

// gitHubRepo returns owner/name from the repo's origin remote, or "".
func gitHubRepo(dir string) string {
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output() //nolint:gosec // fixed git arguments, no shell
	if err != nil {
		return ""
	}
	u := strings.TrimSuffix(strings.TrimSpace(string(out)), ".git")
	for _, prefix := range []string{"git@github.com:", "https://github.com/", "ssh://git@github.com/"} {
		if rest, ok := strings.CutPrefix(u, prefix); ok && strings.Count(rest, "/") == 1 {
			return rest
		}
	}
	return ""
}
