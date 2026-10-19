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
// requests never take that lock: they read the lock-free route table.
type Service struct {
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
	if err := s.apply(ctx, cfg, false); err != nil {
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

// apply makes next the running configuration. If any step fails, the previous
// configuration is restored, so the service never runs a half-applied change.
func (s *Service) apply(ctx context.Context, next config.Config, persist bool) error {
	prev := s.applied
	if err := s.reconcile(ctx, next); err != nil {
		s.restore(ctx, prev)
		return err
	}
	if persist {
		if err := config.Save(s.path, next); err != nil {
			s.restore(ctx, prev)
			return fmt.Errorf("save configuration: %w", err)
		}
	}
	s.applied = next
	if s.logLevel != nil {
		level, _ := next.LoggingLevel() // Configuration has already been validated.
		s.logLevel.Set(level)
	}
	return nil
}

// reconcile brings the pools and the route table in line with cfg.
func (s *Service) reconcile(ctx context.Context, cfg config.Config) error {
	pools := make(map[string]*upstream.Pool, len(cfg.Registries))
	for alias, reg := range cfg.Registries {
		pool, ok := s.pools[alias]
		if !ok {
			pool = upstream.NewPool(s.env)
		}
		if err := pool.Reconcile(ctx, reg); err != nil {
			return fmt.Errorf("registry %s: %w", alias, err)
		}
		pools[alias] = pool
	}

	routes := make(map[string]route.Dispatcher, len(pools))
	for alias, reg := range cfg.Registries {
		routes[alias] = s.dispatcher(reg, pools[alias])
	}

	// Pools for registries that are gone are emptied, which releases their connections.
	for alias, old := range s.pools {
		if _, kept := pools[alias]; !kept {
			if err := old.Reconcile(ctx, config.Registry{}); err != nil {
				s.logger.Warn("could not release pool", "registry", alias, "error", err)
			}
		}
	}
	s.pools = pools
	s.table.Swap(routes)
	return nil
}

// dispatcher assembles the request path for one registry: the strategy chosen by
// its race setting, wrapped in the shared middleware.
func (s *Service) dispatcher(reg config.Registry, pool *upstream.Pool) route.Dispatcher {
	var strategy scheduler.Strategy = scheduler.Sequential{}
	if reg.Race.Enabled {
		strategy = scheduler.Hedged{Delay: reg.Race.Delay, MaxParallel: reg.Race.MaxParallel}
	}
	return route.Chain(route.Route{Source: pool, Strategy: strategy}, s.middle...)
}

func (s *Service) restore(ctx context.Context, prev config.Config) {
	if err := s.reconcile(ctx, prev); err != nil {
		s.logger.Error("could not restore the previous configuration", "error", err)
	}
}

// mutate applies a change to the running configuration and persists it.
// It edits a copy, so a failed change leaves no trace.
func (s *Service) mutate(fn func(*config.Config) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.applied.Clone()
	if err := fn(&next); err != nil {
		return err
	}
	next.ApplyDefaults()
	if err := next.Validate(); err != nil {
		return fmt.Errorf("%w: %w", admin.ErrInvalid, err)
	}
	return s.apply(context.Background(), next, true)
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
	return s.mutate(func(c *config.Config) error {
		reg, ok := c.Registries[alias]
		if !ok {
			return fmt.Errorf("%w: registry %q", admin.ErrNotFound, alias)
		}
		if _, found := findUpstream(reg, u.ID); found {
			return fmt.Errorf("%w: upstream %q already exists", admin.ErrConflict, u.ID)
		}
		reg.Upstreams = append(reg.Upstreams, u)
		c.Registries[alias] = reg
		return nil
	})
}

// SetUpstreamDisabled implements admin.Controller.
func (s *Service) SetUpstreamDisabled(alias, id string, disabled bool) error {
	return s.mutate(func(c *config.Config) error {
		reg, ok := c.Registries[alias]
		if !ok {
			return fmt.Errorf("%w: registry %q", admin.ErrNotFound, alias)
		}
		i, found := findUpstream(reg, id)
		if !found {
			return fmt.Errorf("%w: upstream %q", admin.ErrNotFound, id)
		}
		reg.Upstreams[i].Disabled = disabled
		c.Registries[alias] = reg
		return nil
	})
}

// RemoveUpstream implements admin.Controller.
func (s *Service) RemoveUpstream(alias, id string) error {
	return s.mutate(func(c *config.Config) error {
		reg, ok := c.Registries[alias]
		if !ok {
			return fmt.Errorf("%w: registry %q", admin.ErrNotFound, alias)
		}
		i, found := findUpstream(reg, id)
		if !found {
			return fmt.Errorf("%w: upstream %q", admin.ErrNotFound, id)
		}
		reg.Upstreams = slices.Delete(reg.Upstreams, i, i+1)
		c.Registries[alias] = reg
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
	return s.apply(context.Background(), next, false)
}

// Close releases every upstream connection. Requests already in flight finish.
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for alias, pool := range s.pools {
		if err := pool.Reconcile(context.Background(), config.Registry{}); err != nil {
			s.logger.Warn("could not close pool", "registry", alias, "error", err)
		}
	}
}

func findUpstream(reg config.Registry, id string) (int, bool) {
	for i, u := range reg.Upstreams {
		if u.ID == id {
			return i, true
		}
	}
	return 0, false
}
