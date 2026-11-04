package config

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
)

// SecretRef names a secret in the secret store without saying which store:
// "secret:<scope>/<name>", optionally "#<key>" for one value. The server's
// config maps it to a real path.
type SecretRef string

var secretRefPattern = regexp.MustCompile(`^secret:[a-z0-9][-a-z0-9_.]*(/[a-z0-9][-a-z0-9_.]*)+(#[A-Za-z_][A-Za-z0-9_.-]*)?$`)

// Validate reports whether the reference is well formed.
func (r SecretRef) Validate() error {
	if !secretRefPattern.MatchString(string(r)) {
		return fmt.Errorf("%q is not a secret reference; use secret:<scope>/<name>", string(r))
	}
	return nil
}

// JSONSchema describes SecretRef in the published schema.
func (SecretRef) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        "string",
		Pattern:     secretRefPattern.String(),
		Description: "A secret in the secret store: secret:<scope>/<name>, optionally #<key>.",
	}
}

// Duration is a time span written the Go way, such as "600s" or "10m".
type Duration struct{ time.Duration }

// MarshalText writes the duration as "10m0s".
func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// UnmarshalText reads "600s", "10m" and the like.
func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(strings.TrimSpace(string(b)))
	if err != nil {
		return fmt.Errorf("invalid duration %q: use a value such as 600s or 10m", string(b))
	}
	d.Duration = v
	return nil
}

// JSONSchema describes Duration in the published schema.
func (Duration) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        "string",
		Pattern:     `^([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$`,
		Description: "A duration such as 600s or 10m.",
	}
}
