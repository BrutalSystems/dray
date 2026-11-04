package buildkit

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/BrutalSystems/dray/internal/deploy"
	"github.com/BrutalSystems/dray/internal/execx"
)

// BuildNodeKey is the label and taint that mark build nodes: a build pod
// selects and tolerates it, and nothing else does, so builds and other
// workloads never share a node.
const BuildNodeKey = "dray/build"

// PodBuilder starts a BuildKit pod for one deploy, builds every image of the
// deploy on it, and deletes it when the deploy is done (Close). The pod
// requests all the memory it may use, so the scheduler and the node
// autoscaler reserve it; when no build node is free, one is started, and an
// empty build node is removed again. Nothing runs between deploys.
type PodBuilder struct {
	Builder
	// Namespace holds the build pods.
	Namespace string
	// Image is the BuildKit image (rootless).
	Image string
	// Memory and CPU are what the pod requests; memory is also its limit.
	Memory, CPU string
	// Platform is the node architecture, such as arm64.
	Arch string
	// Kubectl runs kubectl; nil is execx.Run.
	Kubectl execx.Runner
	// Ready bounds the wait for the pod, node start included.
	Ready time.Duration

	mu   sync.Mutex
	name string
}

var _ deploy.Builder = (*PodBuilder)(nil)

// DefaultImage is the rootless BuildKit image build pods run.
const DefaultImage = "moby/buildkit:v0.34.2-rootless"

var podTemplate = template.Must(template.New("pod").Parse(`apiVersion: v1
kind: Pod
metadata:
  name: {{.Name}}
  labels:
    app.kubernetes.io/name: buildkitd
    app.kubernetes.io/managed-by: dray
spec:
  restartPolicy: Never
  # A pod a crashed server never deleted ends by itself, and its node with it.
  activeDeadlineSeconds: 7200
  nodeSelector:
    kubernetes.io/arch: {{.Arch}}
    {{.Key}}: "true"
  tolerations:
    - key: {{.Key}}
      operator: Equal
      value: "true"
      effect: NoSchedule
  containers:
    - name: buildkitd
      image: {{.Image}}
      args: [--addr, "unix:///run/user/1000/buildkit/buildkitd.sock", --addr, "tcp://0.0.0.0:1234", --oci-worker-no-process-sandbox]
      ports:
        - name: buildkit
          containerPort: 1234
      readinessProbe:
        exec:
          command: [buildctl, debug, workers]
        periodSeconds: 3
      securityContext:
        seccompProfile:
          type: Unconfined
        appArmorProfile:
          type: Unconfined
        runAsUser: 1000
        runAsGroup: 1000
      resources:
        requests:
          cpu: "{{.CPU}}"
          memory: {{.Memory}}
        limits:
          memory: {{.Memory}}
      volumeMounts:
        - name: buildkitd
          mountPath: /home/user/.local/share/buildkit
  volumes:
    - name: buildkitd
      emptyDir: {}
`))

// Manifest is the build pod's manifest.
func (p *PodBuilder) Manifest(name string) ([]byte, error) {
	var buf bytes.Buffer
	err := podTemplate.Execute(&buf, map[string]string{
		"Name": name, "Key": BuildNodeKey, "Image": p.image(), "Memory": p.Memory, "CPU": p.CPU, "Arch": p.arch(),
	})
	return buf.Bytes(), err
}

func (p *PodBuilder) image() string {
	if p.Image != "" {
		return p.Image
	}
	return DefaultImage
}

func (p *PodBuilder) arch() string {
	if p.Arch != "" {
		return p.Arch
	}
	return "arm64"
}

func (p *PodBuilder) kubectl(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	run := p.Kubectl
	if run == nil {
		run = execx.Run
	}
	c := execx.Cmd{Name: "kubectl", Args: append(args, "-n", p.Namespace)}
	if stdin != nil {
		c.Stdin = bytes.NewReader(stdin)
	}
	return run(ctx, c)
}

// Build starts the pod on first use, then builds on it.
func (p *PodBuilder) Build(ctx context.Context, b deploy.BuildSpec) (string, error) {
	if err := p.start(ctx); err != nil {
		return "", err
	}
	return p.Builder.Build(ctx, b)
}

func (p *PodBuilder) start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.name != "" {
		return nil
	}
	if p.Memory == "" || p.CPU == "" {
		return fmt.Errorf("build pods need memory and cpu set")
	}
	var id [4]byte
	_, _ = rand.Read(id[:])
	name := "buildkitd-" + hex.EncodeToString(id[:])
	doc, err := p.Manifest(name)
	if err != nil {
		return err
	}
	if p.Progress != nil {
		_, _ = fmt.Fprintf(p.Progress, "starting build pod %s (%s memory; a build node starts if none is free)\n", name, p.Memory)
	}
	if _, err := p.kubectl(ctx, doc, "apply", "-f", "-"); err != nil {
		return fmt.Errorf("starting the build pod: %w", err)
	}
	p.name = name
	ready := p.Ready
	if ready == 0 {
		ready = 15 * time.Minute
	}
	if _, err := p.kubectl(ctx, nil, "wait", "--for=condition=Ready", "pod/"+name, fmt.Sprintf("--timeout=%ds", int(ready.Seconds()))); err != nil {
		return fmt.Errorf("the build pod did not become ready: %w", err)
	}
	ip, err := p.kubectl(ctx, nil, "get", "pod/"+name, "-o", "jsonpath={.status.podIP}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(ip)) == "" {
		return fmt.Errorf("the build pod has no address")
	}
	p.Addr = "tcp://" + strings.TrimSpace(string(ip)) + ":1234"
	return nil
}

// Close deletes the build pod, if one was started.
func (p *PodBuilder) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.name == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_, err := p.kubectl(ctx, nil, "delete", "pod/"+p.name, "--wait=false", "--ignore-not-found")
	p.name, p.Addr = "", ""
	return err
}
