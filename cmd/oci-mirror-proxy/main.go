// Command oci-mirror-proxy runs a local registry mirror proxy and configures
// dockerd to use it.
package main

import (
	"context"
	"os"

	"github.com/runui/oci-mirror-proxy/internal/cli"
)

func main() {
	os.Exit(cli.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
