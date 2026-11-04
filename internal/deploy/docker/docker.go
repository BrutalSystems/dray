// Package docker is the deploy.Builder adapter that builds with docker buildx
// and pushes in the same step, reading the pushed digest from buildx's
// metadata file.
package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BrutalSystems/dray/internal/deploy"
	"github.com/BrutalSystems/dray/internal/execx"
)

// Builder builds with the local docker.
type Builder struct {
	// Run runs docker; nil is execx.Run.
	Run execx.Runner
	// Progress receives build output; nil discards it.
	Progress io.Writer
}

var _ deploy.Builder = (*Builder)(nil)

// Args is the buildx command line for b, writing metadata to meta. Build
// argument values are not in it: each is named with --build-arg NAME and its
// value set in the environment, so values never show in a process listing.
func Args(b deploy.BuildSpec, meta string) (args, env []string) {
	args = []string{"buildx", "build", "--provenance=false", "-f", b.Dockerfile, "-t", b.Ref, "--push", "--metadata-file", meta}
	if b.Platform != "" {
		args = append(args, "--platform", b.Platform)
	}
	names := make([]string, 0, len(b.Args))
	for k := range b.Args {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		args = append(args, "--build-arg", k)
		env = append(env, k+"="+b.Args[k])
	}
	return append(args, b.Context), env
}

// Build builds and pushes b and returns the pushed digest.
func (d *Builder) Build(ctx context.Context, b deploy.BuildSpec) (string, error) {
	dir, err := os.MkdirTemp("", "dray-build-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	meta := filepath.Join(dir, "metadata.json")
	args, env := Args(b, meta)
	run := d.Run
	if run == nil {
		run = execx.Run
	}
	progress := d.Progress
	if progress == nil {
		progress = io.Discard
	}
	if _, err := run(ctx, execx.Cmd{Name: "docker", Args: args, Env: env, Stream: progress}); err != nil {
		return "", err
	}
	data, err := os.ReadFile(meta) //nolint:gosec // our own temp file
	if err != nil {
		return "", fmt.Errorf("reading the build's metadata: %w", err)
	}
	return digestFrom(data)
}

func digestFrom(meta []byte) (string, error) {
	var m map[string]any
	if err := json.Unmarshal(meta, &m); err != nil {
		return "", fmt.Errorf("reading the build's metadata: %w", err)
	}
	d, _ := m["containerimage.digest"].(string)
	if !strings.HasPrefix(d, "sha256:") {
		return "", fmt.Errorf("the build reported no pushed digest")
	}
	return d, nil
}
