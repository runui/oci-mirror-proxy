// Package cli implements the oci-mirror-proxy command line. Run takes its
// arguments and writers as parameters, so the whole interface is testable
// without starting a process.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

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
	case "reload":
		err = cmdReload(args[1:], stderr)
	case "upstream":
		err = cmdUpstream(args[1:], stdout, stderr)
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
  dockerd revert       undo the last apply
  doctor               check that the proxy and dockerd are wired correctly

Running:
  serve                run the proxy in the foreground
  status               show upstream health from a running proxy
  reload               re-read the configuration file of a running proxy

Upstreams:
  upstream list|add|enable|disable|remove

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

// resolveConfigPath returns the explicit path, or the per-user default.
func resolveConfigPath(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	return config.DefaultPath()
}

// resolveAdminSocket returns the explicit path, or the per-user default.
func resolveAdminSocket(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	return config.DefaultAdminSocket()
}

// newLogger creates a text logger at the requested level.
func newLogger(w io.Writer, level string) (*slog.Logger, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("%w: invalid log level %q (want debug, info, warn or error)", errUsage, level)
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: lvl})), nil
}

func cmdInit(args []string, stderr, stdout io.Writer) error {
	fs := newFlagSet("init", stderr)
	configPath := fs.String("config", "", "configuration file to create (default: per-user location)")
	force := fs.Bool("force", false, "overwrite an existing file")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	path, err := resolveConfigPath(*configPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil && !*force {
		return fmt.Errorf("%s already exists; use --force to overwrite it", path)
	}
	if err := config.Save(path, config.Default()); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "wrote %s\n", path)
	return nil
}
