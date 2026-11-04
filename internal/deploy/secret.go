package deploy

import (
	"encoding/base64"

	"sigs.k8s.io/yaml"

	"github.com/BrutalSystems/dray/internal/secrets"
)

// secretManifest is a Kubernetes Secret holding v. The namespace comes from
// the cluster adapter.
func secretManifest(name string, v secrets.Values) ([]byte, error) {
	data := map[string]string{}
	for k, val := range v {
		data[k] = base64.StdEncoding.EncodeToString([]byte(val))
	}
	return yaml.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   map[string]any{"name": name},
		"type":       "Opaque",
		"data":       data,
	})
}
