package config

// Environment is one place the app deploys to.
type Environment struct {
	// Target names an entry in targets: the cluster and cloud account.
	Target string `json:"target"`
	// Namespace is the Kubernetes namespace.
	Namespace string `json:"namespace" jsonschema:"pattern=^[a-z0-9]([-a-z0-9]*[a-z0-9])?$,maxLength=63"`
	// NamespaceMode is owned (the default: Dray labels the namespace as this
	// app's and refuses other apps there) or shared with other apps, which
	// then may change only resources they own.
	NamespaceMode string `json:"namespace_mode,omitempty" jsonschema:"enum=owned,enum=shared"`
	// Release is how changes reach the environment.
	Release string `json:"release" jsonschema:"enum=direct,enum=gitops,enum=feed"`
	// Approval is who must say yes before a change applies. Dray's own
	// settings can require more for an environment; a repo cannot require less.
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

// Target is a cluster in a cloud account. Credentials are not part of it:
// each person's (or the server's) own access is used.
type Target struct {
	// Cloud is the cloud provider.
	Cloud string `json:"cloud" jsonschema:"enum=aws"`
	// Account is the cloud account ID; images are pushed to its registry.
	Account string `json:"account,omitempty"`
	// Region is the cloud region.
	Region string `json:"region,omitempty"`
	// Cluster is the Kubernetes cluster, used as the kube context name unless
	// a person's own settings map it to another.
	Cluster string `json:"cluster"`
	// Platform is the build platform, such as linux/arm64.
	Platform string `json:"platform,omitempty"`
}

// SecretStore says where the repo's secret: references live.
type SecretStore struct {
	// Type is the adapter: openbao or file.
	Type    string        `json:"type" jsonschema:"enum=openbao,enum=file"`
	OpenBao *OpenBaoStore `json:"openbao,omitempty"`
	File    *FileStore    `json:"file,omitempty"`
}

// OpenBaoStore is an OpenBao (or Vault) KV version 2 engine. The token comes
// from the person's own sign-in, never from this file.
type OpenBaoStore struct {
	// Address is the server; defaults to BAO_ADDR, then VAULT_ADDR.
	Address string `json:"address,omitempty"`
	// Mount is the KV engine's mount; defaults to secret.
	Mount string `json:"mount,omitempty"`
	// Layout places secret:<app>/<name> in the mount, such as
	// {client}/{app}/{env}/{name}; defaults to {app}/{name}.
	Layout string `json:"layout,omitempty"`
	// Client fills {client} in the layout.
	Client string `json:"client,omitempty"`
	// Namespace is the OpenBao namespace, when the server uses them.
	Namespace string `json:"namespace,omitempty"`
}

// FileStore is a folder of secret files, for local development.
type FileStore struct {
	// Dir holds the files; a leading ~/ is the home folder.
	Dir string `json:"dir"`
}
