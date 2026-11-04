// Package buildkit is the deploy.Builder adapter for a BuildKit daemon,
// driven with buildctl. The server uses it in the cluster in place of a local
// Docker; the daemon pushes the image and reports its digest.
package buildkit

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

// Builder builds on the BuildKit daemon at Addr.
type Builder struct {
	// Addr is the daemon, such as tcp://buildkitd.dray:1234.
	Addr string
	// DockerConfig is the folder whose config.json holds registry logins for
	// the push (DOCKER_CONFIG); empty is buildctl's default.
	DockerConfig string
	// Cache, when set, names a registry reference to keep the build cache in
	// for an image reference, so a fresh daemon need not start from nothing.
	Cache func(ctx context.Context, ref string) (string, error)
	// Run runs buildctl; nil is execx.Run.
	Run execx.Runner
	// Progress receives the build's output; nil discards it.
	Progress io.Writer
}

var _ deploy.Builder = (*Builder)(nil)

// Args is the buildctl command line for b, writing metadata to meta.
//
// Unlike docker, buildctl takes build argument values on its command line.
// Build arguments are never secrets (D13): anything in them can end up in the
// image.
func Args(addr string, b deploy.BuildSpec, meta, cache string) []string {
	args := []string{"--addr", addr, "build",
		"--frontend", "dockerfile.v0",
		"--local", "context=" + b.Context,
		"--local", "dockerfile=" + filepath.Dir(b.Dockerfile),
		"--opt", "filename=" + filepath.Base(b.Dockerfile),
		"--output", "type=image,name=" + b.Ref + ",push=true",
		"--metadata-file", meta,
	}
	if b.Platform != "" {
		args = append(args, "--opt", "platform="+b.Platform)
	}
	if cache != "" {
		args = append(args,
			"--import-cache", "type=registry,ref="+cache,
			"--export-cache", "type=registry,ref="+cache+",mode=max,image-manifest=true,oci-mediatypes=true")
	}
	names := make([]string, 0, len(b.Args))
	for k := range b.Args {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		args = append(args, "--opt", "build-arg:"+k+"="+b.Args[k])
	}
	return args
}

// Build builds and pushes b and returns the pushed digest.
func (k *Builder) Build(ctx context.Context, b deploy.BuildSpec) (string, error) {
	if k.Addr == "" {
		return "", fmt.Errorf("no BuildKit daemon address")
	}
	dir, err := os.MkdirTemp("", "dray-buildkit-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	meta := filepath.Join(dir, "metadata.json")
	run := k.Run
	if run == nil {
		run = execx.Run
	}
	progress := k.Progress
	if progress == nil {
		progress = io.Discard
	}
	cache := ""
	if k.Cache != nil {
		if cache, err = k.Cache(ctx, b.Ref); err != nil {
			return "", fmt.Errorf("build cache: %w", err)
		}
	}
	cmd := execx.Cmd{Name: "buildctl", Args: Args(k.Addr, b, meta, cache), Stream: progress}
	if k.DockerConfig != "" {
		cmd.Env = []string{"DOCKER_CONFIG=" + k.DockerConfig}
	}
	if _, err := run(ctx, cmd); err != nil {
		return "", err
	}
	data, err := os.ReadFile(meta) //nolint:gosec // our own temp file
	if err != nil {
		return "", fmt.Errorf("reading the build's metadata: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return "", fmt.Errorf("reading the build's metadata: %w", err)
	}
	d, _ := m["containerimage.digest"].(string)
	if !strings.HasPrefix(d, "sha256:") {
		return "", fmt.Errorf("the build reported no pushed digest")
	}
	return d, nil
}
