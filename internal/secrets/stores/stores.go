// Package stores opens the secret store a deployment's config names. It is
// the one place that knows every adapter; core code receives a secrets.Store.
package stores

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BrutalSystems/dray/internal/secrets"
	"github.com/BrutalSystems/dray/internal/secrets/file"
	"github.com/BrutalSystems/dray/internal/secrets/openbao"
)

// Config chooses and configures a secret store.
type Config struct {
	// Type is the adapter: openbao or file.
	Type    string         `json:"type"`
	OpenBao *OpenBaoConfig `json:"openbao,omitempty"`
	File    *FileConfig    `json:"file,omitempty"`
}

// OpenBaoConfig configures the openbao adapter. The token comes from
// BAO_TOKEN, VAULT_TOKEN or ~/.vault-token, never from this file.
type OpenBaoConfig struct {
	// Address defaults to BAO_ADDR, then VAULT_ADDR.
	Address   string `json:"address,omitempty"`
	Mount     string `json:"mount,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

// FileConfig configures the file adapter.
type FileConfig struct {
	// Dir holds the secret files; a leading ~/ is the home folder.
	Dir string `json:"dir"`
}

// Open returns the configured store.
func Open(c Config) (secrets.Store, error) {
	switch c.Type {
	case "openbao":
		oc := OpenBaoConfig{}
		if c.OpenBao != nil {
			oc = *c.OpenBao
		}
		if oc.Address == "" {
			oc.Address = openbao.EnvAddress()
		}
		return openbao.New(openbao.Config{
			Address: oc.Address, Mount: oc.Mount, Prefix: oc.Prefix, Namespace: oc.Namespace,
			Token: openbao.EnvToken,
		})
	case "file":
		if c.File == nil || c.File.Dir == "" {
			return nil, fmt.Errorf("secret store: file needs file.dir")
		}
		dir, err := expandHome(c.File.Dir)
		if err != nil {
			return nil, err
		}
		return file.Open(dir)
	case "":
		return nil, fmt.Errorf("no secret store configured")
	}
	return nil, fmt.Errorf("secret store: unknown type %q (want openbao or file)", c.Type)
}

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
