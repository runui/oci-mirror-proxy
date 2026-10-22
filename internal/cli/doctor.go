package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/runui/oci-mirror-proxy/internal/config"
	"github.com/runui/oci-mirror-proxy/internal/daemonconf"
)

// cmdDoctor runs read-only checks and fails when any of them does.
func cmdDoctor(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("doctor", stderr)
	var flags dockerFlags
	flags.register(fs, false)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	paths, err := flags.resolve(ctx)
	if err != nil {
		return err
	}
	if err := flags.adapter.Validate(ctx); err != nil {
		return err
	}
	cfgPath, err := resolveConfigPath(flags.configPath)
	if err != nil {
		return err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	info, infoErr := daemonconf.FetchInfo(ctx, paths.DockerSocket)
	var expected []daemonconf.File
	if _, err := storeForDoctor(flags.store, info, infoErr); err == nil {
		if plan, err := flags.adapter.Render(ctx, targetFor(cfg), flags.store); err == nil {
			expected = plan.Files
			printWarnings(stderr, plan)
		}
	}

	checks := daemonconf.Diagnose(ctx, daemonconf.DiagnoseInput{
		ProxyURL:       cfg.ProxyURL(),
		Info:           info,
		InfoErr:        infoErr,
		Expected:       expected,
		MirrorSettings: flags.adapter.MergesConfig(),
	})
	failed := 0
	for _, c := range checks {
		mark := "ok  "
		if !c.OK {
			mark = "FAIL"
			failed++
		}
		fmt.Fprintf(stdout, "[%s] %s: %s\n", mark, c.Name, c.Detail)
	}
	if failed > 0 {
		return fmt.Errorf("%d check(s) failed", failed)
	}
	fmt.Fprintln(stdout, "all checks passed")
	return nil
}

// storeForDoctor picks the store to verify against: the detected one when dockerd
// answered, otherwise an explicit --store value.
func storeForDoctor(flagValue string, info daemonconf.Info, infoErr error) (daemonconf.StoreKind, error) {
	if infoErr == nil {
		return daemonconf.DetectStore(info), nil
	}
	switch flagValue {
	case "containerd":
		return daemonconf.StoreContainerd, nil
	case "legacy":
		return daemonconf.StoreLegacy, nil
	default:
		return "", errors.New("image store unknown because dockerd did not answer; pass --store")
	}
}
