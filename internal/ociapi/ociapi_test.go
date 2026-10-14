package ociapi

import (
	"errors"
	"strings"
	"testing"
)

const digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		want    Parsed
		wantErr bool
	}{
		{
			name: "ping root",
			path: "/v2/",
			want: Parsed{Alias: DefaultAlias, Kind: KindPing, Path: "/v2/"},
		},
		{
			name: "ping without slash",
			path: "/v2",
			want: Parsed{Alias: DefaultAlias, Kind: KindPing, Path: "/v2/"},
		},
		{
			name: "manifest by tag on default alias",
			path: "/v2/library/nginx/manifests/latest",
			want: Parsed{Alias: DefaultAlias, Kind: KindManifest, Repo: "library/nginx", Ref: "latest", Path: "/v2/library/nginx/manifests/latest"},
		},
		{
			name: "manifest by digest",
			path: "/v2/library/nginx/manifests/" + digest,
			want: Parsed{Alias: DefaultAlias, Kind: KindManifest, Repo: "library/nginx", Ref: digest, Path: "/v2/library/nginx/manifests/" + digest},
		},
		{
			name: "blob on prefixed alias",
			path: "/ghcr.io/v2/owner/repo/blobs/" + digest,
			want: Parsed{Alias: "ghcr.io", Kind: KindBlob, Repo: "owner/repo", Ref: digest, Path: "/v2/owner/repo/blobs/" + digest},
		},
		{
			name: "tags list",
			path: "/v2/library/nginx/tags/list",
			want: Parsed{Alias: DefaultAlias, Kind: KindTags, Repo: "library/nginx", Path: "/v2/library/nginx/tags/list"},
		},
		{
			name: "referrers",
			path: "/v2/library/nginx/referrers/" + digest,
			want: Parsed{Alias: DefaultAlias, Kind: KindReferrers, Repo: "library/nginx", Ref: digest, Path: "/v2/library/nginx/referrers/" + digest},
		},
		{
			// The repository itself contains the marker segment; the last marker wins.
			name: "repo containing marker word",
			path: "/v2/team/manifests/blobs/manifests/v1",
			want: Parsed{Alias: DefaultAlias, Kind: KindManifest, Repo: "team/manifests/blobs", Ref: "v1", Path: "/v2/team/manifests/blobs/manifests/v1"},
		},
		{
			name:    "traversal is rejected",
			path:    "/v2/../../etc/passwd",
			wantErr: true,
		},
		{
			name:    "encoded traversal in repo is rejected",
			path:    "/v2/library/..%2F..%2Fx/manifests/latest",
			wantErr: true,
		},
		{
			name:    "reference cannot contain slash",
			path:    "/v2/library/nginx/manifests/a/b",
			wantErr: true,
		},
		{
			name:    "blob requires digest",
			path:    "/v2/library/nginx/blobs/latest",
			wantErr: true,
		},
		{
			// "v2" is only reserved as an alias prefix, not as a repository component.
			name: "v2 as repository component",
			path: "/v2/v2/library/nginx/manifests/latest",
			want: Parsed{Alias: DefaultAlias, Kind: KindManifest, Repo: "v2/library/nginx", Ref: "latest", Path: "/v2/v2/library/nginx/manifests/latest"},
		},
		{
			name:    "unknown root path",
			path:    "/healthz",
			wantErr: true,
		},
		{
			name:    "uppercase repo is rejected",
			path:    "/v2/Library/Nginx/manifests/latest",
			wantErr: true,
		},
		{
			name:    "empty repo",
			path:    "/v2//manifests/latest",
			wantErr: true,
		},
		{
			name:    "repo longer than 255",
			path:    "/v2/" + strings.Repeat("a", 256) + "/manifests/latest",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.path)
			if tc.wantErr {
				if !errors.Is(err, ErrNotAPIPath) {
					t.Fatalf("Parse(%q) error = %v, want ErrNotAPIPath", tc.path, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tc.path, err)
			}
			if got != tc.want {
				t.Fatalf("Parse(%q) = %+v, want %+v", tc.path, got, tc.want)
			}
		})
	}
}

func TestValidAlias(t *testing.T) {
	valid := []string{"docker.io", "ghcr.io", "quay.io", "registry-local", "a"}
	invalid := []string{"", "v2", "Docker.io", "-bad", "bad-", "host:5000", "a/b", "a_b"}
	for _, s := range valid {
		if !ValidAlias(s) {
			t.Errorf("ValidAlias(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if ValidAlias(s) {
			t.Errorf("ValidAlias(%q) = true, want false", s)
		}
	}
}

func FuzzParse(f *testing.F) {
	f.Add("/v2/library/nginx/manifests/latest")
	f.Add("/ghcr.io/v2/owner/repo/blobs/" + digest)
	f.Add("/v2/../x")
	f.Fuzz(func(t *testing.T, p string) {
		got, err := Parse(p)
		if err != nil {
			return
		}
		// A successful parse must never produce a path that escapes the API root.
		if !strings.HasPrefix(got.Path, "/v2/") {
			t.Fatalf("canonical path %q lost the /v2/ prefix", got.Path)
		}
		// Tags may contain dots, so only whole "." and ".." segments are traversal.
		for segment := range strings.SplitSeq(got.Path, "/") {
			if segment == "." || segment == ".." {
				t.Fatalf("canonical path %q contains traversal segment %q", got.Path, segment)
			}
		}
	})
}
