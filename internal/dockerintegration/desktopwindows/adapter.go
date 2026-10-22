// Package desktopwindows configures the user-managed Docker Desktop Engine
// settings on Windows, never files inside its Linux VM.
package desktopwindows

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/runui/oci-mirror-proxy/internal/daemonconf"
	"github.com/runui/oci-mirror-proxy/internal/dockerintegration"
	"github.com/runui/oci-mirror-proxy/internal/dockerintegration/desktopmac"
)

// Reuse the Desktop JSON merger and explicit restart/verification workflow.
type Adapter struct{ desktopmac.Adapter }

func Defaults() (dockerintegration.Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return dockerintegration.Paths{}, err
	}
	state, err := os.UserConfigDir()
	if err != nil {
		return dockerintegration.Paths{}, err
	}
	return dockerintegration.Paths{
		DaemonJSON:   filepath.Join(home, ".docker", "daemon.json"),
		StateDir:     filepath.Join(state, "oci-mirror-proxy", "desktop-state"),
		DockerSocket: `\\.\pipe\dockerDesktopLinuxEngine`,
	}, nil
}

func (a Adapter) Validate(ctx context.Context) error {
	if os.Getenv("DOCKER_HOST") != "" {
		return fmt.Errorf("Desktop integration refuses DOCKER_HOST; select the local Desktop context instead")
	}
	if os.Getenv("DOCKER_CONFIG") != "" {
		return fmt.Errorf("custom DOCKER_CONFIG is not supported for automatic local configuration")
	}
	pipe := strings.ToLower(strings.ReplaceAll(a.Locations.DockerSocket, "/", `\`))
	if pipe != `\\.\pipe\dockerdesktoplinuxengine` && pipe != `\\.\pipe\docker_engine` {
		return fmt.Errorf("selected Docker pipe is not a local Docker Desktop Linux engine")
	}
	binary := a.Docker
	if binary == "" {
		binary = "docker"
	}
	out, err := exec.CommandContext(ctx, binary, "context", "inspect").CombinedOutput()
	if err != nil {
		return fmt.Errorf("inspect Docker context: %w: %s", err, out)
	}
	var contexts []struct {
		Endpoints map[string]struct{ Host string }
	}
	if err := json.Unmarshal(out, &contexts); err != nil {
		return err
	}
	if len(contexts) != 1 {
		return fmt.Errorf("expected one active Docker connection")
	}
	host := strings.ReplaceAll(contexts[0].Endpoints["docker"].Host, "/", `\`)
	if !strings.HasPrefix(strings.ToLower(host), `npipe:\\`) || !strings.EqualFold(host[len(`npipe:\\`):], pipe) {
		return fmt.Errorf("current context does not point at the selected local Desktop pipe")
	}
	info, err := daemonconf.FetchInfo(ctx, a.Locations.DockerSocket)
	if err != nil {
		return err
	}
	if info.OperatingSystem != "Docker Desktop" || info.OSType != "linux" {
		return fmt.Errorf("target daemon is not the Docker Desktop Linux engine; switch to Linux containers")
	}
	return nil
}
