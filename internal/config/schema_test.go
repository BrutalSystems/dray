package config

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files and published schemas")

func TestSchemasUpToDate(t *testing.T) {
	cases := map[string]func(string) ([]byte, error){
		"config.schema.json":   RepoConfigSchema,
		"registry.schema.json": RegistryEntrySchema,
	}
	for file, gen := range cases {
		t.Run(file, func(t *testing.T) {
			got, err := gen(".")
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("..", "..", "schema", file)
			if *update {
				if err := os.WriteFile(path, got, 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path) //nolint:gosec // fixed path in the repo
			if err != nil {
				t.Fatalf("%v (run: go test ./internal/config -run TestSchemasUpToDate -update)", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("schema/%s is out of date; run: go test ./internal/config -run TestSchemasUpToDate -update", file)
			}
		})
	}
}
