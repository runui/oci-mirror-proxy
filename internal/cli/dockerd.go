package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/admin"
	"github.com/runui/oci-mirror-proxy/internal/daemonconf"
	"github.com/runui/oci-mirror-proxy/internal/dockerintegration"
	"github.com/runui/oci-mirror-proxy/internal/dockerintegration/autodetect"
	linuxintegration "github.com/runui/oci-mirror-proxy/internal/dockerintegration/linux"
	"github.com/runui/oci-mirror-proxy/internal/userdir"
)

// dockerPaths are the locations that the dockerd integration reads and writes.
type dockerPaths = dockerintegration.Paths

// defaultDockerPaths preserves the Linux path contract for explicit offline plans.
func defaultDockerPaths(rootless bool) (dockerPaths, error) {
	return linuxintegration.Defaults(rootless)
}

// dockerFlags are the flags shared by every dockerd and doctor subcommand.
type dockerFlags struct {
	restart     bool
	adapter     dockerintegration.Adapter
	configPath  string
	adminSocket string
	target      *daemonconf.Target
	store       string
	rootless    bool
	certsDir    string
	daemonJSON  string
	stateDir    string
	dockerSock  string
	pidFile     string
	force       bool
	noReload    bool
	serverInfo  string
	ifManaged   bool
	remove      bool
	verify      bool
}

func (f *dockerFlags) register(fs *flag.FlagSet, withApplyFlags bool) {
	fs.StringVar(&f.configPath, "config", "", "deprecated compatibility flag; ignored")
	fs.StringVar(&f.adminSocket, "admin-socket", "", "explicit admin endpoint")
	fs.StringVar(&f.serverInfo, "server-info", "", "read server info --json output from FILE instead of private service configuration")
	fs.StringVar(&f.store, "store", "auto", "image store: auto, containerd or legacy")
	fs.BoolVar(&f.rootless, "rootless", false, "target a rootless dockerd (per-user paths)")
	fs.StringVar(&f.certsDir, "certs-dir", "", "override the containerd certs directory")
	fs.StringVar(&f.daemonJSON, "daemon-json", "", "override the daemon.json path")
	fs.StringVar(&f.dockerSock, "docker-socket", "", "override the dockerd socket path")
	fs.StringVar(&f.pidFile, "pid-file", "", "override the dockerd pid file path")
	if withApplyFlags {
		fs.BoolVar(&f.restart, "restart", false, "allow restarting Docker to activate settings (interrupts containers)")
		fs.StringVar(&f.stateDir, "state-dir", "", "deprecated compatibility flag; ignored")
		fs.BoolVar(&f.force, "force", false, "compatibility flag; existing configuration is always merged safely")
		fs.BoolVar(&f.noReload, "no-reload", false, "do not signal dockerd after changing registry-mirrors")
		fs.BoolVar(&f.ifManaged, "if-managed", false, "compatibility flag; actual configuration is always inspected")
		fs.BoolVar(&f.verify, "verify", false, "verify proxy, files and loaded mirror settings after applying")
	}
}

