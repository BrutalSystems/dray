// Command dray is the Dray CLI: the local dev loop, operator deploys, and
// break-glass. See docs/decisions.md for why it is built the way it is.
package main

import (
	"fmt"
	"os"

	"github.com/BrutalSystems/dray/internal/cli"
)

func main() {
	root := cli.NewRoot("dray", "Deploy code from Git to its targets")
	root.AddCommand(cli.NewConfigCmd())
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
