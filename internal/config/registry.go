package config

// AppKind says whether a registry entry deploys or only publishes versions.
type AppKind string

// App kinds.
const (
	// KindApp deploys to environments.
	KindApp AppKind = "app"
	// KindComponent publishes versioned images that apps pin; it never deploys
	// on its own.
	KindComponent AppKind = "component"
)

// RegistryEntry is an app's file in the registry: which repo it is, where it
// may deploy, and who approves. Only a person merges one; it is the allowlist.
type RegistryEntry struct {
	// Name is the app's name; it matches the repo's .dray/config.yaml.
	Name string `json:"name" jsonschema:"pattern=^[a-z0-9]([-a-z0-9]*[a-z0-9])?$,maxLength=63"`
	// Repo is the GitHub repository, owner/name.
	Repo string `json:"repo" jsonschema:"pattern=^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$"`
	// Path is the app's folder inside the repo, when it is not the root.
	Path string `json:"path,omitempty"`
	// Kind is app (the default) or component.
	Kind AppKind `json:"kind,omitempty" jsonschema:"enum=app,enum=component"`
	// ReleaseOn says when a component builds a version: on a version tag.
	ReleaseOn string `json:"release_on,omitempty" jsonschema:"enum=tag"`
	// Environments are where an app may deploy, by name (dev, staging, prod).
	Environments map[string]Environment `json:"environments,omitempty"`
	// ThirdParty lists upstream images this app may run, mirrored by digest.
	ThirdParty []string `json:"third_party,omitempty"`
}

// EffectiveKind is Kind with its default applied.
func (e RegistryEntry) EffectiveKind() AppKind {
	if e.Kind == "" {
		return KindApp
	}
	return e.Kind
}

// Environment is one place an app may deploy.
type Environment struct {
	// Target names a cluster and account from the server's config.
	Target string `json:"target"`
	// Namespace is the Kubernetes namespace.
	Namespace string `json:"namespace" jsonschema:"pattern=^[a-z0-9]([-a-z0-9]*[a-z0-9])?$,maxLength=63"`
	// NamespaceMode is owned (the app's alone, the default) or shared with
	// other apps, which then may change only resources they own.
	NamespaceMode string `json:"namespace_mode,omitempty" jsonschema:"enum=owned,enum=shared"`
	// Release is how changes reach the environment.
	Release string `json:"release" jsonschema:"enum=direct,enum=gitops,enum=feed"`
	// Approval is who must say yes before a change applies.
	Approval string `json:"approval" jsonschema:"enum=none,enum=passkey,enum=chat"`
	// CIRequired refuses to deploy a commit unless CI passed on it.
	CIRequired bool `json:"ci_required,omitempty"`
	// AllowDirty lets an operator's CLI deploy uncommitted changes here, for
	// a dev environment. Never set it for production.
	AllowDirty bool `json:"allow_dirty,omitempty"`
	// OnMerge deploys when CI finishes green on the main branch; for an
	// environment that needs approval it only opens the request.
	OnMerge string `json:"on_merge,omitempty" jsonschema:"enum=off,enum=deploy"`
	// PromoteFrom is the environment whose verified artifact this one takes.
	PromoteFrom string `json:"promote_from,omitempty"`
}

// Target is a cluster in a cloud account, named in registry entries.
type Target struct {
	// Name is how registry entries refer to it.
	Name string `json:"name"`
	// Cloud is the cloud provider.
	Cloud string `json:"cloud" jsonschema:"enum=aws"`
	// Account is the cloud account ID.
	Account string `json:"account,omitempty"`
	// Region is the cloud region.
	Region string `json:"region,omitempty"`
	// Cluster is the Kubernetes context or cluster name.
	Cluster string `json:"cluster,omitempty"`
	// Profile is a local credentials profile, for the CLI only.
	Profile string `json:"profile,omitempty"`
	// Platform is the build platform, such as linux/arm64.
	Platform string `json:"platform,omitempty"`
}
