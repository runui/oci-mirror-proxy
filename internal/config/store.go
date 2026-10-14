package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/runui/oci-mirror-proxy/internal/fsutil"
)

// AppName is used for per-user file locations.
const AppName = "oci-mirror-proxy"

const fileHeader = "# oci-mirror-proxy configuration.\n# Changes made through the CLI are written back to this file.\n"

// Load reads, decodes, defaults and validates the configuration at path.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	return Decode(data)
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
