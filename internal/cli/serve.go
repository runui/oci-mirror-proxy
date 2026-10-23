package cli

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/runui/oci-mirror-proxy/internal/app"
)

// cmdServe runs the proxy in the foreground until interrupted.
// SIGHUP re-reads the configuration file; SIGINT or SIGTERM stops the proxy.
func cmdServe(ctx context.Context, args []string, stderr io.Writer) error {
	fs := newFlagSet("serve", stderr)
	configPath := fs.String("config", "", "configuration file (default: user or system configuration)")
	adminSocket := fs.String("admin-socket", "", "admin socket path (default: configuration or per-user location)")
	logLevel := fs.String("log-level", "", "debug, info, warn or error (overrides configuration; default info)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	path, err := resolveConfigPath(*configPath)
	if err != nil {
		return err
	}
	socket, err := resolveAdminSocket(*adminSocket, path)
	if err != nil {
		return err
	}
	var level slog.LevelVar
	var configLevel *slog.LevelVar = &level
	if *logLevel != "" {
		var parsed slog.Level
		if err := parsed.UnmarshalText([]byte(*logLevel)); err != nil {
			return err
		}
		level.Set(parsed)
		configLevel = nil
	}
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: &level}))

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	svc, err := app.New(ctx, app.Options{ConfigPath: path, Logger: logger, LogLevel: configLevel, Version: version})
	if err != nil {
		return err
	}

	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-hup:
				if err := svc.Reload(); err != nil {
					logger.Error("configuration reload failed; keeping the running configuration", "error", err)
					continue
				}
				logger.Info("configuration reloaded")
			}
		}
	}()

	return svc.Serve(ctx, socket)
}
