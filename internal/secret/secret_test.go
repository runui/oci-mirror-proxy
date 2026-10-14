package secret

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSecretNeverPrintsValue(t *testing.T) {
	s := New("hunter2")
	for _, got := range []string{
		s.String(),
		fmt.Sprint(s),
		fmt.Sprintf("%v", s),
		fmt.Sprintf("%+v", s),
		fmt.Sprintf("%#v", s),
	} {
		if got != "[REDACTED]" {
			t.Fatalf("formatted secret = %q, want redacted placeholder", got)
		}
	}
	if s.Reveal() != "hunter2" {
		t.Fatalf("Reveal() = %q, want the raw value", s.Reveal())
	}
}

func TestValidateRef(t *testing.T) {
	for _, ref := range []string{"env:TOKEN", "file:/run/secret"} {
		if err := ValidateRef(ref); err != nil {
			t.Errorf("ValidateRef(%q) = %v, want nil", ref, err)
		}
	}
	for _, ref := range []string{"", "plain-password", "env:", ":NAME"} {
		if err := ValidateRef(ref); !errors.Is(err, ErrInvalidRef) {
			t.Errorf("ValidateRef(%q) = %v, want ErrInvalidRef", ref, err)
		}
	}
}

func TestResolverEnv(t *testing.T) {
	env := map[string]string{"SET": "value", "EMPTY": ""}
	r, err := NewResolver(EnvProvider{Lookup: func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Resolve(context.Background(), "env:SET")
	if err != nil || got.Reveal() != "value" {
		t.Fatalf("Resolve(env:SET) = %v, %v", got, err)
	}
	if _, err := r.Resolve(context.Background(), "env:EMPTY"); err == nil {
		t.Error("empty environment variable must be an error")
	}
	if _, err := r.Resolve(context.Background(), "env:MISSING"); err == nil {
		t.Error("missing environment variable must be an error")
	}
}

func TestResolverFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pw")
	if err := os.WriteFile(path, []byte("from-file\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := NewResolver(FileProvider{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Resolve(t.Context(), "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Reveal() != "from-file" {
		t.Fatalf("file secret = %q, want trailing newline stripped", got.Reveal())
	}
}

func TestResolverUnknownSchemeAndDuplicates(t *testing.T) {
	r, err := NewResolver(EnvProvider{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(t.Context(), "keychain:item"); !errors.Is(err, ErrUnknownScheme) {
		t.Fatalf("unknown scheme error = %v, want ErrUnknownScheme", err)
	}
	if _, err := NewResolver(EnvProvider{}, EnvProvider{}); err == nil {
		t.Fatal("duplicate schemes must be rejected")
	}
	var nilResolver *Resolver
	if _, err := nilResolver.Resolve(t.Context(), "env:X"); !errors.Is(err, ErrUnknownScheme) {
		t.Fatalf("nil resolver error = %v, want ErrUnknownScheme", err)
	}
}
