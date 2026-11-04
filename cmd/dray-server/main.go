// Command dray-server is the Dray server: the MCP endpoint, the HTTP API, the
// approval page, and the reconcile loop. It arrives in milestone M1.2; until
// then it only reports its version.
package main

import (
	"fmt"
	"os"

	"github.com/BrutalSystems/dray/internal/cli"
)

func main() {
	if err := cli.NewRoot("dray-server", "Run the Dray server").Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
