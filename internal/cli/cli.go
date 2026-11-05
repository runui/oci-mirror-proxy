// Package cli implements the oci-mirror-proxy command line. Run takes its
// arguments and writers as parameters, so the whole interface is testable
// without starting a process.
// Client commands consume the admin API, not service YAML; only serve and init
// select local service configuration. Subscription interpretation stays here,
// behind fragmentClient storage. Adapters own Docker paths and activation. This
// layer owns compatibility and output, not persistence or implicit elevation.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"syscall"

	"github.com/runui/oci-mirror-proxy/internal/admin"
	"github.com/runui/oci-mirror-proxy/internal/config"
	"github.com/spf13/cobra"
)

// version is overridden at build time with -ldflags.
var version = "dev"

// errUsage marks mistakes in how a command was invoked. They exit with status 2.
var errUsage = errors.New("usage error")

// Run executes args, which excludes the program name, and returns the exit code.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	cmd := newRootCommand(ctx, stdout, stderr)
	normalized, err := prepareCommandArgs(cmd, args)
	if err == nil {
		// Cobra treats nil as "use os.Args"; Run(nil) must instead mean no
		// arguments, regardless of the embedding process's own command line.
		if normalized == nil {
			normalized = []string{}
		}
		cmd.SetArgs(normalized)
		_, err = cmd.ExecuteC()
	}
	// Cobra's own error/usage printing is silenced so business failures retain
	// exit 1, usage failures retain exit 2, and neither is printed twice.
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errUsage):
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	default:
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
}

// resolveConfigPath prefers user configuration over system configuration.
func resolveConfigPath(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	path, err := config.DefaultPath()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		return path, nil
	}
	return discoverConfigPath(path, "/etc/oci-mirror-proxy/config.yaml")
}

func discoverConfigPath(userPath, systemPath string) (string, error) {
	for _, path := range []string{userPath, systemPath} {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return userPath, nil
}

// resolveAdminSocket never reads service YAML. A permission or protocol error
// must not redirect a command to a different service instance.
func resolveAdminSocket(explicit, _ string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	user, err := config.DefaultAdminSocket()
	if err != nil {
		return "", err
	}
	candidates := []string{user}
	if runtime.GOOS == "windows" {
		candidates = append(candidates, `\\.\pipe\oci-mirror-proxy-admin`)
	} else {
		candidates = append(candidates, systemAdminSocket(runtime.GOOS))
	}
	return discoverAdminSocket(user, candidates)
}

func discoverAdminSocket(user string, candidates []string) (string, error) {
	var found string
	for _, endpoint := range candidates {
		if _, err := admin.NewClient(endpoint).Status(); err != nil {
			if errors.Is(err, os.ErrPermission) {
				return "", adminPermissionError(endpoint, err)
			}
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
				continue
			}
			return "", fmt.Errorf("discover admin endpoint %s (use --admin-socket to select explicitly): %w", endpoint, err)
		}
		if found != "" {
			return "", fmt.Errorf("multiple proxy services found; select one with --admin-socket")
		}
		found = endpoint
	}
	if found != "" {
		return found, nil
	}
	return user, nil
}

func adminPermissionError(endpoint string, err error) error {
	return fmt.Errorf("permission denied accessing admin endpoint %s; for a system service, check its endpoint permissions or retry with administrator privileges; for a private instance, use its owning account: %w", endpoint, err)
}

func systemAdminSocket(goos string) string {
	// macOS has no Linux /run directory. Keep legacy system configurations
	// consistent with launchd even when they omit an explicit admin socket.
	if goos == "darwin" {
		return "/var/run/oci-mirror-proxy/admin.sock"
	}
	return "/run/oci-mirror-proxy/admin.sock"
}

func newInitCommand(ctx context.Context, stdout, stderr io.Writer) *cobra.Command {
	cmd := leafCommand("init", "Write a default configuration file")
	fs := cmd.Flags()
	configPath := fs.String("config", "", "configuration file to create (default: per-user location)")
	force := fs.Bool("force", false, "overwrite an existing file")
	adminSocket := fs.String("admin-socket", "", "admin socket path to write into the configuration")
	listen := fs.String("listen", "", "proxy listen address to write into the configuration")
	advertise := fs.String("advertise", "", "client-facing proxy address to write into the configuration")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		path := *configPath
		var err error
		if path == "" {
			path, err = config.DefaultPath()
		}
		if err != nil {
			return err
		}
		if _, err := os.Stat(path); err == nil && !*force {
			return fmt.Errorf("%s already exists; use --force to overwrite it", path)
		}
		cfg := config.Default()
		cfg.Admin.Socket = *adminSocket
		if *listen != "" {
			cfg.Listen = *listen
		}
		cfg.Advertise = *advertise
		if err := config.Save(path, cfg); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "wrote %s\n", path)
		return nil
	}
	return cmd
}
