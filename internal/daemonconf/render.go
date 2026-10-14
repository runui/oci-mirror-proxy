// Package daemonconf configures dockerd to use the proxy. Rendering is pure and
// produces a Plan; Applier is the only part that writes files, and it records
// enough state to revert exactly what it changed.
package daemonconf

import (
	"errors"
	"io/fs"

	"github.com/runui/oci-mirror-proxy/internal/ociapi"
)

// StoreKind identifies the image store dockerd uses.
type StoreKind string

const (
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
}

// Route maps one registry as dockerd names it to its official origin.
type Route struct {
	Alias  string // e.g. docker.io or ghcr.io
	Origin string // e.g. https://registry-1.docker.io
}

// File is one file dockerd must see. Mode is applied when the file is written.
type File struct {
	Path    string
	Content []byte
	Mode    fs.FileMode
}

// Plan is the complete set of changes a renderer wants to make.
type Plan struct {
	Store       StoreKind
	Files       []File
	NeedsReload bool     // dockerd must re-read its configuration
	Notes       []string // remarks shown to the operator
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
