package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"

	"github.com/BrutalSystems/dray/internal/config/legacy"
	"github.com/BrutalSystems/dray/internal/secrets"
	"github.com/BrutalSystems/dray/internal/secrets/sops"
	"github.com/BrutalSystems/dray/internal/secrets/stores"
)

// UserConfig is the CLI's own settings file: deployment settings that never
// belong in a repo, such as which secret store to use.
type UserConfig struct {
	SecretStore stores.Config `json:"secret_store"`
	// Registry is the folder holding registry entries (<app>.yaml) and
	// targets (targets/<name>.yaml); default "registry" beside this file.
	Registry string `json:"registry,omitempty"`
}

// UserConfigPath is $DRAY_CONFIG, else $XDG_CONFIG_HOME/dray/config.yaml,
// else ~/.config/dray/config.yaml.
func UserConfigPath() string {
	if p := os.Getenv("DRAY_CONFIG"); p != "" {
		return p
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "dray", "config.yaml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "dray", "config.yaml")
	}
	return filepath.Join(home, ".config", "dray", "config.yaml")
}

const userConfigExample = `secret_store:
  type: openbao          # or file
  openbao:
    address: https://bao.example.com   # default: BAO_ADDR, then VAULT_ADDR
    mount: kv
    layout: "{client}/{app}/{env}/{name}"   # default: {app}/{name}
    clients:
      shop: acme
  # file:
  #   dir: ~/.local/share/dray/secrets
registry: ~/.config/dray/registry       # <app>.yaml and targets/<name>.yaml`

// loadUserConfig reads Dray's config file and returns it with its path.
func loadUserConfig(path string) (*UserConfig, string, error) {
	if path == "" {
		path = UserConfigPath()
	}
	data, err := os.ReadFile(path) //nolint:gosec // the user's own config file
	if errors.Is(err, fs.ErrNotExist) {
		return nil, path, fmt.Errorf("no Dray config: create %s, for example:\n\n%s", path, userConfigExample)
	}
	if err != nil {
		return nil, path, err
	}
	var c UserConfig
	if err := yaml.UnmarshalStrict(data, &c); err != nil {
		return nil, path, fmt.Errorf("%s: %w", path, err)
	}
	return &c, path, nil
}

func openStore(path, env string) (secrets.Store, error) {
	c, path, err := loadUserConfig(path)
	if err != nil {
		return nil, err
	}
	s, err := stores.Open(c.SecretStore, env)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// expandHome turns a leading ~/ into the home folder.
func expandHome(p string) (string, error) {
	rest, ok := strings.CutPrefix(p, "~/")
	if !ok {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, rest), nil
}

// NewSecretsCmd is `dray secrets`: the secret store, seen by path and key
// name. No command prints a value.
func NewSecretsCmd() *cobra.Command {
	var cfgPath, env string
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Look at and import secrets in the configured store",
		Long: "Look at and import secrets in the secret store set in Dray's config file\n" +
			"(" + "$DRAY_CONFIG, else ~/.config/dray/config.yaml). Secrets are shown by path and\n" +
			"key name; no command prints a value.",
	}
	cmd.PersistentFlags().StringVar(&cfgPath, "config", "", "Dray's config file (default $DRAY_CONFIG or ~/.config/dray/config.yaml)")
	cmd.PersistentFlags().StringVar(&env, "env", "", "environment, for stores that keep secrets per environment (dev, staging, prod)")

	list := &cobra.Command{
		Use:   "list [prefix]",
		Short: "List secret paths, such as shop/app",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore(cfgPath, env)
			if err != nil {
				return err
			}
			prefix := ""
			if len(args) == 1 {
				prefix = args[0]
			}
			paths, err := s.List(cmd.Context(), prefix)
			if err != nil {
				return err
			}
			for _, p := range paths {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), p)
			}
			return nil
		},
	}
	keys := &cobra.Command{
		Use:   "keys <path>",
		Short: "List a secret's key names",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore(cfgPath, env)
			if err != nil {
				return err
			}
			v, err := s.Get(cmd.Context(), strings.TrimPrefix(args[0], "secret:"))
			if err != nil {
				return err
			}
			for _, k := range v.Keys() {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), k)
			}
			return nil
		},
	}
	cmd.AddCommand(list, keys, newSecretsImportCmd(&cfgPath, &env))
	return cmd
}

