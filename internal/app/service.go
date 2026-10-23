// Package app wires the components together. It owns the configuration state,
// the upstream pools and the route table, and it implements the admin controller.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/admin"
	"github.com/runui/oci-mirror-proxy/internal/config"
	"github.com/runui/oci-mirror-proxy/internal/frontend"
	"github.com/runui/oci-mirror-proxy/internal/middleware"
	"github.com/runui/oci-mirror-proxy/internal/route"
	"github.com/runui/oci-mirror-proxy/internal/scheduler"
	"github.com/runui/oci-mirror-proxy/internal/secret"
	"github.com/runui/oci-mirror-proxy/internal/upstream"
)

// resumeAttempts bounds how often one blob download may be continued.
const resumeAttempts = 3

// Options configures a Service.
type Options struct {
	Version    string
	ConfigPath string
	Logger     *slog.Logger
	// LogLevel follows configuration on reload, unless the CLI overrides it.
	LogLevel *slog.LevelVar
	// Secrets resolves secret references. It defaults to env: and file: providers.
	Secrets *secret.Resolver
	// Now is the clock used for breaker timing and token expiry.
	Now func() time.Time
}

// Service is a running proxy. Configuration changes are serialized by mu, and
// requests never take that lock: they briefly acquire a runtime generation,
// then serve the entire response without holding configuration locks.
type Service struct {
	build    admin.BuildInfo
	path     string
	logger   *slog.Logger
	logLevel *slog.LevelVar
	env      upstream.Env
	table    *route.Table
	middle   []route.Middleware

	mu      sync.Mutex
	applied config.Config
	pools   map[string]*upstream.Pool
}

// New loads the configuration file and builds routes from it.
func New(ctx context.Context, opts Options) (*Service, error) {
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return nil, err
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	secrets := opts.Secrets
	if secrets == nil {
		secrets, err = secret.NewResolver(secret.EnvProvider{}, secret.FileProvider{})
		if err != nil {
			return nil, err
		}
	}
	s := &Service{
		build:    admin.CurrentBuild(opts.Version),
		path:     opts.ConfigPath,
		logger:   logger,
		logLevel: opts.LogLevel,
		env:      upstream.Env{Secrets: secrets, Now: opts.Now},
		table:    route.NewTable(),
		middle: []route.Middleware{
			middleware.Logging(logger),
			middleware.Resume(resumeAttempts),
		},
		pools: make(map[string]*upstream.Pool),
	}
	if err := s.apply(ctx, cfg, nil); err != nil {
		return nil, err
	}
	return s, nil
}

// Handler returns the registry API handler.
func (s *Service) Handler() http.Handler {
	return frontend.New(s.table)
}

// ListenAddr returns the address the data plane listens on.
func (s *Service) ListenAddr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applied.Listen
}

// apply prepares every registry without mutating the published generation.
// Publication is infallible and follows persistence; neither a build error nor
// a write failure can expose a half-applied runtime configuration.
func (s *Service) apply(ctx context.Context, next config.Config, persist func() error) error {
	pools := make(map[string]*upstream.Pool, len(next.Registries))
	cleanup := func() {
		for _, p := range pools {
			p.Release()
		}
	}
	for alias, reg := range next.Registries {
		old := s.pools[alias]
		if old == nil {
			old = upstream.NewPool(s.env)
		}
		p, err := old.Fork(ctx, reg)
		if err != nil {
			cleanup()
			return fmt.Errorf("registry %s: %w", alias, err)
		}
		pools[alias] = p
	}
	if persist != nil {
		if err := persist(); err != nil {
			cleanup()
			return err
		}
	}
	routes := make(map[string]route.Dispatcher, len(pools))
	for alias, reg := range next.Registries {
		routes[alias] = s.dispatcher(reg, pools[alias])
	}
	s.pools = pools
	s.table.SwapWithCleanup(routes, cleanup)
	s.applied = next
	if s.logLevel != nil {
		level, _ := next.LoggingLevel() // Configuration has already been validated.
		s.logLevel.Set(level)
	}
	return nil
}

// dispatcher assembles the request path for one registry: the strategy chosen by
// its race setting, wrapped in the shared middleware.
func (s *Service) dispatcher(reg config.Registry, pool *upstream.Pool) route.Dispatcher {
	var strategy scheduler.Strategy = scheduler.Sequential{}
	if reg.Race.Enabled {
		strategy = scheduler.Hedged{Delay: reg.Race.Delay, MaxParallel: reg.Race.MaxParallel}
	}
	if reg.Adaptive.Enabled {
		strategy = scheduler.Adaptive{Base: strategy, Window: reg.Adaptive.Window, MinBytesPerSecond: reg.Adaptive.MinBytesPerSecond, ProbeTimeout: reg.Adaptive.ProbeTimeout, MaxSwitches: reg.Adaptive.MaxSwitches, Logger: s.logger}
	}
	return route.Chain(route.Route{Source: pool, Strategy: strategy}, s.middle...)
}

