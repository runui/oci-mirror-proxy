// Package config defines the configuration model, its defaults and its
// validation. It depends only on other leaf packages.
package config

import (
	"errors"
	"fmt"
	"maps"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/ociapi"
	"github.com/runui/oci-mirror-proxy/internal/secret"
)

// SchemaVersion is the only configuration version this build understands.
const SchemaVersion = 1

// DefaultListen is the address the proxy binds to unless configured otherwise.
const DefaultListen = "127.0.0.1:24817"

const (
	dockerHubOrigin = "https://registry-1.docker.io"

	AuthNone  = "none"
	AuthBasic = "basic"
	AuthToken = "token"
)

const (
	defaultRaceDelay        = 500 * time.Millisecond
	defaultRaceParallel     = 2
	defaultBreakerThreshold = 3
	defaultBreakerCooldown  = 30 * time.Second
	defaultFirstByte        = 5 * time.Second
	defaultOverall          = 60 * time.Second
)

var upstreamIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,62}$`)

// Config is the complete proxy configuration as stored in the config file.
type Config struct {
	Version    int                 `yaml:"version"`
	Listen     string              `yaml:"listen"`
	Advertise  string              `yaml:"advertise,omitempty"`
	Admin      Admin               `yaml:"admin,omitempty"`
	Registries map[string]Registry `yaml:"registries"`
}

// Admin holds settings for the local management socket.
type Admin struct {
	Socket string `yaml:"socket,omitempty"`
}

// Registry configures one registry alias: its upstreams and how requests reach them.
type Registry struct {
	// Origin is the official endpoint. dockerd falls back to it when the proxy fails.
	Origin    string     `yaml:"origin,omitempty"`
	Race      Race       `yaml:"race"`
	Breaker   Breaker    `yaml:"breaker"`
	Upstreams []Upstream `yaml:"upstreams"`
}

// Race controls hedged requests for a registry.
type Race struct {
	Enabled     bool          `yaml:"enabled"`
	Delay       time.Duration `yaml:"delay"`
	MaxParallel int           `yaml:"max_parallel"`
}

// Breaker controls when an upstream is taken out of rotation.
type Breaker struct {
	Threshold int           `yaml:"threshold"`
	Cooldown  time.Duration `yaml:"cooldown"`
}

// Upstream is one user-configured Registry v2 endpoint.
// It is comparable, so the runtime can detect unchanged definitions with ==.
type Upstream struct {
	ID       string   `yaml:"id"`
	URL      string   `yaml:"url"`
	Disabled bool     `yaml:"disabled,omitempty"`
	Timeouts Timeouts `yaml:"timeouts"`
	Auth     Auth     `yaml:"auth"`
	TLS      TLS      `yaml:"tls,omitempty"`
}

// Timeouts bounds one upstream attempt.
type Timeouts struct {
	// FirstByte limits the wait for response headers.
	FirstByte time.Duration `yaml:"first_byte"`
	// Overall limits a whole metadata request. Blob transfers are not bounded by it.
	Overall time.Duration `yaml:"overall"`
}

// Auth selects how requests to an upstream are authenticated.
type Auth struct {
	Type     string `yaml:"type"`
	Username string `yaml:"username,omitempty"`
	// Password is a secret reference such as env:NAME or file:/path, never a literal.
	Password string `yaml:"password,omitempty"`
}

// TLS holds per-upstream transport security options.
type TLS struct {
	CAFile             string `yaml:"ca_file,omitempty"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify,omitempty"`
}

// Default returns a fresh configuration with the built-in defaults applied.
func Default() Config {
	c := Config{Registries: map[string]Registry{ociapi.DefaultAlias: {}}}
	c.ApplyDefaults()
	return c
}

// ApplyDefaults fills every unset field with its default value.
func (c *Config) ApplyDefaults() {
	if c.Version == 0 {
		c.Version = SchemaVersion
	}
	if c.Listen == "" {
		c.Listen = DefaultListen
	}
	if c.Registries == nil {
		c.Registries = map[string]Registry{}
	}
	for alias, reg := range c.Registries {
		reg.applyDefaults(alias)
		c.Registries[alias] = reg
	}
}

func (r *Registry) applyDefaults(alias string) {
	if r.Origin == "" {
		r.Origin = defaultOrigin(alias)
	}
	if r.Race.Delay == 0 {
		r.Race.Delay = defaultRaceDelay
	}
	if r.Race.MaxParallel == 0 {
		r.Race.MaxParallel = defaultRaceParallel
	}
	if r.Breaker.Threshold == 0 {
		r.Breaker.Threshold = defaultBreakerThreshold
	}
	if r.Breaker.Cooldown == 0 {
		r.Breaker.Cooldown = defaultBreakerCooldown
	}
	for i := range r.Upstreams {
		u := &r.Upstreams[i]
		if u.Timeouts.FirstByte == 0 {
			u.Timeouts.FirstByte = defaultFirstByte
		}
		if u.Timeouts.Overall == 0 {
			u.Timeouts.Overall = defaultOverall
		}
		if u.Auth.Type == "" {
			u.Auth.Type = AuthNone
		}
	}
}

func defaultOrigin(alias string) string {
	if alias == ociapi.DefaultAlias {
		return dockerHubOrigin
	}
	return "https://" + alias
}

