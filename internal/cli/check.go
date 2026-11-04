package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BrutalSystems/dray/internal/config"
	"github.com/BrutalSystems/dray/internal/render"
	"github.com/BrutalSystems/dray/internal/validate"
)

// NewCheckCmd is `dray check`: render a repo's workloads and check them, with
// no cluster or credentials, as CI can.
func NewCheckCmd() *cobra.Command {
	var repoConfig, kubeVersion string
	cmd := &cobra.Command{
		Use:   "check [repo]",
		Short: "Check a repo's config and manifests without deploying",
		Long: "Read a repo's .dray/config.yaml, render every workload that isn't disabled\n" +
			"(images as placeholders), and check each manifest against Kubernetes' schemas.\n" +
			"Needs no cluster and no credentials, so CI can run it. Exits non-zero on a\n" +
			"problem a deploy would stop on.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			c, err := loadRepoConfig(dir, repoConfig)
			if err != nil {
				return err
			}
			if kubeVersion == "" {
				kubeVersion = newestTargetVersion(c)
			}
			checker, err := validate.New(validate.Options{KubernetesVersion: kubeVersion})
			if err != nil {
				return err
			}
			findings, err := checkRepo(dir, c, checker)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			errs := 0
			for _, f := range findings {
				mark := "!"
				if f.Error {
					mark = "✗"
					errs++
				}
				_, _ = fmt.Fprintf(out, "%s %s\n", mark, f)
			}
			if errs > 0 {
				return fmt.Errorf("%d problem(s) a deploy would stop on", errs)
			}
			_, _ = fmt.Fprintf(out, "✓ %s: config and manifests check out\n", c.Name)
			return nil
		},
	}
	cmd.Flags().StringVar(&repoConfig, "repo-config", "", "read the repo's config from this file instead of .dray/config.yaml")
	cmd.Flags().StringVar(&kubeVersion, "kubernetes-version", "", "Kubernetes version to check against (default: the targets', else the newest)")
	return cmd
}

// placeholderDigest stands in for images that aren't built; it is a valid
// reference, so schemas accept it.
const placeholderDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

// checkRepo renders every workload that isn't disabled and checks it.
func checkRepo(dir string, c *config.RepoConfig, checker interface {
	Check(string, []byte) []validate.Finding
},
) ([]validate.Finding, error) {
	refs := map[string]string{}
	for _, img := range c.Images {
		refs[img.Name] = "registry.invalid/" + img.Name + "@" + placeholderDigest
	}
	for _, u := range c.Uses {
		refs[u.LocalName()] = "registry.invalid/" + u.LocalName() + "@" + placeholderDigest
	}
	var out []validate.Finding
	var errs []error
	for _, w := range c.Workloads {
		if w.Disabled {
			continue
		}
		res, err := render.Workload(dir, c, w, refs)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, m := range res.Manifests {
			out = append(out, checker.Check(m.Path, m.Content)...)
		}
	}
	return out, errors.Join(errs...)
}

// newestTargetVersion is the highest kubernetes_version among the targets.
func newestTargetVersion(c *config.RepoConfig) string {
	best := ""
	for _, t := range c.Targets {
		if versionLess(best, t.KubernetesVersion) {
			best = t.KubernetesVersion
		}
	}
	return best
}

func versionLess(a, b string) bool {
	pa, pb := strings.Split(strings.TrimPrefix(a, "v"), "."), strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			_, _ = fmt.Sscan(pa[i], &x)
		}
		if i < len(pb) {
			_, _ = fmt.Sscan(pb[i], &y)
		}
		if x != y {
			return x < y
		}
	}
	return false
}
