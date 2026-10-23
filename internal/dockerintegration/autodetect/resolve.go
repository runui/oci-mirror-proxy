// Package autodetect is the only place that selects platform adapters.
// It interprets local Docker context and explicit target overrides, not service
// renderer data. Selection never elevates privileges or chooses CLI storage.
package autodetect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/runui/oci-mirror-proxy/internal/daemonconf"
	"github.com/runui/oci-mirror-proxy/internal/dockerintegration"
	"github.com/runui/oci-mirror-proxy/internal/dockerintegration/desktopmac"
	"github.com/runui/oci-mirror-proxy/internal/dockerintegration/desktopwindows"
	linuxintegration "github.com/runui/oci-mirror-proxy/internal/dockerintegration/linux"
)

type Options struct {
	Rootless  bool
	Store     string
	Overrides dockerintegration.Paths
}

func Resolve(ctx context.Context, o Options) (dockerintegration.Adapter, error) {
	// Explicit file/store options preserve offline configuration generation. They
	// are not required for ordinary operation. Linux activation still follows the
	// existing --no-reload contract for explicitly generated configurations.
	offline := o.Overrides.CertsDir != "" || o.Overrides.PidFile != "" || (o.Store != "" && o.Store != "auto")
	var paths dockerintegration.Paths
	var err error
	desktop := false
	if !offline {
		socket, err := endpoint(ctx, o.Overrides.DockerSocket)
		if err != nil {
			return nil, err
		}
		info, err := daemonconf.FetchInfo(ctx, socket)
		if err != nil {
			return nil, fmt.Errorf("cannot detect Docker environment: %w; for offline configuration generation pass --store explicitly", err)
		}
		desktop = info.OperatingSystem == "Docker Desktop"
		if desktop {
			switch runtime.GOOS {
			case "darwin":
				paths, err = desktopmac.Defaults()
			case "windows":
				paths, err = desktopwindows.Defaults()
			default:
				return nil, fmt.Errorf("this Docker Desktop host is not supported")
			}
		} else {
			if runtime.GOOS != "linux" {
				return nil, fmt.Errorf("the selected engine cannot be configured locally on %s", runtime.GOOS)
			}
			paths, err = linuxintegration.Defaults(o.Rootless)
		}
		if err != nil {
			return nil, err
		}
		paths.DockerSocket = socket
	} else {
		if runtime.GOOS == "windows" {
			if o.Overrides.PidFile != "" {
				return nil, fmt.Errorf("--pid-file is only supported for Unix Docker engines")
			}
			paths, err = desktopwindows.OfflineDefaults(o.Rootless)
		} else {
			paths, err = linuxintegration.Defaults(o.Rootless)
		}
		if err != nil {
			return nil, err
		}
	}
	for _, field := range []struct {
		dst   *string
		value string
	}{
		{&paths.CertsDir, o.Overrides.CertsDir}, {&paths.DaemonJSON, o.Overrides.DaemonJSON},
		{&paths.DockerSocket, o.Overrides.DockerSocket}, {&paths.PidFile, o.Overrides.PidFile},
	} {
		if field.value != "" {
			*field.dst = field.value
		}
	}
	if desktop {
		if o.Rootless {
			return nil, fmt.Errorf("rootless overrides do not match the selected engine")
		}
		if runtime.GOOS == "windows" {
			return desktopwindows.Adapter{Adapter: desktopmac.Adapter{Locations: paths}}, nil
		}
		return desktopmac.Adapter{Locations: paths}, nil
	}
	if runtime.GOOS == "windows" {
		return desktopwindows.OfflineAdapter{Adapter: linuxintegration.Adapter{Locations: paths}}, nil
	}
	return linuxintegration.Adapter{Locations: paths}, nil
}

func endpoint(ctx context.Context, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	// DOCKER_CONTEXT takes precedence over DOCKER_HOST, matching Docker CLI.
	if host := os.Getenv("DOCKER_HOST"); host != "" && os.Getenv("DOCKER_CONTEXT") == "" {
		return localSocket(host)
	}
	out, err := exec.CommandContext(ctx, "docker", "context", "inspect").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("inspect Docker connection: %w: %s", err, out)
	}
	var contexts []struct {
		Endpoints map[string]struct{ Host string }
	}
	if err := json.Unmarshal(out, &contexts); err != nil {
		return "", err
	}
	if len(contexts) != 1 {
		return "", fmt.Errorf("expected one active Docker connection")
	}
	return localSocket(contexts[0].Endpoints["docker"].Host)
}

func localSocket(endpoint string) (string, error) {
	if runtime.GOOS == "windows" && strings.HasPrefix(endpoint, "npipe://") {
		path := strings.ReplaceAll(strings.TrimPrefix(endpoint, "npipe://"), "/", `\`)
		if strings.HasPrefix(strings.ToLower(path), `\\.\pipe\`) && len(path) > len(`\\.\pipe\`) {
			return path, nil
		}
	}
	path, ok := strings.CutPrefix(endpoint, "unix://")
	if !ok || !strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("refusing to modify local configuration for non-local Docker endpoint %q", endpoint)
	}
	return path, nil
}
