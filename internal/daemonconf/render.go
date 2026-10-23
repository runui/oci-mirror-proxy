// Package daemonconf configures dockerd to use the proxy. Rendering is pure and
// produces a Plan; Applier backs up and atomically publishes actual changes.
// Callers supply target paths and backup storage; this package does not discover
// services, parse subscription catalogs or persist integration ownership state.
// All changed existing files must be backed up before publication. Atomicity is
// per file, so failures report already-visible writes rather than imply rollback.
package daemonconf

import (
	"errors"
	"io/fs"

	"github.com/runui/oci-mirror-proxy/internal/ociapi"
)

// StoreKind identifies the image store dockerd uses.
type StoreKind string

const (
	// StoreDesktop uses Desktop's Engine settings regardless of its image store.
	StoreDesktop StoreKind = "desktop"
	// StoreContainerd is the containerd image store. Mirrors are configured per
	// registry with hosts.toml files.
	StoreContainerd StoreKind = "containerd"
	// StoreLegacy is the classic graphdriver store. Only docker.io mirrors are
	// supported, through daemon.json.
	StoreLegacy StoreKind = "legacy"
)

// Target describes what dockerd must be told. It deliberately knows nothing
// about the proxy's own configuration model.
type Target struct {
	// ProxyURL is the address dockerd connects to, e.g. http://127.0.0.1:24817.
	ProxyURL string
	Routes   []Route
	// Remove removes this URL from current configuration, never restores history.
	Remove bool
}

// Route maps one registry as dockerd names it to its official origin.
type Route struct {
	Alias  string // e.g. docker.io or ghcr.io
	Origin string // e.g. https://registry-1.docker.io
}

// File is one file dockerd must see. Mode is used when creating a new file;
// replacement preserves the existing file's permissions and owner.
type File struct {
	Path    string
	Content []byte
	Mode    fs.FileMode
	// CheckBefore binds a merge to the bytes it read. Every writable plan must
	// supply a snapshot; an unchecked replacement is never safe for shared files.
	CheckBefore *string
	// BeforeExists distinguishes an absent file from an existing empty file.
	BeforeExists bool
}

// Plan is the complete set of changes a renderer wants to make.
type Plan struct {
	Store       StoreKind
	Files       []File
	NeedsReload bool     // dockerd must re-read its configuration
	Notes       []string // remarks shown to the operator
	Warnings    []string // unsupported configuration skipped by the renderer
}

// ReadFunc reads the current content of a file. A missing file must return an
// error for which errors.Is(err, fs.ErrNotExist) holds.
type ReadFunc func(path string) ([]byte, error)

// Renderer computes the files for one image store.
type Renderer interface {
	Store() StoreKind
	Render(t Target, read ReadFunc) (Plan, error)
}

// proxyPathPrefix is the path under which the proxy serves an alias. The default
// alias is served at the root, so dockerd's /v2 requests reach it unchanged.
func proxyPathPrefix(alias string) string {
	if alias == ociapi.DefaultAlias {
		return ""
	}
	return "/" + alias
}

// ErrNoRoutes means there is nothing to configure.
var ErrNoRoutes = errors.New("no registries are configured")
