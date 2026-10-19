package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/config"
	"github.com/runui/oci-mirror-proxy/internal/daemonconf"
)

// dockerPaths are the locations that the dockerd integration reads and writes.
type dockerPaths struct {
	CertsDir     string // containerd: one hosts.toml per registry below this directory
	DaemonJSON   string // legacy: registry-mirrors lives here
	StateDir     string // where apply records its changes and keeps backups
	DockerSocket string // used to detect the image store and verify the setup
	PidFile      string // used to send SIGHUP to dockerd
}

// defaultDockerPaths returns the standard locations for a system-wide or a
// rootless installation. The rootless pid file location is an assumption; the
// --pid-file flag overrides it when the installation differs.
func defaultDockerPaths(rootless bool) (dockerPaths, error) {
	if !rootless {
		return dockerPaths{
			CertsDir:     "/etc/docker/certs.d",
			DaemonJSON:   "/etc/docker/daemon.json",
			StateDir:     "/var/lib/oci-mirror-proxy",
			DockerSocket: "/var/run/docker.sock",
			PidFile:      "/var/run/docker.pid",
		}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return dockerPaths{}, fmt.Errorf("locate home directory: %w", err)
	}
	configHome := envOr("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	stateHome := envOr("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		return dockerPaths{}, errors.New("rootless setup needs XDG_RUNTIME_DIR to locate the dockerd socket")
	}
	return dockerPaths{
		CertsDir:     filepath.Join(configHome, "docker", "certs.d"),
		DaemonJSON:   filepath.Join(configHome, "docker", "daemon.json"),
		StateDir:     filepath.Join(stateHome, "oci-mirror-proxy"),
		DockerSocket: filepath.Join(runtimeDir, "docker.sock"),
		PidFile:      filepath.Join(runtimeDir, "docker.pid"),
	}, nil
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// dockerFlags are the flags shared by every dockerd and doctor subcommand.
type dockerFlags struct {
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
		fs.StringVar(&f.stateDir, "state-dir", "", "override where apply keeps its state and backups")
		fs.BoolVar(&f.force, "force", false, "take over files that were not created by this tool (they are backed up first)")
		fs.BoolVar(&f.noReload, "no-reload", false, "do not signal dockerd after changing registry-mirrors")
	}
}

// resolve merges the flags over the defaults for the chosen installation type.
func (f *dockerFlags) resolve() (dockerPaths, error) {
	p, err := defaultDockerPaths(f.rootless)
	if err != nil {
		return dockerPaths{}, err
	}
	override := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	override(&p.CertsDir, f.certsDir)
	override(&p.DaemonJSON, f.daemonJSON)
	override(&p.StateDir, f.stateDir)
	override(&p.DockerSocket, f.dockerSock)
	override(&p.PidFile, f.pidFile)
	return p, nil
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
	paths, err := flags.resolve()
	if err != nil {
		return err
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
		st, err := applier.Apply(plan, flags.force)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "applied %d file(s) for the %s image store\n", len(st.Files), plan.Store)
		for _, note := range plan.Notes {
			fmt.Fprintln(stdout, "note:", note)
		}
		return reloadIfNeeded(stdout, plan.NeedsReload, &flags, paths)

	default: // revert
		st, err := applier.Revert()
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "reverted %d file(s)\n", len(st.Files))
		return reloadIfNeeded(stdout, st.NeedsReload, &flags, paths)
	}
}

func reloadIfNeeded(stdout io.Writer, needed bool, flags *dockerFlags, paths dockerPaths) error {
	if !needed {
		return nil
	}
	if flags.noReload {
		fmt.Fprintln(stdout, "dockerd was not signalled; reload it before the change takes effect")
		return nil
	}
	if err := daemonconf.Reload(paths.PidFile); err != nil {
		return fmt.Errorf("configuration written, but dockerd could not be reloaded (%w); run: systemctl reload docker", err)
	}
	fmt.Fprintln(stdout, "signalled dockerd to reload its configuration")
	return nil
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
	store, err := resolveStore(ctx, flags.store, paths.DockerSocket)
	if err != nil {
		return daemonconf.Plan{}, err
	}
	return renderer(store, paths).Render(targetFor(cfg), os.ReadFile)
}

func renderer(store daemonconf.StoreKind, paths dockerPaths) daemonconf.Renderer {
	if store == daemonconf.StoreLegacy {
		return daemonconf.LegacyRenderer{DaemonJSON: paths.DaemonJSON}
	}
	return daemonconf.ContainerdRenderer{CertsDir: paths.CertsDir}
}

// targetFor converts the proxy configuration into what the renderers need.
func targetFor(cfg config.Config) daemonconf.Target {
	t := daemonconf.Target{ProxyURL: cfg.ProxyURL()}
	for _, alias := range slices.Sorted(maps.Keys(cfg.Registries)) {
		t.Routes = append(t.Routes, daemonconf.Route{Alias: alias, Origin: cfg.Registries[alias].Origin})
	}
	return t
}

// resolveStore honours an explicit --store, and otherwise asks dockerd which store it uses.
func resolveStore(ctx context.Context, flagValue, socket string) (daemonconf.StoreKind, error) {
	switch flagValue {
	case "containerd":
		return daemonconf.StoreContainerd, nil
	case "legacy":
		return daemonconf.StoreLegacy, nil
	case "", "auto":
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		info, err := daemonconf.FetchInfo(ctx, socket)
		if err != nil {
			return "", fmt.Errorf("cannot detect the image store (is dockerd running?): %w; pass --store explicitly", err)
		}
		return daemonconf.DetectStore(info), nil
	default:
		return "", fmt.Errorf("%w: --store must be auto, containerd or legacy", errUsage)
	}
}

func printPlan(w io.Writer, plan daemonconf.Plan) {
	fmt.Fprintf(w, "image store: %s\n", plan.Store)
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
