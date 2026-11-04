package buildkit

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/BrutalSystems/dray/internal/deploy"
	"github.com/BrutalSystems/dray/internal/execx"
)

func TestPodBuilderStartsBuildsAndDeletesItsPod(t *testing.T) {
	var kube, builds []string
	var manifest []byte
	p := &PodBuilder{
		Namespace: "dray", Memory: "6Gi", CPU: "2",
		Kubectl: func(_ context.Context, c execx.Cmd) ([]byte, error) {
			kube = append(kube, c.String())
			if c.Stdin != nil {
				manifest, _ = io.ReadAll(c.Stdin)
			}
			if c.Args[0] == "get" {
				return []byte("10.0.1.7"), nil
			}
			return nil, nil
		},
	}
	p.Run = func(_ context.Context, c execx.Cmd) ([]byte, error) {
		builds = append(builds, c.String())
		for i, a := range c.Args {
			if a == "--metadata-file" {
				return nil, os.WriteFile(c.Args[i+1], []byte(`{"containerimage.digest":"sha256:abc"}`), 0o600)
			}
		}
		return nil, nil
	}
	ctx := context.Background()
	for _, ref := range []string{"reg/a:1", "reg/b:1"} {
		if d, err := p.Build(ctx, deploy.BuildSpec{Context: "/w", Dockerfile: "/w/Dockerfile", Ref: ref}); err != nil || d != "sha256:abc" {
			t.Fatalf("%s %v", d, err)
		}
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if len(kube) != 4 || !strings.HasPrefix(kube[0], "kubectl apply -f - -n dray") || !strings.Contains(kube[1], "wait --for=condition=Ready") || !strings.Contains(kube[3], "delete pod/buildkitd-") {
		t.Errorf("one pod for both builds, then deleted: %v", kube)
	}
	if len(builds) != 2 || !strings.Contains(builds[1], "--addr tcp://10.0.1.7:1234") {
		t.Errorf("builds %v", builds)
	}
	var pod struct {
		Spec struct {
			NodeSelector map[string]string `json:"nodeSelector"`
			Tolerations  []map[string]string
			Containers   []struct {
				Resources struct {
					Requests map[string]string `json:"requests"`
					Limits   map[string]string `json:"limits"`
				} `json:"resources"`
			} `json:"containers"`
			ActiveDeadlineSeconds int `json:"activeDeadlineSeconds"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal(manifest, &pod); err != nil {
		t.Fatalf("%v\n%s", err, manifest)
	}
	r := pod.Spec.Containers[0].Resources
	if r.Requests["memory"] != "6Gi" || r.Limits["memory"] != "6Gi" || r.Requests["cpu"] != "2" {
		t.Errorf("the pod must request the memory it may use: %+v", r)
	}
	if pod.Spec.NodeSelector[BuildNodeKey] != "true" || pod.Spec.Tolerations[0]["key"] != BuildNodeKey || pod.Spec.ActiveDeadlineSeconds == 0 {
		t.Errorf("build nodes only, with a deadline: %+v", pod.Spec)
	}
	if err := p.Close(); err != nil || len(kube) != 4 {
		t.Error("a second Close did something")
	}
}

func TestPodBuilderNeedsResources(t *testing.T) {
	p := &PodBuilder{Namespace: "dray"}
	if _, err := p.Build(context.Background(), deploy.BuildSpec{}); err == nil {
		t.Error("a build pod without memory and cpu was started")
	}
}
