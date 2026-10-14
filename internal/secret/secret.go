// Package secret resolves secret references such as "env:NAME" or "file:/path"
// into values, and keeps those values out of logs and error messages.
package secret

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

const redacted = "[REDACTED]"

// Secret wraps a sensitive value. Its formatting methods never reveal the value;
// only Reveal returns it, so every disclosure is explicit at the call site.
type Secret struct{ value string }

// New wraps value as a Secret.
func New(value string) Secret { return Secret{value: value} }

// Reveal returns the raw value. Call it only where the value is actually used.
func (s Secret) Reveal() string { return s.value }

// String implements fmt.Stringer and always returns a redacted placeholder.
func (s Secret) String() string { return redacted }

// GoString implements fmt.GoStringer so %#v does not leak the value either.
func (s Secret) GoString() string { return redacted }

var (
	// ErrInvalidRef means a reference is not in "scheme:key" form.
	ErrInvalidRef = errors.New("invalid secret reference")
	// ErrUnknownScheme means no provider is registered for the reference scheme.
	ErrUnknownScheme = errors.New("unknown secret scheme")
)

// ValidateRef checks the syntax of a reference without resolving it.
// Configuration validation uses it, so adding a provider never requires
// changing the config package.
func ValidateRef(ref string) error {
	_, _, err := splitRef(ref)
	return err
}

func splitRef(ref string) (string, string, error) {
	scheme, key, ok := strings.Cut(ref, ":")
	if !ok || scheme == "" || key == "" {
		return "", "", fmt.Errorf("%w: %q must look like scheme:key", ErrInvalidRef, ref)
	}
	return scheme, key, nil
}

// Provider resolves references of one scheme.
type Provider interface {
	Scheme() string
	// Get returns the secret identified by key, the part after "scheme:".
	Get(ctx context.Context, key string) (Secret, error)
}

// Resolver dispatches references to the provider registered for their scheme.
type Resolver struct {
	providers map[string]Provider
}

// NewResolver builds a Resolver. Two providers may not share a scheme.
func NewResolver(providers ...Provider) (*Resolver, error) {
	r := &Resolver{providers: make(map[string]Provider, len(providers))}
	for _, p := range providers {
		if _, dup := r.providers[p.Scheme()]; dup {
			return nil, fmt.Errorf("duplicate secret provider for scheme %q", p.Scheme())
		}
		r.providers[p.Scheme()] = p
	}
	return r, nil
}

// Resolve looks up a reference such as "env:MIRROR_PASSWORD".
func (r *Resolver) Resolve(ctx context.Context, ref string) (Secret, error) {
	if r == nil {
		return Secret{}, fmt.Errorf("%w: no resolver configured", ErrUnknownScheme)
	}
	scheme, key, err := splitRef(ref)
	if err != nil {
		return Secret{}, err
	}
	p, ok := r.providers[scheme]
	if !ok {
		return Secret{}, fmt.Errorf("%w: %q", ErrUnknownScheme, scheme)
	}
	return p.Get(ctx, key)
}

// EnvProvider reads secrets from environment variables ("env:NAME").
type EnvProvider struct {
	// Lookup defaults to os.LookupEnv. Tests inject their own.
	Lookup func(name string) (string, bool)
}

// Scheme returns "env".
func (EnvProvider) Scheme() string { return "env" }

// Get returns the value of the named environment variable.
func (p EnvProvider) Get(_ context.Context, name string) (Secret, error) {
	lookup := p.Lookup
	if lookup == nil {
		lookup = os.LookupEnv
	}
	v, ok := lookup(name)
	if !ok || v == "" {
		return Secret{}, fmt.Errorf("environment variable %s is not set or empty", name)
	}
	return New(v), nil
}

// FileProvider reads secrets from files ("file:/path/to/secret").
// Trailing line breaks are stripped, so `echo secret > file` works.
type FileProvider struct{}

// Scheme returns "file".
func (FileProvider) Scheme() string { return "file" }

// Get returns the contents of the named file.
func (FileProvider) Get(_ context.Context, path string) (Secret, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Secret{}, fmt.Errorf("read secret file: %w", err)
	}
	v := strings.TrimRight(string(data), "\r\n")
	if v == "" {
		return Secret{}, fmt.Errorf("secret file %s is empty", path)
	}
	return New(v), nil
}
