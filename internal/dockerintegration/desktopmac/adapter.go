// Package desktopmac integrates with the user-managed Docker Desktop Engine
// configuration, never with files inside its Linux VM.
// Desktop adapters only modify host-side Engine settings, never VM internals.
// Activation requires explicit restart consent; backup storage belongs to the
// CLI, and shared merge behavior must not assume the caller is privileged.
package desktopmac

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/runui/oci-mirror-proxy/internal/daemonconf"
	"github.com/runui/oci-mirror-proxy/internal/dockerintegration"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Adapter struct {
	Locations dockerintegration.Paths
	Docker    string
}

func Defaults() (dockerintegration.Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return dockerintegration.Paths{}, err
	}
	return dockerintegration.Paths{DaemonJSON: filepath.Join(home, ".docker", "daemon.json"), DockerSocket: filepath.Join(home, ".docker", "run", "docker.sock")}, nil
}

func (a Adapter) command(ctx context.Context, args ...string) ([]byte, error) {
	binary := a.Docker
	if binary == "" {
		binary = "docker"
	}
	out, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker %v: %w: %s", args, err, out)
	}
	return out, nil
}

func (a Adapter) Paths() dockerintegration.Paths { return a.Locations }
func (Adapter) RequiresRestart() bool            { return true }

func (a Adapter) Validate(ctx context.Context) error {
	// A local-looking endpoint is insufficient: also check its canonical path
	// and daemon identity before editing the user's Desktop configuration.
	if os.Getenv("DOCKER_HOST") != "" {
		return fmt.Errorf("Desktop integration refuses DOCKER_HOST; select the local Desktop context instead")
	}
	if os.Getenv("DOCKER_CONFIG") != "" {
		return fmt.Errorf("custom DOCKER_CONFIG is not supported for automatic local configuration")
	}
	defaults, err := Defaults()
	if err != nil {
		return err
	}
	expected, err := filepath.EvalSymlinks(defaults.DockerSocket)
	if err != nil {
		return fmt.Errorf("locate local Docker socket: %w", err)
	}
	actual, err := filepath.EvalSymlinks(a.Locations.DockerSocket)
	if err != nil || expected != actual {
		return fmt.Errorf("selected Docker socket is not the local Desktop socket")
	}
	out, err := a.command(ctx, "context", "inspect")
	if err != nil {
		return err
	}
	var contexts []struct {
		Endpoints map[string]struct{ Host string }
	}
	if err := json.Unmarshal(out, &contexts); err != nil {
		return err
	}
	if len(contexts) != 1 || contexts[0].Endpoints["docker"].Host != "unix://"+a.Locations.DockerSocket {
		return fmt.Errorf("current context does not point at the local Docker Desktop socket %s", a.Locations.DockerSocket)
	}
	info, err := daemonconf.FetchInfo(ctx, a.Locations.DockerSocket)
	if err != nil {
		return err
	}
	if info.OperatingSystem != "Docker Desktop" {
		return fmt.Errorf("target daemon is not Docker Desktop")
	}
	return nil
}

func (a Adapter) Render(_ context.Context, t daemonconf.Target, store string) (daemonconf.Plan, error) {
	if store != "" && store != "auto" {
		return daemonconf.Plan{}, fmt.Errorf("Desktop manages the image store; omit --store")
	}
	// Desktop accepts registry-mirrors even with the containerd image store.
	// Reuse the JSON merger, not Linux's store-to-config selection policy.
	data, err := os.ReadFile(a.Locations.DaemonJSON)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return daemonconf.Plan{}, err
	}
	p, err := (daemonconf.LegacyRenderer{DaemonJSON: a.Locations.DaemonJSON}).Render(t, func(string) ([]byte, error) { return data, err })
	if err != nil {
		return p, err
	}
	p.Store = daemonconf.StoreDesktop
	for i := range p.Files {
		p.Files[i].CheckBefore = new(string(data))
		p.Files[i].BeforeExists = err == nil
		p.Files[i].Mode = 0o600
		if info, err := os.Stat(a.Locations.DaemonJSON); err == nil {
			p.Files[i].Mode = info.Mode().Perm()
		}
	}
	p.Notes = []string{"Docker Desktop must restart to load Engine settings; use --restart or apply through Settings > Docker Engine"}
	p.Warnings = nil
	for _, route := range t.Routes {
		if route.Alias != "docker.io" {
			p.Warnings = append(p.Warnings, "Docker Desktop integration only configures Docker Hub; skipping "+route.Alias)
		}
	}
	return p, nil
}

func (a Adapter) Activate(ctx context.Context, restart bool) error {
	if !restart {
		return fmt.Errorf("Docker Desktop activation requires --restart")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if _, err := a.command(ctx, "desktop", "restart"); err != nil {
		return err
	}
	for {
		if info, err := daemonconf.FetchInfo(ctx, a.Locations.DockerSocket); err == nil {
			// Restart completion is not proof that the Engine loaded this file.
			// Verify the complete mirror list, including the empty list on revert.
			data, err := os.ReadFile(a.Locations.DaemonJSON)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			var cfg struct {
				Mirrors []string `json:"registry-mirrors"`
			}
			if len(data) != 0 {
				if err := json.Unmarshal(data, &cfg); err != nil {
					return err
				}
			}
			var actual []string
			if info.RegistryConfig != nil {
				actual = info.RegistryConfig.Mirrors
			}
			if len(cfg.Mirrors) != len(actual) {
				return fmt.Errorf("Docker restarted but did not load the expected mirror configuration")
			}
			for i, mirror := range cfg.Mirrors {
				if strings.TrimRight(mirror, "/") != strings.TrimRight(actual[i], "/") {
					return fmt.Errorf("Docker restarted but loaded different mirror settings")
				}
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for Docker after restart: %w", ctx.Err())
		case <-time.After(time.Second):
		}
	}
}
