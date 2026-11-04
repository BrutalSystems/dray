// Package kubectl is the deploy.Cluster adapter that drives kubectl. It
// applies server-side, as field manager "dray", so the cluster computes what
// changes and keeps track of which fields Dray owns.
package kubectl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/BrutalSystems/dray/internal/deploy"
	"github.com/BrutalSystems/dray/internal/execx"
)

// FieldManager is the name Dray applies under.
const FieldManager = "dray"

// Cluster is one namespace in one kube context.
type Cluster struct {
	Context   string
	Namespace string
	// Run runs kubectl; nil is execx.Run.
	Run execx.Runner
	// Progress receives rollout status output; nil discards it.
	Progress io.Writer
}

var _ deploy.Cluster = (*Cluster)(nil)

func (c *Cluster) run(ctx context.Context, stdin []byte, stream bool, args ...string) ([]byte, error) {
	run := c.Run
	if run == nil {
		run = execx.Run
	}
	cmd := execx.Cmd{Name: "kubectl", Args: append(args, "-n", c.Namespace, "--context", c.Context)}
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	if stream && c.Progress != nil {
		cmd.Stream = c.Progress
	}
	return run(ctx, cmd)
}

var ssa = []string{"--server-side", "--field-manager=" + FieldManager, "--force-conflicts"}

// Diff runs kubectl diff, which exits 1 when there are differences.
func (c *Cluster) Diff(ctx context.Context, docs []byte) (bool, string, error) {
	out, err := c.run(ctx, docs, false, append([]string{"diff"}, append(ssa, "-f", "-")...)...)
	switch execx.ExitCode(err) {
	case -1:
		if err != nil {
			return false, "", err
		}
		return false, "", nil
	case 1:
		return true, tidyDiff(out), nil
	default:
		return false, "", err
	}
}

// Apply applies server-side.
func (c *Cluster) Apply(ctx context.Context, docs []byte) error {
	_, err := c.run(ctx, docs, false, append([]string{"apply"}, append(ssa, "-f", "-")...)...)
	return err
}

type deployment struct {
	Metadata struct {
		Generation int64 `json:"generation"`
	} `json:"metadata"`
	Spec struct {
		Replicas *int32 `json:"replicas"`
	} `json:"spec"`
	Status struct {
		ObservedGeneration int64 `json:"observedGeneration"`
		UpdatedReplicas    int32 `json:"updatedReplicas"`
		AvailableReplicas  int32 `json:"availableReplicas"`
		Conditions         []struct {
			Type   string `json:"type"`
			Reason string `json:"reason"`
		} `json:"conditions"`
	} `json:"status"`
}

// Deployment reads a Deployment's rollout state.
func (c *Cluster) Deployment(ctx context.Context, name string) (deploy.DeploymentState, error) {
	out, err := c.run(ctx, nil, false, "get", "deployment/"+name, "-o", "json")
	if err != nil {
		var e *execx.Error
		if errors.As(err, &e) && strings.Contains(e.Stderr, "NotFound") {
			return deploy.DeploymentState{}, nil
		}
		return deploy.DeploymentState{}, err
	}
	return parseDeployment(out)
}

func parseDeployment(data []byte) (deploy.DeploymentState, error) {
	var d deployment
	if err := json.Unmarshal(data, &d); err != nil {
		return deploy.DeploymentState{}, fmt.Errorf("reading the deployment: %w", err)
	}
	st := deploy.DeploymentState{
		Found: true, Generation: d.Metadata.Generation, ObservedGeneration: d.Status.ObservedGeneration,
		Replicas: 1, Updated: d.Status.UpdatedReplicas, Available: d.Status.AvailableReplicas,
	}
	if d.Spec.Replicas != nil {
		st.Replicas = *d.Spec.Replicas
	}
	for _, cond := range d.Status.Conditions {
		if cond.Type == "Progressing" && cond.Reason == "ProgressDeadlineExceeded" {
			st.DeadlineExceeded = true
		}
	}
	return st, nil
}

// ReadNamespace reads the namespace and its owner label.
func (c *Cluster) ReadNamespace(ctx context.Context) (deploy.NamespaceState, error) {
	out, err := c.run(ctx, nil, false, "get", "namespace", c.Namespace, "-o", "json")
	if err != nil {
		var e *execx.Error
		if errors.As(err, &e) && strings.Contains(e.Stderr, "NotFound") {
			return deploy.NamespaceState{}, nil
		}
		return deploy.NamespaceState{}, err
	}
	var ns struct {
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(out, &ns); err != nil {
		return deploy.NamespaceState{}, fmt.Errorf("reading namespace %s: %w", c.Namespace, err)
	}
	return deploy.NamespaceState{Exists: true, Owner: ns.Metadata.Labels[deploy.OwnerLabel]}, nil
}

// Claim creates or labels the namespace, applied server-side like the rest.
func (c *Cluster) Claim(ctx context.Context, app string) error {
	doc := fmt.Sprintf("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: %s\n  labels:\n    %s: %s\n", c.Namespace, deploy.OwnerLabel, app)
	return c.Apply(ctx, []byte(doc))
}

// Restart starts a new rollout.
func (c *Cluster) Restart(ctx context.Context, name string) error {
	_, err := c.run(ctx, nil, false, "rollout", "restart", "deployment/"+name)
	return err
}

// Wait waits for the rollout, showing its progress.
func (c *Cluster) Wait(ctx context.Context, name string, timeout time.Duration) error {
	_, err := c.run(ctx, nil, true, "rollout", "status", "deployment/"+name, fmt.Sprintf("--timeout=%ds", int(timeout.Seconds())))
	return err
}

// Undo rolls back to the previous revision.
func (c *Cluster) Undo(ctx context.Context, name string) error {
	_, err := c.run(ctx, nil, false, "rollout", "undo", "deployment/"+name)
	return err
}

// tidyDiff makes kubectl's diff readable: the temp-file headers become the
// object's name, and very long lines (such as the last-applied annotation)
// are cut short.
func tidyDiff(out []byte) string {
	var b strings.Builder
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "diff -u -N "):
			fields := strings.Fields(line)
			b.WriteString("── " + objectName(fields[len(fields)-1]) + "\n")
			continue
		case strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "+++ "):
			continue
		}
		if len(line) > 160 {
			line = line[:160] + "…"
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// objectName turns kubectl's temp file name, such as
// apps.v1.Deployment.shop.api, into Deployment/api.
func objectName(file string) string {
	base := file[strings.LastIndexAny(file, "/\\")+1:]
	parts := strings.Split(base, ".")
	for i, p := range parts {
		if p != "" && p[0] >= 'A' && p[0] <= 'Z' && i+2 < len(parts) {
			return p + "/" + strings.Join(parts[i+2:], ".")
		}
	}
	return base
}
