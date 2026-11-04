// Package sops is a read-only secret store over the files the JS tool used:
// sops-encrypted Kubernetes Secret manifests and env files, and plain env
// files. Its job is migration: `dray secrets import` reads every secret
// through it and writes them to the configured store. It decrypts with the
// sops binary, so plaintext never touches the disk.
package sops

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/BrutalSystems/dray/internal/secrets"
)

// Format is how a file holds a secret.
type Format string

// Formats.
const (
	// Manifest is a sops-encrypted Kubernetes Secret (data and stringData).
	Manifest Format = "manifest"
	// Dotenv is a sops-encrypted file decrypted as dotenv; values lose
	// surrounding quotes, as the JS tool's build arguments did.
	Dotenv Format = "dotenv"
	// ExecEnv is a sops-encrypted file whose top-level keys become
	// environment variables, as `sops exec-env` exports them.
	ExecEnv Format = "exec-env"
	// EnvFile is a plain file read the way kubectl's --from-env-file reads it.
	EnvFile Format = "env-file"
)

// Source is where one secret comes from.
type Source struct {
	// File is relative to the store's folder.
	File   string
	Format Format
	// SecretName picks the Secret in a Manifest file holding several.
	SecretName string
	// Prefix keeps only keys starting with it (Dotenv only).
	Prefix string
}

// Runner runs sops with args in dir and returns its standard output.
type Runner func(ctx context.Context, dir string, args ...string) ([]byte, error)

// Store reads the secrets listed in Sources, by logical path.
type Store struct {
	// Dir is the folder files are relative to, usually a repo.
	Dir     string
	Sources map[string]Source
	// Run defaults to the sops binary on PATH.
	Run Runner
}

func (s *Store) String() string { return "sops " + s.Dir }

// Put implements secrets.Store: the store is read-only.
func (s *Store) Put(context.Context, string, secrets.Values) error { return secrets.ErrReadOnly }

// Delete implements secrets.Store: the store is read-only.
func (s *Store) Delete(context.Context, string) error { return secrets.ErrReadOnly }

