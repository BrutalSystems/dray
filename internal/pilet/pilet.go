// Package pilet publishes Piral pilets to a feed: build each one with the
// repo's own build, pack it with the pilet CLI, stamp its version, and upload
// the package to the feed's publish endpoint.
package pilet

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BrutalSystems/dray/internal/config"
	"github.com/BrutalSystems/dray/internal/execx"
)

// DefaultPath is where a pilet lives when the config does not say.
const DefaultPath = "packages/{name}"

// DefaultBuild builds a pilet when the config does not say how.
var DefaultBuild = []string{"npx", "--no-install", "pilet", "build"}

// Options say which pilets to publish where.
type Options struct {
	RepoDir string
	Pilets  config.Pilets
	// Names are the pilets to publish; empty is all of them.
	Names []string
	// FeedURL is the feed's publish endpoint.
	FeedURL string
	// APIKey is the feed's publish key. It reaches the feed in a header only.
	APIKey string
	// Suffix, when set, is appended to each package.json version as a
	// prerelease: 1.2.0 becomes 1.2.0-<Suffix>.
	Suffix string
	// DryRun builds and packs but does not upload.
	DryRun bool

	// Run runs the build and pack; nil is execx.Run.
	Run execx.Runner
	// HTTP uploads; nil is http.DefaultClient.
	HTTP *http.Client
	// Out receives progress; Progress receives build output.
	Out, Progress io.Writer
}

// Result is one published pilet.
type Result struct {
	Pilet   string
	Package string
	Version string
}

// Publish publishes the chosen pilets, stopping at the first failure.
func Publish(ctx context.Context, o Options) ([]Result, error) {
	names := o.Names
	if len(names) == 0 {
		names = o.Pilets.Names
	}
	for _, n := range names {
		if !contains(o.Pilets.Names, n) {
			return nil, fmt.Errorf("unknown pilet %q (the config lists %s)", n, strings.Join(o.Pilets.Names, ", "))
		}
	}
	if o.Out == nil {
		o.Out = io.Discard
	}
	var out []Result
	for _, n := range names {
		r, err := publishOne(ctx, o, n)
		if err != nil {
			return out, fmt.Errorf("pilet %s: %w", n, err)
		}
		out = append(out, r)
	}
	return out, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func fill(args []string, name string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = strings.ReplaceAll(a, "{name}", name)
	}
	return out
}

// Dir is a pilet's folder inside the repo.
func Dir(repoDir string, p config.Pilets, name string) (string, error) {
	rel := p.Path
	if rel == "" {
		rel = DefaultPath
	}
	rel = filepath.FromSlash(strings.ReplaceAll(rel, "{name}", name))
	dir := filepath.Join(repoDir, rel)
	if r, err := filepath.Rel(repoDir, dir); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("folder %s is outside the repo", rel)
	}
	return dir, nil
}

func publishOne(ctx context.Context, o Options, name string) (Result, error) {
	dir, err := Dir(o.RepoDir, o.Pilets, name)
	if err != nil {
		return Result{}, err
	}
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err != nil {
		return Result{}, fmt.Errorf("no package.json in %s", dir)
	}
	run := o.Run
	if run == nil {
		run = execx.Run
	}
	progress := o.Progress
	if progress == nil {
		progress = io.Discard
	}
	build := o.Pilets.Build
	if len(build) == 0 {
		build = DefaultBuild
	}
	build = fill(build, name)
	_, _ = fmt.Fprintf(o.Out, "\n── %s: building (%s)\n", name, strings.Join(build, " "))
	if _, err := run(ctx, execx.Cmd{Name: build[0], Args: build[1:], Dir: dir, Stream: progress}); err != nil {
		return Result{}, err
	}
	tmp, err := os.MkdirTemp("", "dray-pilet-")
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if _, err := run(ctx, execx.Cmd{Name: "npx", Args: []string{"--no-install", "pilet", "pack", "--target", tmp}, Dir: dir, Stream: progress}); err != nil {
		return Result{}, err
	}
	tgz, err := onlyTarball(tmp)
	if err != nil {
		return Result{}, err
	}
	data, err := os.ReadFile(tgz) //nolint:gosec // our own temp folder
	if err != nil {
		return Result{}, err
	}
	pkg, version, data, err := Restamp(data, o.Suffix)
	if err != nil {
		return Result{}, err
	}
	r := Result{Pilet: name, Package: pkg, Version: version}
	if o.DryRun {
		_, _ = fmt.Fprintf(o.Out, "%s@%s packed (%d bytes); not uploaded (dry run)\n", pkg, version, len(data))
		return r, nil
	}
	if err := upload(ctx, o, filepath.Base(tgz), data); err != nil {
		return r, err
	}
	_, _ = fmt.Fprintf(o.Out, "published %s@%s\n", pkg, version)
	return r, nil
}

