// Package cache defines the extension point for a local blob cache. The
// interface exists so that routing can be wired to a cache later without
// reshaping the data path. No implementation ships in this release.
package cache

import (
	"context"
	"io"
)

// Store caches blobs addressed by digest.
type Store interface {
	// Get returns the cached blob. ok is false when the blob is not present.
	Get(ctx context.Context, digest string) (body io.ReadCloser, size int64, ok bool, err error)
	// Put stores a blob. Implementations must verify that its content matches digest.
	Put(ctx context.Context, digest string, size int64, r io.Reader) error
}