// mutate edits fresh document trees, prepares a complete generation, persists
// the targeted edits, then publishes. Persistence attempts conditional rollback
// on error; any rollback failures are reported without altering the runtime.
func (s *Service) mutate(fn func(*config.Bundle) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	unlock, err := config.Lock(ctx, s.path)
	if err != nil {
		return err
	}
	defer unlock()
	// Disk may contain a CLI update not yet reloaded. Never write a clone of
	// s.applied back over newer files; mutate the latest document set instead.
	b, err := config.LoadBundle(s.path)
	if err != nil {
		return fmt.Errorf("%w: %w", admin.ErrInvalid, err)
	}
	if err := fn(b); err != nil {
		return err
	}
	if err := b.Merge(); err != nil {
		return fmt.Errorf("%w: %w", admin.ErrInvalid, err)
	}
	next := b.Effective
	next.Listen = s.applied.Listen
	return s.apply(ctx, next, b.Persist)
}

// Info describes the applied generation under the reload lock. Reading disk
// would report edits that the running service has not accepted yet.
func (s *Service) Info() admin.ServerInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := admin.ConfigInfo{Listen: s.applied.Listen, ProxyURL: s.applied.ProxyURL(), Registries: []admin.RegistryInfo{}}
	for _, alias := range slices.Sorted(maps.Keys(s.applied.Registries)) {
		cfg.Registries = append(cfg.Registries, admin.RegistryInfo{Alias: alias, Origin: s.applied.Registries[alias].Origin})
	}
	return admin.ServerInfo{SchemaVersion: 1, Server: s.build, APIVersion: "v1", Config: cfg}
}

// Status implements admin.Controller.
func (s *Service) Status() admin.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := admin.Status{Listen: s.applied.Listen}
	for _, alias := range slices.Sorted(maps.Keys(s.pools)) {
		out.Registries = append(out.Registries, admin.RegistryStatus{
			Alias:     alias,
			Upstreams: s.pools[alias].Status(),
		})
	}
	return out
}

// ListUpstreams implements admin.Controller.
func (s *Service) ListUpstreams(alias string) ([]config.Upstream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reg, ok := s.applied.Registries[alias]
	if !ok {
		return nil, fmt.Errorf("%w: registry %q", admin.ErrNotFound, alias)
	}
	return slices.Clone(reg.Upstreams), nil
}

// AddUpstream implements admin.Controller.
func (s *Service) AddUpstream(alias string, u config.Upstream) error {
	return s.mutate(func(b *config.Bundle) error {
		reg, ok := b.Effective.Registries[alias]
		if !ok {
			return fmt.Errorf("%w: registry %q", admin.ErrNotFound, alias)
		}
		_, found := findUpstream(reg, u.ID)
		canonical, _ := config.NormalizeURL(u.URL)
		for _, existing := range reg.Upstreams {
			if existing.URL == canonical {
				found = true
			}
		}
		if found {
			return fmt.Errorf("%w: upstream %q already exists", admin.ErrConflict, u.ID)
		}
		if err := b.EditUpstream("add", alias, "", u, false); err != nil {
			return fmt.Errorf("%w: %w", admin.ErrInvalid, err)
		}
		return nil
	})
}

// SetUpstreamDisabled implements admin.Controller.
func (s *Service) SetUpstreamDisabled(alias, id string, disabled bool) error {
	return s.mutate(func(b *config.Bundle) error {
		reg, ok := b.Effective.Registries[alias]
		if !ok {
			return fmt.Errorf("%w: registry %q", admin.ErrNotFound, alias)
		}
		_, found := findUpstream(reg, id)
		if !found {
			return fmt.Errorf("%w: upstream %q", admin.ErrNotFound, id)
		}
		if err := b.EditUpstream("disable", alias, id, config.Upstream{}, disabled); err != nil {
			return fmt.Errorf("%w: %w", admin.ErrInvalid, err)
		}
		return nil
	})
}

// RemoveUpstream implements admin.Controller.
func (s *Service) RemoveUpstream(alias, id string) error {
	return s.mutate(func(b *config.Bundle) error {
		reg, ok := b.Effective.Registries[alias]
		if !ok {
			return fmt.Errorf("%w: registry %q", admin.ErrNotFound, alias)
		}
		_, found := findUpstream(reg, id)
		if !found {
			return fmt.Errorf("%w: upstream %q", admin.ErrNotFound, id)
		}
		if err := b.EditUpstream("remove", alias, id, config.Upstream{}, false); err != nil {
			return fmt.Errorf("%w: %w", admin.ErrInvalid, err)
		}
		return nil
	})
}

// Reload implements admin.Controller. It re-reads the configuration file, which
// is the source of truth for changes made by hand. A file that fails validation
// leaves the running configuration untouched.
func (s *Service) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	next, err := config.Load(s.path)
	if err != nil {
		return fmt.Errorf("%w: %w", admin.ErrInvalid, err)
	}
	if next.Listen != s.applied.Listen {
		s.logger.Warn("listen address changed; it takes effect after a restart", "listen", next.Listen)
		next.Listen = s.applied.Listen
	}
	return s.apply(context.Background(), next, nil)
}

// Close releases every upstream connection. Requests already in flight finish.
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.table.SwapWithCleanup(nil, nil)
	s.pools = map[string]*upstream.Pool{}
}

func findUpstream(reg config.Registry, id string) (int, bool) {
	canonical, _ := config.NormalizeURL(id)
	for i, u := range reg.Upstreams {
		if (id != "" && u.ID == id) || u.URL == canonical {
			return i, true
		}
	}
	return 0, false
}
