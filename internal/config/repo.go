// Package config defines Dray's configuration files and checks them:
// a repo's .dray/config.yaml (what to build and deploy) and a registry
// entry (where an app may go and who approves it).
package config

// RepoConfig is a repo's .dray/config.yaml: what to build and what to deploy.
// It says nothing about clusters, accounts or approvals; those live in the
// app's registry entry.
type RepoConfig struct {
	// Name is the app's name. It must match the app's registry entry.
	Name string `json:"name" jsonschema:"pattern=^[a-z0-9]([-a-z0-9]*[a-z0-9])?$,maxLength=63"`
	// Images are built from this repo.
	Images []Image `json:"images,omitempty"`
	// Uses pins shared components that this app deploys but does not build.
	Uses []Use `json:"uses,omitempty"`
	// Workloads are what gets deployed: manifests plus the images stamped into them.
	Workloads []Workload `json:"workloads,omitempty"`
	// Secrets are Kubernetes Secrets the workloads need, filled from the secret store.
	Secrets []Secret `json:"secrets,omitempty"`
	// Pilets are Piral micro-frontends published to a feed.
	Pilets *Pilets `json:"pilets,omitempty"`
}

// Image is a container image built from this repo.
type Image struct {
	// Name is how workloads refer to the image.
	Name string `json:"name" jsonschema:"pattern=^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"`
	// Repository is the container repository name; defaults to Name.
	Repository string `json:"repository,omitempty"`
	// Dockerfile is relative to the repo root; defaults to "Dockerfile".
	Dockerfile string `json:"dockerfile,omitempty"`
	// Context is the build context, relative to the repo root; defaults to ".".
	Context string `json:"context,omitempty"`
	// TemplateVar is the placeholder manifests use for this image; defaults to
	// NAME_IMAGE (upper snake case), as in ${API_IMAGE}.
	TemplateVar string `json:"template_var,omitempty" jsonschema:"pattern=^[A-Z_][A-Z0-9_]*$"`
	// BuildArgs are passed to the build as --build-arg values.
	BuildArgs *BuildArgs `json:"build_args,omitempty"`
	// BuildPerEnvironment rebuilds the image for each environment from the same
	// commit instead of promoting one image. Set it for front ends that bake
	// environment settings in at build time.
	BuildPerEnvironment bool `json:"build_per_environment,omitempty"`
	// Sources are other repos the build needs beside this one, such as a shared
	// library the projects reference by relative path. Dray clones this repo
	// into a folder named after its repository and each source into its Path
	// next to it, so a context of ".." is that shared folder.
	Sources []BuildSource `json:"sources,omitempty"`
}

// BuildSource is another repo checked out next to this one for a build,
// pinned to a commit. Upgrading it is a one-line change in a pull request.
type BuildSource struct {
	// Repo is the GitHub repository, owner/name.
	Repo string `json:"repo" jsonschema:"pattern=^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$"`
	// Path is the folder it is cloned into, next to this repo's folder.
	Path string `json:"path" jsonschema:"pattern=^[A-Za-z0-9_][A-Za-z0-9_.-]*$"`
	// Commit pins the exact commit.
	Commit string `json:"commit" jsonschema:"pattern=^[0-9a-f]{7,40}$"`
}

// BuildArgs are build-time values. Anything here can end up in the image, and
// in a front end's case in a public bundle, so never put secrets here.
type BuildArgs struct {
	// EnvFile is a dotenv file in the repo, such as .env.production.
	EnvFile string `json:"env_file,omitempty"`
	// Prefix keeps only variables whose names start with it, such as VITE_.
	Prefix string `json:"prefix,omitempty"`
	// Values are explicit name=value pairs.
	Values map[string]string `json:"values,omitempty"`
	// FromSecret reads dotenv-style values from the secret store.
	FromSecret SecretRef `json:"from_secret,omitempty"`
}

// Use pins a shared component this app deploys. Exactly one of Version or
// Commit is set.
type Use struct {
	// Component is the component's registry name, such as jobs-service.
	Component string `json:"component" jsonschema:"pattern=^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"`
	// Version is a released version of the component, such as 1.8.0.
	Version string `json:"version,omitempty"`
	// Commit pins a commit instead of a version, for components that have not
	// started tagging releases yet.
	Commit string `json:"commit,omitempty" jsonschema:"pattern=^[0-9a-f]{7,40}$"`
	// As is the name workloads use for this image; defaults to Component.
	As string `json:"as,omitempty" jsonschema:"pattern=^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"`
	// TemplateVar is the placeholder manifests use; defaults to AS_IMAGE.
	TemplateVar string `json:"template_var,omitempty" jsonschema:"pattern=^[A-Z_][A-Z0-9_]*$"`
}

// LocalName is the name workloads use for the component's image.
func (u Use) LocalName() string {
	if u.As != "" {
		return u.As
	}
	return u.Component
}

// WorkloadKind is the kind of Kubernetes workload.
type WorkloadKind string

// Workload kinds.
const (
	KindDeployment WorkloadKind = "deployment"
	KindCronJob    WorkloadKind = "cronjob"
)

// Workload is a set of manifests deployed together.
type Workload struct {
	// Name is the workload's name; for a deployment, the Deployment's name.
	Name string `json:"name" jsonschema:"pattern=^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"`
	// Kind decides how Dray waits for it: deployments roll out, cronjobs are applied.
	Kind WorkloadKind `json:"kind" jsonschema:"enum=deployment,enum=cronjob"`
	// Images are stamped into the manifests by digest. Names refer to images
	// built here or to components in uses. Empty for a workload whose images are
	// all third-party.
	Images []string `json:"images,omitempty"`
	// Manifests are relative to the repo root.
	Manifests []string `json:"manifests" jsonschema:"minItems=1"`
	// DependsOn lists workloads that must be applied first.
	DependsOn []string `json:"depends_on,omitempty"`
	// Secrets lists secrets (by name) that must exist before this workload.
	Secrets []string `json:"secrets,omitempty"`
	// Manual workloads ship only when named, never in a whole-repo deploy.
	Manual bool `json:"manual,omitempty"`
	// Disabled workloads are kept in the config but never deployed.
	Disabled bool `json:"disabled,omitempty"`
	// RolloutTimeout is how long to wait for a rollout; defaults to 600s,
	// Kubernetes' own progress deadline.
	RolloutTimeout *Duration `json:"rollout_timeout,omitempty"`
}

// Secret is a Kubernetes Secret filled from the secret store.
type Secret struct {
	// Name is the Kubernetes Secret's name.
	Name string `json:"name" jsonschema:"pattern=^[a-z0-9]([-.a-z0-9]*[a-z0-9])?$"`
	// From is where its values live in the secret store.
	From SecretRef `json:"from"`
}

// Pilets are Piral micro-frontends published from this repo.
type Pilets struct {
	// Names are the pilets this repo publishes.
	Names []string `json:"names" jsonschema:"minItems=1"`
	// Command publishes one pilet; "{name}" is replaced with the pilet's name.
	Command []string `json:"command" jsonschema:"minItems=1"`
	// Env is passed to the command as environment variables, such as the feed key.
	Env SecretRef `json:"env,omitempty"`
}
