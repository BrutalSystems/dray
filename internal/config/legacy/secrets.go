package legacy

// SecretSource is where the JS tool kept one secret's values. Path is the
// logical path Convert's secret: references use, so importing every source
// fills exactly what the converted config asks for.
type SecretSource struct {
	Path string
	// File is relative to the repo.
	File string
	// Kind is the JS tool's secret kind (sops-manifest, literal-from-env,
	// sops-exec-env), or sops-dotenv for build values.
	Kind string
	// SecretName is the Kubernetes Secret's name, for sops-manifest.
	SecretName string
	// Prefix filters build values by name.
	Prefix string
}

// SecretPath is the logical path Convert gives an app's secret.
func SecretPath(app, name string) string { return app + "/" + name }

// SecretSources lists every secret an old config reads: its secrets, the
// sops env files its images build with, and its pilets' publish values.
func SecretSources(old *Config) []SecretSource {
	var out []SecretSource
	for _, s := range old.Secrets {
		out = append(out, SecretSource{Path: SecretPath(old.Name, s.Name), File: s.File, Kind: s.Kind, SecretName: s.Name})
	}
	for _, img := range old.Images {
		if img.Source.Git != "" || img.BuildArgs == nil || img.BuildArgs.SopsEnvFile == "" {
			continue
		}
		out = append(out, SecretSource{
			Path: SecretPath(old.Name, "build-"+img.Name), File: img.BuildArgs.SopsEnvFile,
			Kind: "sops-dotenv", Prefix: img.BuildArgs.Prefix,
		})
	}
	if p := old.Pilets; p != nil && len(p.Names) > 0 && p.SecretsFile != "" {
		out = append(out, SecretSource{Path: SecretPath(old.Name, "pilets"), File: p.SecretsFile, Kind: "sops-exec-env"})
	}
	return out
}
