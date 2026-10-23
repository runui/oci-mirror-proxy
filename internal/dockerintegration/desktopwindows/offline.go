package desktopwindows

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/runui/oci-mirror-proxy/internal/daemonconf"
	"github.com/runui/oci-mirror-proxy/internal/dockerintegration"
	linuxintegration "github.com/runui/oci-mirror-proxy/internal/dockerintegration/linux"
)

// OfflineAdapter preserves explicit renderer/file workflows without pretending
// that a Windows daemon can be activated with a Unix signal or PID file.
type OfflineAdapter struct{ linuxintegration.Adapter }

func OfflineDefaults(rootless bool) (dockerintegration.Paths, error) {
	if rootless {
		return dockerintegration.Paths{}, fmt.Errorf("rootless dockerd is only supported on Linux")
	}
	data := os.Getenv("ProgramData")
	if data == "" {
		return dockerintegration.Paths{}, fmt.Errorf("ProgramData is required to locate Windows Docker configuration")
	}
	return dockerintegration.Paths{
		DaemonJSON:   filepath.Join(data, "docker", "config", "daemon.json"),
		CertsDir:     filepath.Join(data, "docker", "certs.d"),
		DockerSocket: `\\.\pipe\docker_engine`,
	}, nil
}

func (OfflineAdapter) RequiresRestart() bool { return true }

func (a OfflineAdapter) Render(ctx context.Context, target daemonconf.Target, store string) (daemonconf.Plan, error) {
	plan, err := a.Adapter.Render(ctx, target, store)
	if err == nil && plan.NeedsReload {
		plan.Notes = []string{"restart the selected Docker engine manually to load registry-mirrors"}
	}
	return plan, err
}

func (OfflineAdapter) Activate(context.Context, bool) error {
	return fmt.Errorf("restart the selected Windows Docker engine manually; automatic activation requires a detected Docker Desktop Linux engine")
}
