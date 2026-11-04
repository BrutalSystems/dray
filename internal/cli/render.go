package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BrutalSystems/dray/internal/config"
	"github.com/BrutalSystems/dray/internal/render"
)

// NewRenderCmd is `dray render`: print a repo's manifests with its images
// filled in, without deploying anything.
func NewRenderCmd() *cobra.Command {
	var (
		workloads []string
		images    []string
	)
	cmd := &cobra.Command{
		Use:   "render [repo]",
		Short: "Print a repo's manifests with image references filled in",
		Long: "Print the manifests of a repo's workloads (path is the repo, default the current\n" +
			"folder) with each image placeholder, such as ${API_IMAGE}, replaced by the\n" +
			"reference given with --image name=ref. Every workload except disabled ones is\n" +
			"rendered unless --workload names some. Deploys pin images by digest, so a\n" +
			"reference by tag is rendered with a warning.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			c, err := config.LoadRepoConfig(dir)
			if err != nil {
				return err
			}
			refs := map[string]string{}
			for _, kv := range images {
				name, ref, ok := strings.Cut(kv, "=")
				if !ok || name == "" || ref == "" {
					return fmt.Errorf("--image %q: want name=ref", kv)
				}
				refs[name] = ref
			}
			selected, err := pickWorkloads(c, workloads)
			if err != nil {
				return err
			}
			warn := func(format string, a ...any) {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: "+format+"\n", a...)
			}
			for name, ref := range refs {
				if !render.IsDigestRef(ref) {
					warn("image %s is referenced by tag (%s); deploys pin a digest", name, ref)
				}
			}
			var all []render.Manifest
			for _, w := range selected {
				res, err := render.Workload(dir, c, w, refs)
				if err != nil {
					return err
				}
				for _, u := range res.Unfilled {
					warn("workload %s: %s has no value", w.Name, u)
				}
				all = append(all, res.Manifests...)
			}
			return render.Write(cmd.OutOrStdout(), all)
		},
	}
	cmd.Flags().StringArrayVar(&workloads, "workload", nil, "render only this workload (repeatable)")
	cmd.Flags().StringArrayVar(&images, "image", nil, "image reference as name=ref (repeatable)")
	return cmd
}

func pickWorkloads(c *config.RepoConfig, names []string) ([]config.Workload, error) {
	if len(names) == 0 {
		var out []config.Workload
		for _, w := range c.Workloads {
			if !w.Disabled {
				out = append(out, w)
			}
		}
		return out, nil
	}
	var out []config.Workload
	for _, n := range names {
		found := false
		for _, w := range c.Workloads {
			if w.Name != n {
				continue
			}
			if w.Disabled {
				return nil, fmt.Errorf("workload %s is disabled", n)
			}
			out, found = append(out, w), true
		}
		if !found {
			return nil, fmt.Errorf("unknown workload %q", n)
		}
	}
	return out, nil
}
