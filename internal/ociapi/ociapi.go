// Package ociapi parses OCI Distribution (Registry v2) request paths.
// It is a leaf package with no dependencies beyond the standard library.
package ociapi

import (
	"errors"
	"regexp"
	"strings"
)

// DefaultAlias is the registry served from the root of the proxy (/v2/...).
const DefaultAlias = "docker.io"

// maxRepoLength is the limit from the OCI distribution name grammar.
const maxRepoLength = 255

// Kind classifies a registry API request.
type Kind int

const (
	KindPing Kind = iota
	KindManifest
	KindBlob
	KindTags
	KindReferrers
)

// String returns a short lowercase name for logs.
func (k Kind) String() string {
	switch k {
	case KindPing:
		return "ping"
	case KindManifest:
		return "manifest"
	case KindBlob:
		return "blob"
	case KindTags:
		return "tags"
	case KindReferrers:
		return "referrers"
	default:
		return "unknown"
	}
}

// Parsed is a request path split into its routing parts.
type Parsed struct {
	Alias string // registry alias, e.g. "docker.io" or "ghcr.io"
	Kind  Kind
	Repo  string // repository name; empty for ping
	Ref   string // tag or digest; set for manifest, blob and referrers
	Path  string // canonical upstream-relative API path, e.g. /v2/library/nginx/manifests/latest
}

// ErrNotAPIPath is returned for any path that is not a valid registry API path.
var ErrNotAPIPath = errors.New("not a registry API path")

var (
	aliasPattern  = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)
	repoPattern   = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*$`)
	tagPattern    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)
	digestPattern = regexp.MustCompile(`^[a-z0-9]+(?:[.+_-][a-z0-9]+)*:[a-zA-Z0-9=_-]{32,}$`)
)

// operation pairs a URL path marker with the request kind it introduces.
type operation struct {
	marker string
	kind   Kind
}

var operations = []operation{
	{"/manifests/", KindManifest},
	{"/blobs/", KindBlob},
	{"/referrers/", KindReferrers},
}

// ValidAlias reports whether s can be used as a registry alias in a URL path.
// Aliases are host-like names without ports; "v2" is reserved for the API root.
func ValidAlias(s string) bool {
	return s != "v2" && aliasPattern.MatchString(s)
}

// Parse splits a request path into alias, kind, repository and reference.
//
//	/v2/library/nginx/manifests/latest      -> docker.io, manifest
//	/ghcr.io/v2/owner/repo/blobs/sha256:... -> ghcr.io,   blob
func Parse(p string) (Parsed, error) {
	alias := DefaultAlias
	apiPath := p
	if !isAPIPath(p) {
		// Prefixed form: the first segment selects the registry.
		seg, tail, found := strings.Cut(strings.TrimPrefix(p, "/"), "/")
		if !found || !ValidAlias(seg) {
			return Parsed{}, ErrNotAPIPath
		}
		alias, apiPath = seg, "/"+tail
	}
	if apiPath == "/v2" || apiPath == "/v2/" {
		return Parsed{Alias: alias, Kind: KindPing, Path: "/v2/"}, nil
	}
	name, ok := strings.CutPrefix(apiPath, "/v2/")
	if !ok {
		return Parsed{}, ErrNotAPIPath
	}
	return parseOperation(alias, name)
}

// isAPIPath reports whether p is rooted at the API root itself.
func isAPIPath(p string) bool {
	return p == "/v2" || strings.HasPrefix(p, "/v2/")
}

func parseOperation(alias, name string) (Parsed, error) {
	if repo, ok := strings.CutSuffix(name, "/tags/list"); ok {
		if !validRepo(repo) {
			return Parsed{}, ErrNotAPIPath
		}
		return Parsed{Alias: alias, Kind: KindTags, Repo: repo, Path: "/v2/" + repo + "/tags/list"}, nil
	}

	// References never contain "/", so the operation marker that occurs last
	// in the name is always the real one, even when the repository itself
	// contains a marker-like segment.
	var found operation
	best := -1
	for _, op := range operations {
		if i := strings.LastIndex(name, op.marker); i > best {
			best, found = i, op
		}
	}
	if best < 0 {
		return Parsed{}, ErrNotAPIPath
	}
	repo, ref := name[:best], name[best+len(found.marker):]
	if !validRepo(repo) || !validRef(found.kind, ref) {
		return Parsed{}, ErrNotAPIPath
	}
	return Parsed{
		Alias: alias,
		Kind:  found.kind,
		Repo:  repo,
		Ref:   ref,
		Path:  "/v2/" + repo + found.marker + ref,
	}, nil
}

func validRepo(repo string) bool {
	return repo != "" && len(repo) <= maxRepoLength && repoPattern.MatchString(repo)
}

func validRef(kind Kind, ref string) bool {
	if kind == KindManifest {
		return tagPattern.MatchString(ref) || digestPattern.MatchString(ref)
	}
	return digestPattern.MatchString(ref)
}
