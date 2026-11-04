// Package cli holds the command tree shared by the dray and dray-server
// binaries. Each binary adds its own commands on top of NewRoot.
package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/BrutalSystems/dray/internal/version"
)

// NewRoot returns a root command with the version command and --version flag
// that both binaries share.
func NewRoot(name, short string) *cobra.Command {
	root := &cobra.Command{
		Use:           name,
		Short:         short,
		Version:       version.Get().String(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetVersionTemplate(name + " {{.Version}}\n")
	root.AddCommand(newVersionCmd(name))
	return root
}

func newVersionCmd(name string) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Show the version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := version.Get()
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(info)
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", name, info)
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the version as JSON")
	return cmd
}
