package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"

	"github.com/BrutalSystems/dray/internal/config"
	"github.com/BrutalSystems/dray/internal/secrets"
	"github.com/BrutalSystems/dray/internal/secrets/stores"
)

// UserConfig is a person's own Dray settings: things that differ from one
// person to the next and never belong in a repo. Everything about the app
// itself is in its .dray/config.yaml. The file is optional.
type UserConfig struct {
	// AWSProfiles maps an AWS account ID to the local profile to use for it.
	// Without one, the AWS CLI's usual credentials are used.
	AWSProfiles map[string]string `json:"aws_profiles,omitempty"`
	// KubeContexts maps a target's cluster to a kube context, when the
	// context is not named after the cluster.
	KubeContexts map[string]string `json:"kube_contexts,omitempty"`
	// SecretStore is used by dray secrets for a repo whose config names no
	// store.
	SecretStore *stores.Config `json:"secret_store,omitempty"`
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

// loadUserConfig reads a person's settings; a missing file is no settings.
func loadUserConfig(path string) (*UserConfig, string, error) {
	if path == "" {
		path = UserConfigPath()
	}
	data, err := os.ReadFile(path) //nolint:gosec // the user's own settings file
	if errors.Is(err, fs.ErrNotExist) {
		return &UserConfig{}, path, nil
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

// repoStore is the store a repo config names, for the adapters.
func repoStore(c *config.RepoConfig) stores.Config {
	st := c.SecretStore
	out := stores.Config{Type: st.Type}
	if o := st.OpenBao; o != nil {
		out.OpenBao = &stores.OpenBaoConfig{Address: o.Address, Mount: o.Mount, Layout: o.Layout, Namespace: o.Namespace}
		if o.Client != "" {
			out.OpenBao.Clients = map[string]string{c.Name: o.Client}
		}
	}
	if f := st.File; f != nil {
		out.File = &stores.FileConfig{Dir: f.Dir}
	}
	return out
}

// openStore opens the secret store for the repo at dir: the one its
// .dray/config.yaml names, else the one in the person's settings.
func openStore(dir, settingsPath, env string) (secrets.Store, error) {
	c, err := config.LoadRepoConfig(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if c != nil && c.SecretStore != nil {
		s, err := stores.Open(repoStore(c), env)
		if err != nil {
			return nil, fmt.Errorf("%s secret_store: %w", filepath.Join(dir, config.RepoConfigPath), err)
		}
		return s, nil
	}
	u, path, err := loadUserConfig(settingsPath)
	if err != nil {
		return nil, err
	}
	if u.SecretStore == nil {
		return nil, fmt.Errorf("no secret store: set secret_store in %s", filepath.Join(dir, config.RepoConfigPath))
	}
	s, err := stores.Open(*u.SecretStore, env)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}