// List implements secrets.Store.
func (s *Store) List(_ context.Context, prefix string) ([]string, error) {
	if err := secrets.CheckPrefix(prefix); err != nil {
		return nil, err
	}
	out := []string{}
	for p := range s.Sources {
		if secrets.UnderPrefix(p, prefix) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Get implements secrets.Store.
func (s *Store) Get(ctx context.Context, p string) (secrets.Values, error) {
	if err := secrets.CheckPath(p); err != nil {
		return nil, err
	}
	src, ok := s.Sources[p]
	if !ok {
		return nil, fmt.Errorf("%s: %w", p, secrets.ErrNotFound)
	}
	v, err := s.read(ctx, src)
	if err != nil {
		return nil, fmt.Errorf("%s (%s): %w", p, src.File, err)
	}
	return v, nil
}

func (s *Store) read(ctx context.Context, src Source) (secrets.Values, error) {
	if err := s.checkFile(src.File); err != nil {
		return nil, err
	}
	// "./" keeps a file name such as "--x" from reading as a sops flag.
	arg := "./" + filepath.ToSlash(filepath.Clean(filepath.FromSlash(src.File)))
	switch src.Format {
	case EnvFile:
		root, err := os.OpenRoot(s.Dir)
		if err != nil {
			return nil, err
		}
		defer func() { _ = root.Close() }()
		data, err := root.ReadFile(filepath.FromSlash(src.File))
		if err != nil {
			return nil, err
		}
		return parseEnvFile(data)
	case Manifest:
		out, err := s.sops(ctx, "-d", arg)
		if err != nil {
			return nil, err
		}
		return secretFromManifest(out, src.SecretName)
	case Dotenv:
		out, err := s.sops(ctx, "-d", "--output-type", "dotenv", arg)
		if err != nil {
			return nil, err
		}
		return parseDotenv(out, src.Prefix), nil
	case ExecEnv:
		out, err := s.sops(ctx, "-d", "--output-type", "json", arg)
		if err != nil {
			return nil, err
		}
		return topLevel(out)
	}
	return nil, fmt.Errorf("unknown format %q", src.Format)
}

// checkFile keeps every file inside the store's folder, symlinks included.
func (s *Store) checkFile(f string) error {
	if !filepath.IsLocal(filepath.FromSlash(f)) {
		return fmt.Errorf("%s is outside %s", f, s.Dir)
	}
	dir, err := filepath.EvalSymlinks(s.Dir)
	if err != nil {
		return err
	}
	full, err := filepath.EvalSymlinks(filepath.Join(dir, filepath.FromSlash(f)))
	if err != nil {
		return err
	}
	if rel, err := filepath.Rel(dir, full); err != nil || !filepath.IsLocal(rel) {
		return fmt.Errorf("%s leads outside %s", f, s.Dir)
	}
	return nil
}

func (s *Store) sops(ctx context.Context, args ...string) ([]byte, error) {
	run := s.Run
	if run == nil {
		run = runSops
	}
	return run(ctx, s.Dir, args...)
}

func runSops(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "sops", args...) //nolint:gosec // fixed binary; the file argument is checked and starts with ./
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("sops: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// secretFromManifest returns the values of the Secret named name (or the
// only Secret) in a YAML stream.
func secretFromManifest(data []byte, name string) (secrets.Values, error) {
	type secret struct {
		Kind     string `yaml:"kind"`
		Metadata struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
		Data       map[string]string `yaml:"data"`
		StringData map[string]string `yaml:"stringData"`
	}
	var found []secret
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var doc secret
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if doc.Kind == "Secret" {
			found = append(found, doc)
		}
	}
	switch {
	case len(found) == 0:
		return nil, errors.New("no Secret in the file")
	case len(found) > 1:
		// The JS tool applied every one; importing one would drop the rest.
		names := make([]string, len(found))
		for i, f := range found {
			names[i] = f.Metadata.Name
		}
		return nil, fmt.Errorf("the file holds %d Secrets (%s); give each its own entry", len(found), strings.Join(names, ", "))
	case name != "" && found[0].Metadata.Name != name:
		return nil, fmt.Errorf("the file's Secret is named %q, not %q", found[0].Metadata.Name, name)
	}
	pick := &found[0]
	out := secrets.Values{}
	for k, b64 := range pick.Data {
		v, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
		if err != nil {
			return nil, fmt.Errorf("data.%s is not base64", k)
		}
		out[k] = string(v)
	}
	for k, v := range pick.StringData {
		out[k] = v
	}
	return out, nil
}

var dotenvLine = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$`)

// parseDotenv reads dotenv text as the JS tool's build arguments did: one
// KEY=VALUE per line, surrounding quotes removed, filtered by prefix.
func parseDotenv(data []byte, prefix string) secrets.Values {
	out := secrets.Values{}
	for _, line := range strings.Split(string(data), "\n") {
		m := dotenvLine.FindStringSubmatch(line)
		if m == nil || !strings.HasPrefix(m[1], prefix) {
			continue
		}
		v := strings.TrimSpace(m[2])
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		out[m[1]] = v
	}
	return out
}

// parseEnvFile reads a file as kubectl's --from-env-file does: leading space
// and blank or # lines skipped, the value kept exactly as written. A line
// without '=' takes its value from kubectl's environment there; here it is
// an error, since that value is not in the file.
func parseEnvFile(data []byte) (secrets.Values, error) {
	out := secrets.Values{}
	sc := bufio.NewScanner(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimLeft(sc.Text(), " \t\r\v\f")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: %s has no value; kubectl took it from its environment, so set it in the file", n, k)
		}
		out[k] = v
	}
	return out, sc.Err()
}

// topLevel returns a JSON object's top-level values as strings, as sops
// exec-env exports them.
func topLevel(data []byte) (secrets.Values, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	out := secrets.Values{}
	for k, raw := range m {
		var str string
		switch {
		case json.Unmarshal(raw, &str) == nil:
			out[k] = str
		case len(raw) > 0 && (raw[0] == '{' || raw[0] == '['):
			return nil, fmt.Errorf("key %s holds a nested value; exec-env exports only flat keys", k)
		default:
			out[k] = string(raw)
		}
	}
	return out, nil
}
