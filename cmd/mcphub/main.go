// Command mcphub is a gateway and control plane for Model Context Protocol
// servers: define your servers once, proxy them behind one connection, and
// sync them into every agent harness.
package main

import "github.com/abdul-hamid-achik/mcphub/internal/cli"

func main() {
	cli.Execute()
}
