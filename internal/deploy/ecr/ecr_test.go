package ecr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BrutalSystems/dray/internal/execx"
)

type call struct {
	cmd   execx.Cmd
	stdin string
}

func runner(results map[string]func() ([]byte, error), calls *[]call) execx.Runner {
	return func(_ context.Context, c execx.Cmd) ([]byte, error) {
		in := ""
		if c.Stdin != nil {
			b, _ := io.ReadAll(c.Stdin)
			in = string(b)
		}
		*calls = append(*calls, call{c, in})
		for prefix, f := range results {
			if strings.HasPrefix(c.String(), prefix) {
				return f()
			}
		}
		return nil, nil
	}
}

func fail(stderr string) func() ([]byte, error) {
	return func() ([]byte, error) { return nil, &execx.Error{Cmd: "aws", Code: 254, Stderr: stderr} }
}

func TestDigest(t *testing.T) {
	var calls []call
	r := &Registry{Account: "123456789012", Region: "us-east-2", Profile: "deploy"}
	r.Run = runner(map[string]func() ([]byte, error){
		"aws ecr describe-images": func() ([]byte, error) { return []byte("sha256:abc\n"), nil },
	}, &calls)
	d, err := r.Digest(context.Background(), "app", "1234567")
	if err != nil || d != "sha256:abc" {
		t.Fatalf("%q %v", d, err)
	}
	if got := calls[0].cmd.String(); !strings.Contains(got, "--image-ids imageTag=1234567") || !strings.HasSuffix(got, "--region us-east-2 --profile deploy") {
		t.Errorf("command %s", got)
	}
	for stderr, wantErr := range map[string]bool{
		"An error occurred (ImageNotFoundException)":      false,
		"An error occurred (RepositoryNotFoundException)": false,
		"An error occurred (AccessDeniedException)":       true,
	} {
		r.Run = runner(map[string]func() ([]byte, error){"aws ecr describe-images": fail(stderr)}, &calls)
		d, err := r.Digest(context.Background(), "app", "t")
		if (err != nil) != wantErr || d != "" {
			t.Errorf("%s: %q %v", stderr, d, err)
		}
	}
}

func TestEnsure(t *testing.T) {
	var calls []call
	r := &Registry{Account: "123456789012", Region: "us-east-2"}
	r.Run = runner(map[string]func() ([]byte, error){"aws ecr describe-repositories": fail("RepositoryNotFoundException")}, &calls)
	if err := r.Ensure(context.Background(), "app"); err != nil {
		t.Fatal(err)
	}
	if got := calls[1].cmd.String(); !strings.Contains(got, "create-repository --repository-name app --image-tag-mutability IMMUTABLE --image-scanning-configuration scanOnPush=true") {
		t.Errorf("create: %s", got)
	}
	calls = nil
	r.Run = runner(map[string]func() ([]byte, error){"aws ecr describe-repositories": fail("AccessDeniedException")}, &calls)
	if err := r.Ensure(context.Background(), "app"); err == nil || len(calls) != 1 {
		t.Errorf("a permission error must not turn into a create: %v, %d calls", err, len(calls))
	}
}

func TestLoginKeepsPasswordOffTheCommandLine(t *testing.T) {
	var calls []call
	r := &Registry{Account: "123456789012", Region: "us-east-2"}
	r.Run = runner(map[string]func() ([]byte, error){
		"aws ecr get-login-password": func() ([]byte, error) { return []byte("pw123\n"), nil },
	}, &calls)
	if err := r.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	login := calls[1]
	if strings.Contains(login.cmd.String(), "pw123") || login.stdin != "pw123" ||
		login.cmd.String() != "docker login --username AWS --password-stdin 123456789012.dkr.ecr.us-east-2.amazonaws.com" {
		t.Errorf("login %q stdin %q", login.cmd.String(), login.stdin)
	}
}

func TestLoginWritesDockerConfig(t *testing.T) {
	var calls []call
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"auths":{"other.example.com":{"auth":"eA=="}},"credsStore":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &Registry{Account: "123456789012", Region: "us-east-2", DockerConfig: dir}
	r.Run = runner(map[string]func() ([]byte, error){
		"aws ecr get-login-password": func() ([]byte, error) { return []byte("pw123\n"), nil },
	}, &calls)
	if err := r.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Errorf("docker was run: %v", calls)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "config.json")) //nolint:gosec // test
	var cfg struct {
		Auths      map[string]map[string]string `json:"auths"`
		CredsStore string                       `json:"credsStore"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Auths["123456789012.dkr.ecr.us-east-2.amazonaws.com"]["auth"] != base64.StdEncoding.EncodeToString([]byte("AWS:pw123")) ||
		cfg.Auths["other.example.com"]["auth"] != "eA==" || cfg.CredsStore != "x" {
		t.Errorf("config %s", data)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "config.json")); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}
}
