// Package render fills a workload's manifests with the images it runs.
//
// Manifests are the repo's own files. Each image appears in them as a
// placeholder such as ${API_IMAGE}; rendering replaces it with the image's
// reference, by digest for a real deploy. Nothing else in the file changes, so
// a manifest renders the same way the JS tool rendered it.
package render

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/BrutalSystems/dray/internal/config"
)

// Manifest is one rendered manifest file.
type Manifest struct {
	// Path is the file's path relative to the repo root, as the config lists it.
	Path string
	// Content is the rendered file.
	Content []byte
}

// Result is a rendered workload.
type Result struct {
	Manifests []Manifest
	// Vars are the placeholders filled, by name.
	Vars map[string]string
	// Unfilled lists placeholders that look like images (NAME_IMAGE) but had
	// no value, as "path: ${NAME}". They reach the cluster as written.
	Unfilled []string
}

var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// ImageVar is the placeholder name for an image: templateVar when set,
// otherwise the image's name in upper snake case plus _IMAGE.
func ImageVar(name, templateVar string) string {
	if templateVar != "" {
		return templateVar
	}
	return strings.ToUpper(nonAlnum.ReplaceAllString(name, "_")) + "_IMAGE"
}

// Vars maps each placeholder a workload uses to its image reference. refs
// holds a reference per image name (built here or a component's local name).
func Vars(c *config.RepoConfig, w config.Workload, refs map[string]string) (map[string]string, error) {
	vars := map[string]string{}
	var missing []string
	for _, name := range w.Images {
		v, ok := placeholder(c, name)
		if !ok {
			return nil, fmt.Errorf("workload %s: unknown image %q", w.Name, name)
		}
		ref := refs[name]
		if ref == "" {
			missing = append(missing, name)
			continue
		}
		vars[v] = ref
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("workload %s: no image reference for %s", w.Name, strings.Join(missing, ", "))
	}
	return vars, nil
}

func placeholder(c *config.RepoConfig, name string) (string, bool) {
	for _, img := range c.Images {
		if img.Name == name {
			return ImageVar(img.Name, img.TemplateVar), true
		}
	}
	for _, u := range c.Uses {
		if u.LocalName() == name {
			return ImageVar(u.LocalName(), u.TemplateVar), true
		}
	}
	return "", false
}

var placeholderRE = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// String replaces each ${NAME} that has a value in vars. Placeholders without
// one are left as written, as the JS tool did; manifests may hold shell
// scripts with variables of their own.
func String(text []byte, vars map[string]string) []byte {
	return placeholderRE.ReplaceAllFunc(text, func(m []byte) []byte {
		if v, ok := vars[string(m[2:len(m)-1])]; ok {
			return []byte(v)
		}
		return m
	})
}

// Workload renders a workload's manifests from the repo at repoDir. Files are
// read through an os.Root, so a manifest path or symlink cannot reach outside
// the repo. Every rendered file must parse as YAML.
func Workload(repoDir string, c *config.RepoConfig, w config.Workload, refs map[string]string) (*Result, error) {
	vars, err := Vars(c, w, refs)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(repoDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()

	res := &Result{Vars: vars}
	for _, p := range w.Manifests {
		data, err := readFile(root, p)
		if err != nil {
			return nil, fmt.Errorf("workload %s: %w", w.Name, err)
		}
		out := String(data, vars)
		if err := checkYAML(out); err != nil {
			return nil, fmt.Errorf("workload %s: %s: %w", w.Name, p, err)
		}
		for _, name := range unfilledImages(out) {
			res.Unfilled = append(res.Unfilled, p+": ${"+name+"}")
		}
		res.Manifests = append(res.Manifests, Manifest{Path: p, Content: out})
	}
	return res, nil
}

func readFile(root *os.Root, p string) ([]byte, error) {
	f, err := root.Open(filepath.FromSlash(p))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

// checkYAML parses every document in a file.
func checkYAML(data []byte) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for i := 0; ; i++ {
		var n yaml.Node
		err := dec.Decode(&n)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("document %d does not parse: %w", i+1, err)
		}
	}
}

func unfilledImages(data []byte) []string {
	seen := map[string]bool{}
	for _, m := range placeholderRE.FindAllSubmatch(data, -1) {
		if name := string(m[1]); strings.HasSuffix(name, "_IMAGE") {
			seen[name] = true
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// IsDigestRef reports whether ref names an image by digest.
func IsDigestRef(ref string) bool {
	_, digest, ok := strings.Cut(ref, "@sha256:")
	if !ok || len(digest) != 64 {
		return false
	}
	for _, r := range digest {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// Write writes rendered manifests as one multi-document stream, each file
// headed by a comment naming its source.
func Write(w io.Writer, ms []Manifest) error {
	for i, m := range ms {
		sep := ""
		if i > 0 {
			sep = "---\n"
		}
		body := m.Content
		if len(body) > 0 && body[len(body)-1] != '\n' {
			body = append(body, '\n')
		}
		if _, err := fmt.Fprintf(w, "%s# Source: %s\n%s", sep, m.Path, body); err != nil {
			return err
		}
	}
	return nil
}
