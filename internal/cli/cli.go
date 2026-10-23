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
	"flag"
	"fmt"
	"github.com/runui/oci-mirror-proxy/internal/admin"
	"io"
	"os"
	"runtime"
	"syscall"

	"github.com/runui/oci-mirror-proxy/internal/config"
)

// version is overridden at build time with -ldflags.
var version = "dev"

// errUsage marks mistakes in how a command was invoked. They exit with status 2.
var errUsage = errors.New("usage error")

// Run executes args, which excludes the program name, and returns the exit code.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}
	var err error
	switch args[0] {
	case "init":
		err = cmdInit(args[1:], stderr, stdout)
	case "serve":
		err = cmdServe(ctx, args[1:], stderr)
	case "status":
		err = cmdStatus(args[1:], stdout, stderr)
	case "server":
		err = cmdServer(args[1:], stdout, stderr)
	case "reload":
		err = cmdReload(args[1:], stderr)
	case "upstream":
		err = cmdUpstream(args[1:], stdout, stderr)
	case "remote":
		err = cmdRemote(ctx, args[1:], stdout, stderr)
	case "dockerd":
		err = cmdDockerd(ctx, args[1:], stdout, stderr)
	case "doctor":
		err = cmdDoctor(ctx, args[1:], stdout, stderr)
	case "version":
		fmt.Fprintln(stdout, version)
		return 0
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		printUsage(stderr)
		return 2
	}

	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
		return 0
	case errors.Is(err, errUsage):
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	default:
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `oci-mirror-proxy - a local registry mirror proxy for dockerd

Usage:
  oci-mirror-proxy <command> [flags]

Setup:
  init                 write a default configuration file
  dockerd plan         show how dockerd will be configured (writes nothing)
  dockerd apply        configure dockerd to use the proxy (needs root for system paths)
  server info          display client, server and configuration information (--json)
  dockerd revert       undo the last apply
  doctor               check that the proxy and dockerd are wired correctly

Running:
  serve                run the proxy in the foreground
  status               show upstream health from a running proxy
  reload               re-read the configuration file of a running proxy

Upstreams:
  upstream list|add|enable|disable|remove

Remote mirror catalogs:
  remote list|show|update|remove

Other:
  version              print the version
  help                 show this text

Run "oci-mirror-proxy <command> -h" for the flags of a command.
`)
}

// newFlagSet creates a FlagSet that reports errors to stderr.
func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("oci-mirror-proxy "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

// parseFlags parses args and rejects stray positional arguments.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%w: unexpected arguments: %v", errUsage, fs.Args())
	}
	return nil
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

func cmdInit(args []string, stderr, stdout io.Writer) error {
	fs := newFlagSet("init", stderr)
	configPath := fs.String("config", "", "configuration file to create (default: per-user location)")
	force := fs.Bool("force", false, "overwrite an existing file")
	adminSocket := fs.String("admin-socket", "", "admin socket path to write into the configuration")
	listen := fs.String("listen", "", "proxy listen address to write into the configuration")
	advertise := fs.String("advertise", "", "client-facing proxy address to write into the configuration")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
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