var legacyFormats = map[string]sops.Format{
	"sops-manifest":    sops.Manifest,
	"literal-from-env": sops.EnvFile,
	"sops-exec-env":    sops.ExecEnv,
	"sops-dotenv":      sops.Dotenv,
}

func newSecretsImportCmd(cfgPath, env *string) *cobra.Command {
	var write, overwrite bool
	cmd := &cobra.Command{
		Use:   "import [repo]",
		Short: "Copy a repo's secrets from the JS tool's files into the store",
		Long: "Read every secret a repo's .dray/config.json uses (sops-encrypted manifests and\n" +
			"env files, plain env files) and copy each to the path its migrated config\n" +
			"references, such as shop/app. Shows each secret's key names and whether the\n" +
			"store already holds it; nothing is written unless you pass --write. A secret\n" +
			"the store holds with other values is kept unless you pass --overwrite.",
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
			src := &sops.Store{Dir: dir, Sources: map[string]sops.Source{}}
			for _, s := range legacy.SecretSources(old) {
				f, ok := legacyFormats[s.Kind]
				if !ok {
					return fmt.Errorf("%s: unknown secret kind %q", s.Path, s.Kind)
				}
				src.Sources[s.Path] = sops.Source{File: s.File, Format: f, SecretName: s.SecretName, Prefix: s.Prefix}
			}
			out := cmd.OutOrStdout()
			if len(src.Sources) == 0 {
				_, err := fmt.Fprintf(out, "%s uses no secrets\n", old.Name)
				return err
			}
			target, err := openStore(*cfgPath, *env)
			if err != nil {
				return err
			}
			res, err := importSecrets(cmd, src, target, write, overwrite)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "\n%s\n", res)
			if !write {
				_, _ = fmt.Fprintln(out, "Dry run: nothing written. Pass --write to import.")
			}
			if res.failed > 0 {
				return fmt.Errorf("%d secret(s) could not be read", res.failed)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&write, "write", false, "write to the store")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "replace secrets the store holds with other values")
	return cmd
}

type importResult struct {
	written, same, kept, failed, pending int
	target                               string
}

func (r importResult) String() string {
	return fmt.Sprintf("Store %s: %d written, %d to write, %d already the same, %d kept (differ), %d failed",
		r.target, r.written, r.pending, r.same, r.kept, r.failed)
}

func importSecrets(cmd *cobra.Command, src *sops.Store, target secrets.Store, write, overwrite bool) (importResult, error) {
	ctx, out := cmd.Context(), cmd.OutOrStdout()
	res := importResult{target: target.String()}
	paths := make([]string, 0, len(src.Sources))
	for p := range src.Sources {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		s := src.Sources[p]
		_, _ = fmt.Fprintf(out, "%s  <- %s (%s)\n", p, s.File, s.Format)
		v, err := src.Get(ctx, p)
		if err != nil {
			res.failed++
			_, _ = fmt.Fprintf(out, "    cannot read: %v\n", err)
			continue
		}
		_, _ = fmt.Fprintf(out, "    keys: %s\n", strings.Join(v.Keys(), ", "))
		if w, ok := target.(interface{ Where(string) (string, error) }); ok {
			loc, err := w.Where(p)
			if err != nil {
				return res, err
			}
			_, _ = fmt.Fprintf(out, "    to:   %s\n", loc)
		}
		have, err := target.Get(ctx, p)
		var status string
		switch {
		case errors.Is(err, secrets.ErrNotFound):
			status = "new"
		case err != nil:
			return res, err
		case have.Equal(v):
			res.same++
			_, _ = fmt.Fprintln(out, "    already in the store with the same values")
			continue
		default:
			status = "differs"
		}
		if status == "differs" && !overwrite {
			res.kept++
			_, _ = fmt.Fprintf(out, "    the store holds other values (keys: %s); kept (--overwrite replaces it)\n", strings.Join(have.Keys(), ", "))
			continue
		}
		if !write {
			res.pending++
			_, _ = fmt.Fprintf(out, "    would write (%s)\n", status)
			continue
		}
		if err := target.Put(ctx, p, v); err != nil {
			return res, err
		}
		res.written++
		_, _ = fmt.Fprintf(out, "    written (%s)\n", status)
	}
	return res, nil
}
