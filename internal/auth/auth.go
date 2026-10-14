// Package auth attaches credentials to upstream requests. Every upstream owns its
// own Authorizer, so credentials are never shared between upstreams.
package auth

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/config"
	"github.com/runui/oci-mirror-proxy/internal/secret"
)

// Authorizer prepares requests for one upstream.
type Authorizer interface {
	// Authorize attaches credentials for repo before the request is sent.
	Authorize(ctx context.Context, req *http.Request, repo string) error
	// Challenge inspects a 401 response. It returns true when the request should
	// be retried once, because fresh credentials are now available.
	Challenge(ctx context.Context, resp *http.Response, repo string) (retry bool, err error)
}

// Options carries the dependencies an Authorizer may need.
type Options struct {
	Secrets *secret.Resolver
	// HTTPClient performs token exchanges. It should be the upstream's own client,
	// so that the upstream's TLS settings apply to the token realm as well.
	HTTPClient *http.Client
	// Now is the clock used for token expiry. It defaults to time.Now.
	Now func() time.Time
}

// New builds the Authorizer described by cfg. Secret references are resolved
// here, so a missing environment variable fails configuration, not requests.
func New(ctx context.Context, cfg config.Auth, opts Options) (Authorizer, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	switch cfg.Type {
	case "", config.AuthNone:
		return noneAuth{}, nil

	case config.AuthBasic:
		pw, err := opts.Secrets.Resolve(ctx, cfg.Password)
		if err != nil {
			return nil, fmt.Errorf("basic auth password: %w", err)
		}
		return basicAuth{username: cfg.Username, password: pw}, nil

	case config.AuthToken:
		if opts.HTTPClient == nil {
			return nil, fmt.Errorf("token auth requires an HTTP client")
		}
		var pw secret.Secret
		if cfg.Password != "" {
			var err error
			if pw, err = opts.Secrets.Resolve(ctx, cfg.Password); err != nil {
				return nil, fmt.Errorf("token auth password: %w", err)
			}
		}
		return newTokenAuth(opts.HTTPClient, cfg.Username, pw, opts.Now), nil

	default:
		return nil, fmt.Errorf("unknown auth type %q", cfg.Type)
	}
}

// noneAuth sends requests unchanged.
type noneAuth struct{}

func (noneAuth) Authorize(context.Context, *http.Request, string) error { return nil }

func (noneAuth) Challenge(context.Context, *http.Response, string) (bool, error) {
	return false, nil
}

// basicAuth sends static HTTP Basic credentials with every request.
type basicAuth struct {
	username string
	password secret.Secret
}

func (b basicAuth) Authorize(_ context.Context, req *http.Request, _ string) error {
	req.SetBasicAuth(b.username, b.password.Reveal())
	return nil
}

// Challenge returns false: static credentials cannot be renewed, so a 401 is final.
func (basicAuth) Challenge(context.Context, *http.Response, string) (bool, error) {
	return false, nil
}
