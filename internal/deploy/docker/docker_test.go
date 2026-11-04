package docker

import (
	"strings"
	"testing"

	"github.com/BrutalSystems/dray/internal/deploy"
)

func TestArgsKeepValuesOffTheCommandLine(t *testing.T) {
	args, env := Args(deploy.BuildSpec{
		Context: "/w", Dockerfile: "/w/app/Dockerfile", Ref: "reg/app:abc", Platform: "linux/arm64",
		Args: map[string]string{"TOKEN": "s3cret", "GIT_SHA": "abc"},
	}, "/tmp/meta.json")
	line := strings.Join(args, " ")
	want := "buildx build --provenance=false -f /w/app/Dockerfile -t reg/app:abc --push --metadata-file /tmp/meta.json --platform linux/arm64 --build-arg GIT_SHA --build-arg TOKEN /w"
	if line != want {
		t.Errorf("args\n got %s\nwant %s", line, want)
	}
	if strings.Contains(line, "s3cret") || strings.Join(env, " ") != "GIT_SHA=abc TOKEN=s3cret" {
		t.Errorf("env %v", env)
	}
}

func TestDigestFrom(t *testing.T) {
	d, err := digestFrom([]byte(`{"containerimage.digest":"sha256:abc","image.name":"x"}`))
	if err != nil || d != "sha256:abc" {
		t.Errorf("%q %v", d, err)
	}
	if _, err := digestFrom([]byte(`{}`)); err == nil {
		t.Error("no digest accepted")
	}
}
