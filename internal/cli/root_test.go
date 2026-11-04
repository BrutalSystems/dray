package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) string {
	t.Helper()
	root := NewRoot("dray", "test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("dray %s: %v", strings.Join(args, " "), err)
	}
	return out.String()
}

func TestVersionCommand(t *testing.T) {
	if got := run(t, "version"); !strings.HasPrefix(got, "dray ") {
		t.Fatalf("version output = %q, want it to start with %q", got, "dray ")
	}
}

func TestVersionFlag(t *testing.T) {
	if got := run(t, "--version"); !strings.HasPrefix(got, "dray ") {
		t.Fatalf("--version output = %q, want it to start with %q", got, "dray ")
	}
}

func TestVersionJSON(t *testing.T) {
	var info map[string]any
	if err := json.Unmarshal([]byte(run(t, "version", "--json")), &info); err != nil {
		t.Fatalf("version --json is not JSON: %v", err)
	}
	for _, k := range []string{"version", "go", "os", "arch"} {
		if _, ok := info[k]; !ok {
			t.Errorf("version --json is missing %q", k)
		}
	}
}

func TestUnknownArgsRejected(t *testing.T) {
	root := NewRoot("dray", "test")
	root.SetArgs([]string{"version", "extra"})
	root.SetOut(&bytes.Buffer{})
	if err := root.Execute(); err == nil {
		t.Fatal("dray version extra: want an error, got none")
	}
}
