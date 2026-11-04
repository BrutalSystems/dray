package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

// RepoConfigPath is where a repo keeps its config, relative to the repo root.
const RepoConfigPath = ".dray/config.yaml"

// ParseRepoConfig reads .dray/config.yaml content strictly: unknown keys,
// duplicate keys and wrong types are errors, so a typo or a "false" string
// where a boolean belongs never silently changes what gets deployed.
func ParseRepoConfig(data []byte) (*RepoConfig, error) {
	var c RepoConfig
	if err := strictUnmarshal(data, &c); err != nil {
		return nil, err
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// LoadRepoConfig reads and checks a repo's .dray/config.yaml.
func LoadRepoConfig(repoDir string) (*RepoConfig, error) {
	path := filepath.Join(repoDir, RepoConfigPath)
	data, err := os.ReadFile(path) //nolint:gosec // the path is the repo the user named
	if err != nil {
		return nil, err
	}
	c, err := ParseRepoConfig(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// ParseRegistryEntry reads a registry entry strictly and checks it.
func ParseRegistryEntry(data []byte) (*RegistryEntry, error) {
	var e RegistryEntry
	if err := strictUnmarshal(data, &e); err != nil {
		return nil, err
	}
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return &e, nil
}

func strictUnmarshal(data []byte, v any) error {
	if strings.TrimSpace(string(data)) == "" {
		return errors.New("the file is empty")
	}
	if err := yaml.UnmarshalStrict(data, v); err != nil {
		return fmt.Errorf("cannot read config: %w", err)
	}
	return nil
}

// ParseTarget reads a target strictly and checks it.
func ParseTarget(data []byte) (*Target, error) {
	var t Target
	if err := strictUnmarshal(data, &t); err != nil {
		return nil, err
	}
	if err := t.Validate(); err != nil {
		return nil, err
	}
	return &t, nil
}
