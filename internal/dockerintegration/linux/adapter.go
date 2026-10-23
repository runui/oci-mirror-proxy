// Package linux implements system and rootless Docker Engine integration.
// It owns Engine paths and Unix activation, not service configuration or user
// backup storage. Rootless paths require an explicit runtime environment.
package linux

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/runui/oci-mirror-proxy/internal/daemonconf"
	"github.com/runui/oci-mirror-proxy/internal/dockerintegration"
)

type Adapter struct{ Locations dockerintegration.Paths }

func Defaults(rootless bool) (dockerintegration.Paths, error) {
	if !rootless {
		return dockerintegration.Paths{CertsDir: "/etc/docker/certs.d", DaemonJSON: "/etc/docker/daemon.json", DockerSocket: "/var/run/docker.sock", PidFile: "/var/run/docker.pid"}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return dockerintegration.Paths{}, err
	}
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if runtime == "" {
		return dockerintegration.Paths{}, fmt.Errorf("rootless setup needs XDG_RUNTIME_DIR to locate the dockerd socket")
	}
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		config = filepath.Join(home, ".config")
	}
	return dockerintegration.Paths{CertsDir: filepath.Join(config, "docker", "certs.d"), DaemonJSON: filepath.Join(config, "docker", "daemon.json"), DockerSocket: filepath.Join(runtime, "docker.sock"), PidFile: filepath.Join(runtime, "docker.pid")}, nil
}

func (a Adapter) Paths() dockerintegration.Paths { return a.Locations }
func (Adapter) Validate(context.Context) error   { return nil }
func (Adapter) RequiresRestart() bool            { return false }
func (a Adapter) Render(ctx context.Context, t daemonconf.Target, store string) (daemonconf.Plan, error) {
	var kind daemonconf.StoreKind
	switch store {
	case "containerd":
		kind = daemonconf.StoreContainerd
	case "legacy":
		kind = daemonconf.StoreLegacy
	case "", "auto":
		info, err := daemonconf.FetchInfo(ctx, a.Locations.DockerSocket)
		if err != nil {
			return daemonconf.Plan{}, fmt.Errorf("cannot detect the image store: %w; pass --store explicitly", err)
		}
		kind = daemonconf.DetectStore(info)
	default:
		return daemonconf.Plan{}, fmt.Errorf("--store must be auto, containerd or legacy")
	}
	if kind == daemonconf.StoreLegacy {
		return (daemonconf.LegacyRenderer{DaemonJSON: a.Locations.DaemonJSON}).Render(t, os.ReadFile)
	}
	return (daemonconf.ContainerdRenderer{CertsDir: a.Locations.CertsDir}).Render(t, os.ReadFile)
}
func (a Adapter) Activate(_ context.Context, _ bool) error {
	return daemonconf.Reload(a.Locations.PidFile)
}
