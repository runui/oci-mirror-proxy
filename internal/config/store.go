package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/runui/oci-mirror-proxy/internal/fsutil"
)

// AppName is used for per-user file locations.
const AppName = "oci-mirror-proxy"

const fileHeader = "# oci-mirror-proxy configuration.\n# The CLI preserves source files; local overrides are written to the sibling .d directory.\n"

// Load reads the main file and its drop-ins, merges them, applies defaults, and
// validates the effective configuration under a shared collection lock.
func Load(path string) (Config, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	unlock, err := ReadLock(ctx, path)
	if err != nil {
		return Config{}, err
	}
	defer unlock()
	b, err := LoadBundle(path)
	if err != nil {
		return Config{}, err
	}
	// ReadLock does not create a sidecar on a read-only mount. If the first
	// writer created one while we were reading, wait for it and verify again
	// under that lock; two optimistic reads alone could see a paused writer's
	// intermediate multi-file state. A missing sidecar here means no writer
	// began before the snapshot completed.
	verifyUnlock, err := ReadLock(ctx, path)
	if err != nil {
		return Config{}, err
	}
	defer verifyUnlock()
	if err := b.CheckUnchanged(); err != nil {
		return Config{}, err
	}
	return b.Effective, nil
}

// Decode parses configuration bytes. Unknown keys are rejected so typos fail loudly.
func Decode(data []byte) (Config, error) {
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		if errors.Is(err, io.EOF) {
			return Config{}, errors.New("config file is empty")
		}
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	c.ApplyDefaults()
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Encode renders the configuration as YAML with a header comment.
func Encode(c Config) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(fileHeader)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}
	return buf.Bytes(), nil
}

// Save applies defaults, validates c, and atomically writes it to path.
// Callers pass a Clone, because defaults are applied in place.
func Save(path string, c Config) error {
	c.ApplyDefaults()
	if err := c.Validate(); err != nil {
		return err
	}
	data, err := Encode(c)
	if err != nil {
		return err
	}
	// The file may name secret sources, so keep it private to the owner.
	return fsutil.WriteFileAtomic(path, data, 0o600)
}

// DefaultPath returns the per-user configuration file location.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config directory: %w", err)
	}
	return filepath.Join(dir, AppName, "config.yaml"), nil
}

// DefaultAdminSocket returns the per-user admin socket location. It prefers
// XDG_RUNTIME_DIR, which is per session and cleared on logout.
func DefaultAdminSocket() (string, error) {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, AppName, "admin.sock"), nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate user cache directory: %w", err)
	}
	return filepath.Join(dir, AppName, "admin.sock"), nil
}
