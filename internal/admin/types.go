// Package admin exposes runtime management over a private local IPC endpoint. Access
// control uses socket permissions or named-pipe ACLs, so no tokens are involved.
// Transport owns request bounds and protocol mapping; controllers own storage
// and runtime changes. Fragment operations expose only config.d raw documents,
// never arbitrary paths or subscription semantics. Reads remain privileged
// because configuration may contain credentials. Windows uses named-pipe ACLs.
package admin

import (
	"errors"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/config"
	"github.com/runui/oci-mirror-proxy/internal/upstream"
)

// Sentinel errors that the controller returns and the server maps to HTTP status codes.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("already exists")
	ErrInvalid  = errors.New("invalid configuration")
)

// Status is the response of GET /v1/status.
type Status struct {
	Listen     string           `json:"listen"`
	Registries []RegistryStatus `json:"registries"`
}

// RegistryStatus describes one registry alias and its upstreams.
type RegistryStatus struct {
	Alias     string            `json:"alias"`
	Upstreams []upstream.Status `json:"upstreams"`
}

// UpstreamSpec is the JSON form of config.Upstream. Durations are strings such
// as "5s", matching the configuration file, and omitted durations take the defaults.
type UpstreamSpec struct {
	ID        string   `json:"id"`
	URL       string   `json:"url"`
	Disabled  bool     `json:"disabled,omitzero"`
	Connect   string   `json:"connect,omitempty"`
	FirstByte string   `json:"first_byte,omitempty"`
	Overall   string   `json:"overall,omitempty"`
	Auth      AuthSpec `json:"auth,omitzero"`
	TLS       TLSSpec  `json:"tls,omitzero"`
}

// AuthSpec is the JSON form of config.Auth.
type AuthSpec struct {
	Type     string `json:"type,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// TLSSpec is the JSON form of config.TLS.
type TLSSpec struct {
	CAFile             string `json:"ca_file,omitempty"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitzero"`
}

// ToConfig converts the wire form to the configuration model, parsing durations.
func (s UpstreamSpec) ToConfig() (config.Upstream, error) {
	u := config.Upstream{
		ID:       s.ID,
		URL:      s.URL,
		Disabled: s.Disabled,
		Auth: config.Auth{
			Type:     s.Auth.Type,
			Username: s.Auth.Username,
			Password: s.Auth.Password,
		},
		TLS: config.TLS{
			CAFile:             s.TLS.CAFile,
			InsecureSkipVerify: s.TLS.InsecureSkipVerify,
		},
	}
	var err error
	if u.Timeouts.Connect, err = parseDuration("connect", s.Connect); err != nil {
		return config.Upstream{}, err
	}
	if u.Timeouts.FirstByte, err = parseDuration("first_byte", s.FirstByte); err != nil {
		return config.Upstream{}, err
	}
	if u.Timeouts.Overall, err = parseDuration("overall", s.Overall); err != nil {
		return config.Upstream{}, err
	}
	return u, nil
}

func parseDuration(field, value string) (time.Duration, error) {
	if value == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, &invalidFieldError{field: field, err: err}
	}
	return d, nil
}

type invalidFieldError struct {
	field string
	err   error
}

func (e *invalidFieldError) Error() string { return e.field + ": " + e.err.Error() }
func (e *invalidFieldError) Unwrap() error { return ErrInvalid }

// SpecFromConfig converts a configuration upstream to its wire form.
func SpecFromConfig(u config.Upstream) UpstreamSpec {
	return UpstreamSpec{
		ID:        u.ID,
		URL:       u.URL,
		Disabled:  u.Disabled,
		Connect:   u.Timeouts.Connect.String(),
		FirstByte: u.Timeouts.FirstByte.String(),
		Overall:   u.Timeouts.Overall.String(),
		Auth: AuthSpec{
			Type:     u.Auth.Type,
			Username: u.Auth.Username,
			Password: u.Auth.Password,
		},
		TLS: TLSSpec{
			CAFile:             u.TLS.CAFile,
			InsecureSkipVerify: u.TLS.InsecureSkipVerify,
		},
	}
}

// Controller is the set of operations the admin API exposes. The application
// service implements it.
type Controller interface {
	Status() Status
	ListUpstreams(alias string) ([]config.Upstream, error)
	AddUpstream(alias string, u config.Upstream) error
	SetUpstreamDisabled(alias, id string, disabled bool) error
	RemoveUpstream(alias, id string) error
	Reload() error
}
