// Package ecr is the deploy.Registry adapter for Amazon ECR, through the aws
// CLI (Dray's core imports no cloud SDK).
package ecr

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BrutalSystems/dray/internal/deploy"
	"github.com/BrutalSystems/dray/internal/execx"
)

// Registry is one account's registry in one region.
type Registry struct {
	Account string
	Region  string
	// Profile is a local credentials profile; empty uses the environment's.
	Profile string
	// DockerConfig, when set, makes Login write the registry login into
	// <DockerConfig>/config.json itself instead of running docker login, for
	// a builder with no Docker (the server's BuildKit).
	DockerConfig string
	// Run runs aws and docker; nil is execx.Run.
	Run execx.Runner
}

var _ deploy.Registry = (*Registry)(nil)

// Host is the registry address.
func (r *Registry) Host() string {
	return r.Account + ".dkr.ecr." + r.Region + ".amazonaws.com"
}

func (r *Registry) aws(ctx context.Context, args ...string) ([]byte, error) {
	run := r.Run
	if run == nil {
		run = execx.Run
	}
	args = append(args, "--region", r.Region)
	if r.Profile != "" {
		args = append(args, "--profile", r.Profile)
	}
	return run(ctx, execx.Cmd{Name: "aws", Args: args})
}

// Login logs docker in to the registry. The password goes from aws to docker
// through memory and stdin, never the command line.
func (r *Registry) Login(ctx context.Context) error {
	pw, err := r.aws(ctx, "ecr", "get-login-password")
	if err != nil {
		return err
	}
	pw = bytes.TrimSpace(pw)
	if r.DockerConfig != "" {
		return writeLogin(r.DockerConfig, r.Host(), "AWS", pw)
	}
	run := r.Run
	if run == nil {
		run = execx.Run
	}
	_, err = run(ctx, execx.Cmd{
		Name: "docker", Args: []string{"login", "--username", "AWS", "--password-stdin", r.Host()},
		Stdin: bytes.NewReader(pw),
	})
	return err
}

// writeLogin sets host's login in dir/config.json, keeping other entries.
// The file is the user's alone (0600).
func writeLogin(dir, host, user string, pw []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, "config.json")
	cfg := map[string]any{}
	if data, err := os.ReadFile(path); err == nil { //nolint:gosec // the configured folder
		if err := json.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	auths, _ := cfg["auths"].(map[string]any)
	if auths == nil {
		auths = map[string]any{}
	}
	auths[host] = map[string]any{"auth": base64.StdEncoding.EncodeToString(append([]byte(user+":"), pw...))}
	cfg["auths"] = auths
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func stderrHas(err error, s string) bool {
	var e *execx.Error
	return errors.As(err, &e) && strings.Contains(e.Stderr, s)
}

// Ensure creates a missing repository with immutable tags and scan on push.
// Any other failure (often a missing permission) is reported as it is, not
// turned into a misleading create error.
func (r *Registry) Ensure(ctx context.Context, repo string) error {
	_, err := r.aws(ctx, "ecr", "describe-repositories", "--repository-names", repo)
	if err == nil {
		return nil
	}
	if !stderrHas(err, "RepositoryNotFoundException") {
		return err
	}
	_, err = r.aws(ctx, "ecr", "create-repository", "--repository-name", repo,
		"--image-tag-mutability", "IMMUTABLE", "--image-scanning-configuration", "scanOnPush=true")
	return err
}

// Digest is the digest a tag points at, or "" when the tag or the repository
// does not exist. Any other failure is an error: mistaking "cannot read" for
// "missing" would push over an image that is already deployed.
func (r *Registry) Digest(ctx context.Context, repo, tag string) (string, error) {
	out, err := r.aws(ctx, "ecr", "describe-images", "--repository-name", repo, "--image-ids", "imageTag="+tag,
		"--query", "imageDetails[0].imageDigest", "--output", "text")
	if err != nil {
		if stderrHas(err, "ImageNotFoundException") || stderrHas(err, "RepositoryNotFoundException") {
			return "", nil
		}
		return "", err
	}
	d := strings.TrimSpace(string(out))
	if !strings.HasPrefix(d, "sha256:") {
		return "", fmt.Errorf("aws ecr describe-images %s:%s returned %q, not a digest", repo, tag, d)
	}
	return d, nil
}
