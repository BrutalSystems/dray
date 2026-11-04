package legacy

import (
	"encoding/json"
	"os"
	"testing"
)

// readRegistry returns each repo's cached config from a JS-tool registry.json.
func readRegistry(t *testing.T, path string) map[string][]byte {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // path given by the developer running the test
	if err != nil {
		t.Fatal(err)
	}
	var reg map[string]struct {
		Config json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(data, &reg); err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for name, e := range reg {
		out[name] = e.Config
	}
	return out
}
