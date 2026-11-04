// Package legacy reads the JS dray's .dray/config.json and converts it to
// Dray v2's .dray/config.yaml.
package legacy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Defaults are the per-machine, per-repo and per-workload settings the JS tool
// merged: credentials profile, account, region, build platform, kube context,
// namespace and the CI gate.
type Defaults struct {
	Profile   string `json:"profile,omitempty"`
	Region    string `json:"region,omitempty"`
	Account   string `json:"account,omitempty"`
	Platform  string `json:"platform,omitempty"`
	Context   string `json:"context,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	CIGate    *bool  `json:"ciGate,omitempty"`
}

// merge returns d with any field set in o taking its place.
func (d Defaults) merge(o Defaults) Defaults {
	pick := func(a, b string) string {
		if b != "" {
			return b
		}
		return a
	}
	out := Defaults{
		Profile: pick(d.Profile, o.Profile), Region: pick(d.Region, o.Region),
		Account: pick(d.Account, o.Account), Platform: pick(d.Platform, o.Platform),
		Context: pick(d.Context, o.Context), Namespace: pick(d.Namespace, o.Namespace),
		CIGate: d.CIGate,
	}
	if o.CIGate != nil {
		out.CIGate = o.CIGate
	}
	return out
}

// Config is the JS tool's .dray/config.json.
type Config struct {
	Name      string     `json:"name"`
	Defaults  *Defaults  `json:"defaults,omitempty"`
	Images    []Image    `json:"images,omitempty"`
	Workloads []Workload `json:"workloads,omitempty"`
	Secrets   []Secret   `json:"secrets,omitempty"`
	Pilets    *Pilets    `json:"pilets,omitempty"`

	// Notes are the "_note"-style keys the JS configs used as comments, by key.
	Notes map[string]string `json:"-"`
}

// Image is an image the JS tool built.
type Image struct {
	Name        string     `json:"name"`
	ECR         string     `json:"ecr"`
	Source      Source     `json:"source"`
	Dockerfile  string     `json:"dockerfile,omitempty"`
	Context     string     `json:"context,omitempty"`
	TemplateVar string     `json:"templateVar,omitempty"`
	Latest      bool       `json:"latest,omitempty"`
	DepsImage   *DepsImage `json:"depsImage,omitempty"`
	BuildArgs   *BuildArgs `json:"buildArgs,omitempty"`
	Notes       []string   `json:"-"`
}

// Source is where an image's code comes from: this repo, or another one.
type Source struct {
	Local bool   `json:"local,omitempty"`
	Git   string `json:"git,omitempty"`
	Ref   string `json:"ref,omitempty"`
}

// DepsImage is the JS tool's cached dependencies image.
type DepsImage struct {
	Dockerfile string   `json:"dockerfile"`
	Tag        string   `json:"tag"`
	RebuildOn  []string `json:"rebuildOn,omitempty"`
}

// BuildArgs are the JS tool's build arguments.
type BuildArgs struct {
	EnvFile     string            `json:"envFile,omitempty"`
	SopsEnvFile string            `json:"sopsEnvFile,omitempty"`
	Prefix      string            `json:"prefix,omitempty"`
	Values      map[string]string `json:"values,omitempty"`
}

// Workload is a JS-tool workload. Its embedded Defaults are per-workload
// overrides of the repo's defaults.
type Workload struct {
	Name                  string   `json:"name"`
	Kind                  string   `json:"kind"`
	Image                 string   `json:"image,omitempty"`
	Manifests             []string `json:"manifests"`
	StampImages           []string `json:"stampImages,omitempty"`
	DependsOn             []string `json:"dependsOn,omitempty"`
	Disabled              bool     `json:"disabled,omitempty"`
	Manual                bool     `json:"manual,omitempty"`
	RolloutTimeoutSeconds int      `json:"rolloutTimeoutSeconds,omitempty"`
	Defaults
	Notes []string `json:"-"`
}

// Secret is a JS-tool secret: a sops-encrypted file applied to the cluster.
type Secret struct {
	Name      string            `json:"name"`
	Kind      string            `json:"kind"`
	File      string            `json:"file,omitempty"`
	Reflector map[string]string `json:"reflector,omitempty"`
	Notes     []string          `json:"-"`
}

// Pilets is the JS tool's pilet publishing config.
type Pilets struct {
	SecretsFile string   `json:"secretsFile,omitempty"`
	Command     []string `json:"command,omitempty"`
	Names       []string `json:"names,omitempty"`
}

// ParseMachine reads the JS tool's ~/.dray/config.json and returns its defaults.
func ParseMachine(data []byte) (Defaults, error) {
	var m struct {
		Defaults Defaults `json:"defaults"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return Defaults{}, fmt.Errorf("cannot read ~/.dray/config.json: %w", err)
	}
	return m.Defaults, nil
}

// Parse reads a JS-tool config strictly: any key it does not know is an
// error, so nothing in an old config is silently dropped. Keys starting with
// "_" were comments; they are kept as notes.
func Parse(data []byte) (*Config, error) {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("cannot read .dray/config.json: %w", err)
	}
	notes := map[string][]string{}
	cleaned := stripNotes(raw, "", notes)
	// "pilets": [] was what `dray init` wrote; it means no pilets.
	if m, ok := cleaned.(map[string]any); ok {
		if p, ok := m["pilets"].([]any); ok && len(p) == 0 {
			delete(m, "pilets")
		}
	}
	b, err := json.Marshal(cleaned)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("cannot read .dray/config.json: %w", err)
	}
	c.Notes = map[string]string{}
	for path, ns := range notes {
		switch {
		case strings.HasPrefix(path, "images[") && strings.Count(path, "/") == 0:
			if i := index(path); i >= 0 && i < len(c.Images) {
				c.Images[i].Notes = append(c.Images[i].Notes, ns...)
			}
		case strings.HasPrefix(path, "workloads[") && strings.Count(path, "/") == 0:
			if i := index(path); i >= 0 && i < len(c.Workloads) {
				c.Workloads[i].Notes = append(c.Workloads[i].Notes, ns...)
			}
		case strings.HasPrefix(path, "secrets[") && strings.Count(path, "/") == 0:
			if i := index(path); i >= 0 && i < len(c.Secrets) {
				c.Secrets[i].Notes = append(c.Secrets[i].Notes, ns...)
			}
		default:
			c.Notes[path] = strings.Join(ns, "\n")
		}
	}
	return &c, nil
}

// stripNotes removes "_"-prefixed keys, recording their text under the path
// of the object that held them ("" for the top level, "images[2]" for the
// third image, "images[2]/buildArgs" deeper down).
func stripNotes(v any, path string, notes map[string][]string) any {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := map[string]any{}
		for _, k := range keys {
			if strings.HasPrefix(k, "_") {
				key := path
				if path == "" {
					key = k
				}
				notes[key] = append(notes[key], fmt.Sprint(t[k]))
				continue
			}
			child := k
			if path != "" {
				child = path + "/" + k
			}
			out[k] = stripNotes(t[k], child, notes)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = stripNotes(e, fmt.Sprintf("%s[%d]", path, i), notes)
		}
		return out
	default:
		return v
	}
}

func index(path string) int {
	open, closeIdx := strings.Index(path, "["), strings.Index(path, "]")
	if open < 0 || closeIdx < open {
		return -1
	}
	var i int
	if _, err := fmt.Sscanf(path[open+1:closeIdx], "%d", &i); err != nil {
		return -1
	}
	return i
}
