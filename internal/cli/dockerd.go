package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"

	"github.com/runui/oci-mirror-proxy/internal/config"
	"github.com/runui/oci-mirror-proxy/internal/daemonconf"
	"github.com/runui/oci-mirror-proxy/internal/dockerintegration"
	"github.com/runui/oci-mirror-proxy/internal/dockerintegration/autodetect"
	linuxintegration "github.com/runui/oci-mirror-proxy/internal/dockerintegration/linux"
)

// dockerPaths are the locations that the dockerd integration reads and writes.
type dockerPaths = dockerintegration.Paths

// defaultDockerPaths preserves the Linux path contract for explicit offline plans.
func defaultDockerPaths(rootless bool) (dockerPaths, error) {
	return linuxintegration.Defaults(rootless)
}

// dockerFlags are the flags shared by every dockerd and doctor subcommand.
type dockerFlags struct {
	restart    bool
	adapter    dockerintegration.Adapter
	configPath string
	store      string
	rootless   bool
	certsDir   string
	daemonJSON string
	stateDir   string
	dockerSock string
	pidFile    string
	force      bool
	noReload   bool
}

func (f *dockerFlags) register(fs *flag.FlagSet, withApplyFlags bool) {
	fs.StringVar(&f.configPath, "config", "", "proxy configuration file (default: per-user location)")
	fs.StringVar(&f.store, "store", "auto", "image store: auto, containerd or legacy")
	fs.BoolVar(&f.rootless, "rootless", false, "target a rootless dockerd (per-user paths)")
	fs.StringVar(&f.certsDir, "certs-dir", "", "override the containerd certs directory")
	fs.StringVar(&f.daemonJSON, "daemon-json", "", "override the daemon.json path")
	fs.StringVar(&f.dockerSock, "docker-socket", "", "override the dockerd socket path")
	fs.StringVar(&f.pidFile, "pid-file", "", "override the dockerd pid file path")
	if withApplyFlags {
		fs.BoolVar(&f.restart, "restart", false, "allow restarting Docker to activate settings (interrupts containers)")
		fs.StringVar(&f.stateDir, "state-dir", "", "override where apply keeps its state and backups")
		fs.BoolVar(&f.force, "force", false, "take over files that were not created by this tool (they are backed up first)")
		fs.BoolVar(&f.noReload, "no-reload", false, "do not signal dockerd after changing registry-mirrors")
	}
}

// resolve merges the flags over the defaults for the chosen installation type.
func (f *dockerFlags) resolve(ctx context.Context) (dockerPaths, error) {
	adapter, err := autodetect.Resolve(ctx, autodetect.Options{
		Rootless: f.rootless, Store: f.store,
		Overrides: dockerintegration.Paths{CertsDir: f.certsDir, DaemonJSON: f.daemonJSON, StateDir: f.stateDir, DockerSocket: f.dockerSock, PidFile: f.pidFile},
	})
	if err != nil {
		return dockerPaths{}, err
	}
	f.adapter = adapter
	return adapter.Paths(), nil
}

// cmdDockerd dispatches plan, apply and revert.
func cmdDockerd(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: want one of plan, apply, revert", errUsage)
	}
	action, rest := args[0], args[1:]
	fs := newFlagSet("dockerd "+action, stderr)
	var flags dockerFlags
	switch action {
	case "plan", "apply", "revert":
		flags.register(fs, action != "plan")
	default:
		return fmt.Errorf("%w: unknown dockerd action %q", errUsage, action)
	}
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	paths, err := flags.resolve(ctx)
	if err != nil {
		return err
	}
	if err := flags.adapter.Validate(ctx); err != nil {
		return err
	}
	if flags.restart && flags.noReload {
		return fmt.Errorf("--restart and --no-reload cannot be combined")
	}
	applier := daemonconf.Applier{
		StatePath: filepath.Join(paths.StateDir, "dockerd-state.json"),
		BackupDir: filepath.Join(paths.StateDir, "backups"),
	}

	switch action {
	case "plan":
		plan, err := buildPlan(ctx, &flags, paths)
		if err != nil {
			return err
		}
		printPlan(stdout, plan)
		printWarnings(stderr, plan)
		return nil

	case "apply":
		plan, err := buildPlan(ctx, &flags, paths)
		if err != nil {
			return err
		}
		printWarnings(stderr, plan)
		if len(plan.Files) == 0 {
			printPlan(stdout, plan)
			return nil
		}
		st, err := applier.Apply(plan, flags.force || flags.adapter.MergesConfig())
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "applied %d file(s) using %s integration\n", len(st.Files), plan.Store)
		for _, note := range plan.Notes {
			fmt.Fprintln(stdout, "note:", note)
		}
		return activateIntegration(ctx, stdout, plan.NeedsReload, &flags)

	default: // revert
		st, err := applier.Revert()
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "reverted %d file(s)\n", len(st.Files))
		return activateIntegration(ctx, stdout, st.NeedsReload, &flags)
	}
}

// buildPlan reads the proxy configuration and renders the files for the image store.
// It only reads; nothing is written here.
func buildPlan(ctx context.Context, flags *dockerFlags, paths dockerPaths) (daemonconf.Plan, error) {
	cfgPath, err := resolveConfigPath(flags.configPath)
	if err != nil {
		return daemonconf.Plan{}, err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return daemonconf.Plan{}, err
	}
	return flags.adapter.Render(ctx, targetFor(cfg), flags.store)
}

func activateIntegration(ctx context.Context, out io.Writer, needed bool, flags *dockerFlags) error {
	if !needed {
		return nil
	}
	if flags.noReload || (flags.adapter.RequiresRestart() && !flags.restart) {
		// The adapter's plan explains how to activate changes. Do not infer that
		// all targets can be signalled like a Linux daemon.
		fmt.Fprintln(out, "configuration saved; activation is pending (see plan notes)")
		return nil
	}
	if err := flags.adapter.Activate(ctx, flags.restart); err != nil {
		return fmt.Errorf("configuration saved but activation failed: %w", err)
	}
	fmt.Fprintln(out, "Docker configuration activated")
	return nil
}

// targetFor converts the proxy configuration into what the renderers need.
func targetFor(cfg config.Config) daemonconf.Target {
	t := daemonconf.Target{ProxyURL: cfg.ProxyURL()}
	for _, alias := range slices.Sorted(maps.Keys(cfg.Registries)) {
		t.Routes = append(t.Routes, daemonconf.Route{Alias: alias, Origin: cfg.Registries[alias].Origin})
	}
	return t
}

func printPlan(w io.Writer, plan daemonconf.Plan) {
	fmt.Fprintf(w, "configuration strategy: %s\n", plan.Store)
	for _, f := range plan.Files {
		fmt.Fprintf(w, "\n--- %s\n%s", f.Path, f.Content)
	}
	for _, note := range plan.Notes {
		fmt.Fprintln(w, "\nnote:", note)
	}
}

func printWarnings(w io.Writer, plan daemonconf.Plan) {
	for _, warning := range plan.Warnings {
		fmt.Fprintln(w, "warning:", warning)
	}
}