// resolve merges the flags over the defaults for the chosen installation type.
func (f *dockerFlags) resolve(ctx context.Context) (dockerPaths, error) {
	adapter, err := autodetect.Resolve(ctx, autodetect.Options{
		Rootless: f.rootless, Store: f.store,
		Overrides: dockerintegration.Paths{CertsDir: f.certsDir, DaemonJSON: f.daemonJSON, DockerSocket: f.dockerSock, PidFile: f.pidFile},
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
	flags.remove = action == "revert"

	switch action {
	case "plan":
		plan, err := buildPlan(ctx, &flags, paths)
		if err != nil {
			return err
		}
		printPlan(stdout, plan)
		printWarnings(stderr, plan)
		return nil

	case "apply", "revert":
		dir, err := userdir.Dir()
		if err != nil {
			return err
		}
		applier := daemonconf.Applier{BackupDir: filepath.Join(dir, "backups")}
		plan, err := buildPlan(ctx, &flags, paths)
		if err != nil {
			return err
		}
		printWarnings(stderr, plan)
		result, err := applier.Apply(plan)
		for _, backup := range result.Backups {
			fmt.Fprintln(stdout, "Backup:", backup)
		}
		if err != nil {
			for _, f := range result.Files {
				fmt.Fprintln(stderr, "configuration already changed:", f.Path)
			}
			return err
		}
		if len(result.Files) == 0 {
			fmt.Fprintln(stdout, "Configuration files already match; no files changed.")
			for _, note := range plan.Notes {
				fmt.Fprintln(stdout, "note:", note)
			}
		} else {
			fmt.Fprintf(stdout, "%s: changed %d file(s) using %s integration\n", action, len(result.Files), plan.Store)
			for _, note := range plan.Notes {
				fmt.Fprintln(stdout, "note:", note)
			}
		}
		// Matching files do not prove the Engine loaded them. In particular,
		// apply followed by apply --restart must still activate Desktop settings.
		if err := activateIntegration(ctx, stdout, result.NeedsReload, &flags); err != nil {
			return err
		}
		if flags.verify {
			return verifyIntegration(ctx, stdout, plan, &flags)
		}
		return nil
	}
	return nil
}

// buildPlan reads the proxy configuration and renders the files for the image store.
// It only reads; nothing is written here.
func buildPlan(ctx context.Context, flags *dockerFlags, paths dockerPaths) (daemonconf.Plan, error) {
	target, err := flags.serverTarget()
	if err != nil {
		return daemonconf.Plan{}, err
	}
	target.Remove = flags.remove
	if err := validateDockerTarget(target); err != nil {
		return daemonconf.Plan{}, err
	}
	return flags.adapter.Render(ctx, target, flags.store)
}

func validateDockerTarget(target daemonconf.Target) error {
	validURL := func(raw string) bool {
		u, err := url.Parse(raw)
		return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
	}
	if !validURL(target.ProxyURL) {
		return fmt.Errorf("invalid integration proxy URL")
	}
	for _, route := range target.Routes {
		// Renderer aliases become filesystem components. Exported data is not a
		// privilege boundary, but it must never escape the selected certs directory.
		if route.Alias == "" || route.Alias == "." || route.Alias == ".." || strings.ContainsAny(route.Alias, "/\\:\x00\r\n") || !validURL(route.Origin) {
			return fmt.Errorf("invalid integration route %q", route.Alias)
		}
	}
	return nil
}

// serverTarget pins one validated server snapshot for planning and verification.
// Server data supplies renderer inputs, never local paths or execution policy.
func (f *dockerFlags) serverTarget() (daemonconf.Target, error) {
	if f.target != nil {
		return *f.target, nil
	}
	var target daemonconf.Target
	var err error
	if f.serverInfo != "" {
		if f.adminSocket != "" {
			return target, fmt.Errorf("--server-info and --admin-socket cannot be combined")
		}
		target, err = readServerTarget(f)
	} else {
		var client *admin.Client
		client, err = adminClient(f.adminSocket, "")
		if err == nil {
			var info admin.ServerInfo
			info, err = client.Info()
			if err == nil {
				if info.SchemaVersion != 1 {
					return target, fmt.Errorf("unsupported server information schema %d", info.SchemaVersion)
				}
				target.ProxyURL = info.Config.ProxyURL
				for _, route := range info.Config.Registries {
					target.Routes = append(target.Routes, daemonconf.Route{Alias: route.Alias, Origin: route.Origin})
				}
			}
		}
	}
	if err != nil {
		return target, err
	}
	if err := validateDockerTarget(target); err != nil {
		return target, err
	}
	f.target = &target
	return target, nil
}

// Only renderer inputs cross the privilege boundary. JSON never selects Docker
// sockets, output paths or permissions: those still come from local detection.
func readServerTarget(flags *dockerFlags) (daemonconf.Target, error) {
	f, err := os.Open(flags.serverInfo)
	if err != nil {
		return daemonconf.Target{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return daemonconf.Target{}, err
	}
	if len(data) > 1<<20 {
		return daemonconf.Target{}, fmt.Errorf("server information exceeds 1 MiB")
	}
	var info admin.Info
	// Windows PowerShell 5.1 writes a BOM with Set-Content -Encoding UTF8.
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if err := json.Unmarshal(data, &info); err != nil {
		return daemonconf.Target{}, fmt.Errorf("parse server information: %w", err)
	}
	if info.SchemaVersion != 1 {
		return daemonconf.Target{}, fmt.Errorf("unsupported server information schema %d", info.SchemaVersion)
	}
	target := daemonconf.Target{ProxyURL: info.Config.ProxyURL}
	for _, reg := range info.Config.Registries {
		target.Routes = append(target.Routes, daemonconf.Route{Alias: reg.Alias, Origin: reg.Origin})
	}
	if len(target.Routes) == 0 {
		return target, fmt.Errorf("server information has no registry routes")
	}
	return target, validateDockerTarget(target)
}

func verifyIntegration(ctx context.Context, out io.Writer, plan daemonconf.Plan, flags *dockerFlags) error {
	// A successful SIGHUP only confirms delivery. Wait for /info to report the
	// loaded mirrors; Desktop may legitimately remain pending without a restart.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	target, err := flags.serverTarget()
	if err != nil {
		return err
	}
	targetURL := target.ProxyURL
	for {
		info, infoErr := daemonconf.FetchInfo(ctx, flags.adapter.Paths().DockerSocket)
		checks := daemonconf.Diagnose(ctx, daemonconf.DiagnoseInput{ProxyURL: targetURL, Info: info, InfoErr: infoErr, Expected: plan.Files, MirrorSettings: plan.Store == daemonconf.StoreDesktop, Remove: flags.remove})
		var failures []error
		pending := false
		for _, check := range checks {
			if !check.OK {
				if check.Name == "dockerd registry-mirrors" && flags.adapter.RequiresRestart() && !flags.restart {
					pending = true
				} else {
					failures = append(failures, fmt.Errorf("%s: %s", check.Name, check.Detail))
				}
			}
		}
		if len(failures) == 0 {
			if pending {
				fmt.Fprintln(out, "Docker integration: pending Docker Desktop restart. Docker was not restarted.")
			} else if plan.Store == daemonconf.StoreContainerd {
				fmt.Fprintln(out, "Docker integration: files verified and proxy reachable (image pull not tested)")
			} else {
				fmt.Fprintln(out, "Docker integration: active")
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("configuration saved but verification failed: %w", errors.Join(failures...))
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func activateIntegration(ctx context.Context, out io.Writer, needed bool, flags *dockerFlags) error {
	if !needed && !flags.adapter.RequiresRestart() {
		return nil
	}
	if flags.noReload || (flags.adapter.RequiresRestart() && !flags.restart) {
		// The adapter's plan explains how to activate changes. Do not infer that
		// all targets can be signalled like a Linux daemon.
		fmt.Fprintln(out, "Docker was not restarted or reloaded; loaded settings have not been verified (see plan notes)")
		return nil
	}
	if err := flags.adapter.Activate(ctx, flags.restart); err != nil {
		return fmt.Errorf("Docker configuration activation failed (configuration files were preserved): %w", err)
	}
	fmt.Fprintln(out, "Docker configuration activated")
	return nil
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
