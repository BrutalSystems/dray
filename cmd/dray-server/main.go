// Command dray-server is the Dray server. Today it deploys on merge (serve);
// the MCP endpoint, the HTTP API, the approval page and the reconcile loop
// follow.
package main

import (
	"fmt"
	"os"

	"github.com/BrutalSystems/dray/internal/cli"
)

func main() {
	root := cli.NewRoot("dray-server", "Run the Dray server")
	root.AddCommand(cli.NewServeCmd())
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
