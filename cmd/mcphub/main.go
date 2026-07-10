// Command mcphub is a gateway and control plane for Model Context Protocol
// servers: define your servers once, proxy them behind one connection, and
// sync them into every agent harness.
package main

import (
	"fmt"
	"os"

	"github.com/abdul-hamid-achik/mcphub/internal/cli"
	"github.com/abdul-hamid-achik/mcphub/internal/runtimepath"
)

func main() {
	if err := runtimepath.Apply(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "mcphub: configure runtime PATH: %v\n", err)
	}
	cli.Execute()
}
