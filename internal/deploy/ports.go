package deploy

import (
	"context"
	"time"
)

// Registry is the container registry a target pulls from.
type Registry interface {
	// Host is the registry's address, such as registry.example.com.
	Host() string
	// Login lets the builder push.
	Login(ctx context.Context) error
	// Ensure creates the repository if it does not exist.
	Ensure(ctx context.Context, repo string) error
	// Digest is the digest the tag points at, or "" when there is no such tag
	// (or no such repository).
	Digest(ctx context.Context, repo, tag string) (string, error)
}

// BuildSpec is one image to build and push.
type BuildSpec struct {
	// Context is the build context folder.
	Context string
	// Dockerfile is the Dockerfile's path.
	Dockerfile string
	// Ref is where to push, registry/repo:tag.
	Ref string
	// Platform is the target platform, such as linux/arm64.
	Platform string
	// Args are build arguments. Values reach the builder through its
	// environment, never its command line.
	Args map[string]string
}

// Builder builds an image and pushes it.
type Builder interface {
	// Build builds and pushes, and returns the pushed image's digest.
	Build(ctx context.Context, b BuildSpec) (digest string, err error)
}

// OwnerLabel is the label on a namespace naming the app that owns it.
const OwnerLabel = "dray/app"

// NamespaceState is what the cluster says about the deploy's namespace.
type NamespaceState struct {
	Exists bool
	// Owner is the app named by the namespace's OwnerLabel, if any.
	Owner string
}

// Cluster is a Kubernetes namespace's view of a target.
type Cluster interface {
	// ReadNamespace reads the namespace.
	ReadNamespace(ctx context.Context) (NamespaceState, error)
	// Claim creates the namespace if needed and labels it as app's.
	Claim(ctx context.Context, app string) error
	// Diff shows what applying docs would change; changed is false when
	// nothing would.
	Diff(ctx context.Context, docs []byte) (changed bool, diff string, err error)
	// Apply applies docs.
	Apply(ctx context.Context, docs []byte) error
	// Deployment reads a Deployment's rollout state.
	Deployment(ctx context.Context, name string) (DeploymentState, error)
	// Restart starts a new rollout of a Deployment's current spec.
	Restart(ctx context.Context, name string) error
	// Wait waits until a Deployment's rollout finishes or timeout passes.
	Wait(ctx context.Context, name string, timeout time.Duration) error
	// Undo rolls a Deployment back to its previous revision.
	Undo(ctx context.Context, name string) error
}

// DeploymentState is what the cluster says about a Deployment's rollout.
type DeploymentState struct {
	Found              bool
	Generation         int64
	ObservedGeneration int64
	Replicas           int32
	Updated            int32
	Available          int32
	// DeadlineExceeded is Kubernetes itself giving up on the rollout: the
	// Progressing condition with reason ProgressDeadlineExceeded.
	DeadlineExceeded bool
}

// Rolling reports whether the controller is still rolling the Deployment out:
// it has not seen the latest spec yet, or pods are still converging on it.
func (s DeploymentState) Rolling() bool {
	if !s.Found {
		return false
	}
	if s.Generation != s.ObservedGeneration {
		return true
	}
	return s.Updated < s.Replicas || s.Available < s.Replicas
}

// Source is the code an image is built from.
type Source interface {
	// Commit is the repo's HEAD, short form, and whether the working tree has
	// uncommitted changes.
	Commit(ctx context.Context) (commit string, dirty bool, err error)
	// Workspace lays out the commit, with the build sources beside it, and
	// returns the folder holding the repo's checkout. With dirty set it uses
	// the working tree as it is instead.
	Workspace(ctx context.Context, dirty bool) (Workspace, error)
}

// Workspace is a checkout to build from.
type Workspace interface {
	// Root is the folder that holds the repo's checkout and its sources.
	Root() string
	// Repo is the repo's checkout inside Root.
	Repo() string
	// Close removes anything the workspace created.
	Close() error
}

// CI reports the CI runs of the repo being deployed.
type CI interface {
	// Commits are HEAD and its ancestors, newest first, full form.
	Commits(ctx context.Context) ([]string, error)
	// Runs are the repo's recent workflow runs.
	Runs(ctx context.Context) ([]CIRun, error)
}

// CIRun is one CI workflow run.
type CIRun struct {
	HeadSha      string `json:"headSha"`
	Status       string `json:"status"`
	Conclusion   string `json:"conclusion"`
	WorkflowName string `json:"workflowName"`
	URL          string `json:"url"`
}
