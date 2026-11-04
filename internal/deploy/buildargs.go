package deploy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"

	"github.com/BrutalSystems/dray/internal/config"
	"github.com/BrutalSystems/dray/internal/secrets"
)

var envLine = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$`)

// parseEnvLines reads dotenv-style text as the JS tool did: KEY=VALUE lines,
// one pair of surrounding quotes stripped, other lines ignored.
func parseEnvLines(data []byte, prefix string, into map[string]string) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		m := envLine.FindStringSubmatch(sc.Text())
		if m == nil || !strings.HasPrefix(m[1], prefix) {
			continue
		}
		into[m[1]] = stripQuotes(m[2])
	}
}

func stripQuotes(v string) string {
	t := strings.TrimSpace(v)
	if len(t) >= 2 && (t[0] == '"' && t[len(t)-1] == '"' || t[0] == '\'' && t[len(t)-1] == '\'') {
		return t[1 : len(t)-1]
	}
	return t
}

// buildArgs are an image's build arguments. The env file is read from the
// repo's own folder, not the build workspace, because such files are usually
// kept out of git (CI writes them). A missing env file is an error: the JS
// tool skipped it, and a front end built without its settings deploys broken.
func buildArgs(ctx context.Context, img config.Image, repoDir, commit string, store secrets.Store) (map[string]string, error) {
	args := map[string]string{"GIT_SHA": commit}
	b := img.BuildArgs
	if b == nil {
		return args, nil
	}
	if b.EnvFile != "" {
		root, err := os.OpenRoot(repoDir)
		if err != nil {
			return nil, err
		}
		data, err := root.ReadFile(b.EnvFile)
		_ = root.Close()
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("image %s: build_args.env_file %s does not exist; the image would be built without its settings", img.Name, b.EnvFile)
		}
		if err != nil {
			return nil, fmt.Errorf("image %s: %w", img.Name, err)
		}
		before := len(args)
		parseEnvLines(data, b.Prefix, args)
		if len(args) == before {
			return nil, fmt.Errorf("image %s: build_args.env_file %s has no %s values", img.Name, b.EnvFile, prefixWord(b.Prefix))
		}
	}
	if b.FromSecret != "" {
		if store == nil {
			return nil, fmt.Errorf("image %s: build_args.from_secret needs a secret store", img.Name)
		}
		path, key, err := secrets.SplitRef(b.FromSecret)
		if err != nil {
			return nil, fmt.Errorf("image %s: %w", img.Name, err)
		}
		if key != "" {
			return nil, fmt.Errorf("image %s: build_args.from_secret names a whole secret, not one key", img.Name)
		}
		v, err := store.Get(ctx, path)
		if err != nil {
			return nil, fmt.Errorf("image %s: build_args.from_secret: %w", img.Name, err)
		}
		for k, val := range v {
			if strings.HasPrefix(k, b.Prefix) {
				args[k] = val
			}
		}
	}
	for k, v := range b.Values {
		args[k] = v
	}
	return args, nil
}

func prefixWord(p string) string {
	if p == "" {
		return "KEY=VALUE"
	}
	return p + "*"
}
