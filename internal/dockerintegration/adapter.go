// Package dockerintegration isolates target-specific Docker configuration and
// activation from the command workflow. A target is not necessarily the host OS.
package dockerintegration

import (
	"context"

	"github.com/runui/oci-mirror-proxy/internal/daemonconf"
)

type Paths struct {
	CertsDir, DaemonJSON, StateDir, DockerSocket, PidFile string
}

// Adapter owns the target's configuration strategy and activation mechanism.
// Callers must not infer either from the reported image store.
type Adapter interface {
	Paths() Paths
	Render(context.Context, daemonconf.Target, string) (daemonconf.Plan, error)
	Activate(context.Context, bool) error
	Validate(context.Context) error
	RequiresRestart() bool
}