func onlyTarball(dir string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.tgz"))
	if err != nil {
		return "", err
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("pilet pack left %d packages, want 1", len(matches))
	}
	return matches[0], nil
}

var prerelease = regexp.MustCompile(`^[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*$`)

// Restamp rewrites package/package.json inside a packed pilet so its version
// carries suffix as a prerelease, and returns the package name and the final
// version. An empty suffix keeps the version as packed.
func Restamp(tgz []byte, suffix string) (name, version string, out []byte, err error) {
	if suffix != "" && !prerelease.MatchString(suffix) {
		return "", "", nil, fmt.Errorf("version suffix %q is not a valid prerelease", suffix)
	}
	zr, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return "", "", nil, fmt.Errorf("reading the package: %w", err)
	}
	tr := tar.NewReader(zr)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	found := false
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", "", nil, fmt.Errorf("reading the package: %w", err)
		}
		body, err := io.ReadAll(io.LimitReader(tr, 256<<20))
		if err != nil {
			return "", "", nil, err
		}
		if h.Name == "package/package.json" {
			found = true
			if body, name, version, err = stampManifest(body, suffix); err != nil {
				return "", "", nil, err
			}
			h.Size = int64(len(body))
		}
		if err := tw.WriteHeader(h); err != nil {
			return "", "", nil, err
		}
		if _, err := tw.Write(body); err != nil {
			return "", "", nil, err
		}
	}
	if !found {
		return "", "", nil, errors.New("the package has no package/package.json")
	}
	if err := tw.Close(); err != nil {
		return "", "", nil, err
	}
	if err := zw.Close(); err != nil {
		return "", "", nil, err
	}
	return name, version, buf.Bytes(), nil
}

// stampManifest sets the version in a package.json, keeping every other
// field as it is.
func stampManifest(data []byte, suffix string) (out []byte, name, version string, err error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, "", "", fmt.Errorf("package.json: %w", err)
	}
	if err := json.Unmarshal(m["name"], &name); err != nil || name == "" {
		return nil, "", "", errors.New("package.json has no name")
	}
	if err := json.Unmarshal(m["version"], &version); err != nil || version == "" {
		return nil, "", "", errors.New("package.json has no version")
	}
	if suffix == "" {
		return data, name, version, nil
	}
	if strings.ContainsAny(version, "-+") {
		return nil, "", "", fmt.Errorf("version %s already has a prerelease or build part; Dray adds its own", version)
	}
	version += "-" + suffix
	v, _ := json.Marshal(version)
	m["version"] = v
	out, err = json.MarshalIndent(m, "", "  ")
	return out, name, version, err
}

// upload posts the package as the multipart field "file", with the key in
// X-Api-Key, which Piral feed services accept.
func upload(ctx context.Context, o Options, filename string, data []byte) error {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return err
	}
	if _, err := fw.Write(data); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.FeedURL, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Api-Key", o.APIKey)
	client := o.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("uploading to %s: %w", o.FeedURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("the feed refused the package: %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}