// Validate reports every problem it finds, not just the first one.
func (c Config) Validate() error {
	var errs []error
	if c.Version != SchemaVersion {
		errs = append(errs, fmt.Errorf("unsupported config version %d (want %d)", c.Version, SchemaVersion))
	}
	errs = append(errs, validateListen("listen", c.Listen, false))
	if c.Advertise != "" {
		errs = append(errs, validateListen("advertise", c.Advertise, true))
	}
	for _, alias := range slices.Sorted(maps.Keys(c.Registries)) {
		errs = append(errs, validateRegistry(alias, c.Registries[alias]))
	}
	return errors.Join(errs...)
}

// ProxyURL returns the address dockerd should use to reach the proxy.
// Advertise wins. Otherwise the listen address is used, with wildcard hosts
// mapped to loopback, because a wildcard is not a connectable address.
func (c Config) ProxyURL() string {
	if c.Advertise != "" {
		return "http://" + c.Advertise
	}
	host, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return "http://" + c.Listen
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// Clone returns a deep copy, so callers can edit it without touching shared state.
func (c Config) Clone() Config {
	out := c
	out.Registries = make(map[string]Registry, len(c.Registries))
	for alias, reg := range c.Registries {
		reg.Upstreams = slices.Clone(reg.Upstreams)
		out.Registries[alias] = reg
	}
	return out
}

func validateRegistry(alias string, r Registry) error {
	var errs []error
	wrap := func(err error) error { return fmt.Errorf("registry %s: %w", alias, err) }

	if !ociapi.ValidAlias(alias) {
		errs = append(errs, wrap(fmt.Errorf("alias must be a host-like name without a port, and not %q", "v2")))
	}
	if err := validateBaseURL(r.Origin); err != nil {
		errs = append(errs, wrap(fmt.Errorf("origin: %w", err)))
	}
	if r.Race.Enabled {
		if r.Race.Delay <= 0 {
			errs = append(errs, wrap(errors.New("race.delay must be positive")))
		}
		if r.Race.MaxParallel < 2 {
			errs = append(errs, wrap(errors.New("race.max_parallel must be at least 2 when racing is enabled")))
		}
	}
	if r.Breaker.Threshold < 1 {
		errs = append(errs, wrap(errors.New("breaker.threshold must be at least 1")))
	}
	if r.Breaker.Cooldown <= 0 {
		errs = append(errs, wrap(errors.New("breaker.cooldown must be positive")))
	}

	seen := make(map[string]bool, len(r.Upstreams))
	for i, u := range r.Upstreams {
		if seen[u.ID] {
			errs = append(errs, wrap(fmt.Errorf("duplicate upstream id %q", u.ID)))
		}
		seen[u.ID] = true
		if err := validateUpstream(u); err != nil {
			errs = append(errs, wrap(fmt.Errorf("upstream #%d (%s): %w", i+1, u.ID, err)))
		}
	}
	return errors.Join(errs...)
}

func validateUpstream(u Upstream) error {
	var errs []error
	if !upstreamIDPattern.MatchString(u.ID) {
		errs = append(errs, fmt.Errorf("id %q must be 1-63 letters, digits, '-' or '_'", u.ID))
	}
	if err := validateBaseURL(u.URL); err != nil {
		errs = append(errs, fmt.Errorf("url: %w", err))
	}
	if u.Timeouts.FirstByte <= 0 {
		errs = append(errs, errors.New("timeouts.first_byte must be positive"))
	}
	if u.Timeouts.Overall <= 0 {
		errs = append(errs, errors.New("timeouts.overall must be positive"))
	}
	if err := validateAuth(u.Auth); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func validateAuth(a Auth) error {
	switch a.Type {
	case AuthNone:
		if a.Username != "" || a.Password != "" {
			return errors.New("auth type none does not take credentials")
		}
	case AuthBasic:
		if a.Username == "" || a.Password == "" {
			return errors.New("auth type basic requires username and password")
		}
	case AuthToken:
		if a.Username == "" && a.Password != "" {
			return errors.New("auth type token needs a username when a password is set")
		}
		if a.Username != "" && a.Password == "" {
			return errors.New("auth type token needs a password when a username is set")
		}
	default:
		return fmt.Errorf("unknown auth type %q (want none, basic or token)", a.Type)
	}
	if a.Password != "" {
		if err := secret.ValidateRef(a.Password); err != nil {
			return fmt.Errorf("auth password: %w", err)
		}
	}
	return nil
}

func validateBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return fmt.Errorf("URL %q must use http or https", raw)
	case u.Host == "":
		return fmt.Errorf("URL %q has no host", raw)
	case u.User != nil:
		return fmt.Errorf("URL %q must not contain credentials; use auth settings", raw)
	case u.RawQuery != "" || u.Fragment != "":
		return fmt.Errorf("URL %q must not contain a query or fragment", raw)
	}
	return nil
}

// validateListen checks a host:port value. Advertise addresses must name a
// concrete host, because dockerd has to connect to them.
func validateListen(field, addr string, requireHost bool) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%s %q: %w", field, addr, err)
	}
	if requireHost && host == "" {
		return fmt.Errorf("%s %q must include a concrete host", field, addr)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("%s %q: port must be between 1 and 65535", field, addr)
	}
	return nil
}
