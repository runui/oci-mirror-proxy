// Package userdir resolves auxiliary storage for the effective CLI user. It
// neither selects service configuration nor switches identity under sudo.
// Docker target paths belong to platform adapters, not this package.
package userdir

import (
	"fmt"
	"os"
	"path/filepath"
)

// Dir resolves without creating anything; readers must remain side-effect free.
func Dir() (string, error) {
	if path := os.Getenv("OCI_MIRROR_PROXY_CONFIG_HOME"); path != "" {
		if !filepath.IsAbs(path) {
			return "", fmt.Errorf("OCI_MIRROR_PROXY_CONFIG_HOME must be absolute")
		}
		return path, nil
	}
	path, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user application directory: %w", err)
	}
	return filepath.Join(path, "oci-mirror-proxy"), nil
}
