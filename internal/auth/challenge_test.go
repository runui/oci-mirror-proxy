package auth

import (
	"reflect"
	"testing"
)

func TestBearerChallengeParsing(t *testing.T) {
	tests := []struct {
		name    string
		headers []string
		want    map[string]string
		ok      bool
	}{
		{
			name:    "docker hub style",
			headers: []string{`Bearer realm="https://auth.docker.io/token",service="registry.docker.io",scope="repository:library/nginx:pull"`},
			want: map[string]string{
				"realm":   "https://auth.docker.io/token",
				"service": "registry.docker.io",
				"scope":   "repository:library/nginx:pull",
			},
			ok: true,
		},
		{
			name:    "quoted value containing commas",
			headers: []string{`Bearer realm="https://x/token",scope="repository:a:pull,push"`},
			want:    map[string]string{"realm": "https://x/token", "scope": "repository:a:pull,push"},
			ok:      true,
		},
		{
			name:    "unquoted values and extra spaces",
			headers: []string{`Bearer   realm=https://x/token , service=svc`},
			want:    map[string]string{"realm": "https://x/token", "service": "svc"},
			ok:      true,
		},
		{
			name:    "basic challenge is not bearer",
			headers: []string{`Basic realm="registry"`},
			ok:      false,
		},
		{
			name:    "bearer found after other schemes",
			headers: []string{`Basic realm="x"`, `Bearer realm="https://y/token"`},
			want:    map[string]string{"realm": "https://y/token"},
			ok:      true,
		},
		{
			name:    "no headers",
			headers: nil,
			ok:      false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := bearerChallenge(tc.headers)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if tc.ok && !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("params = %v, want %v", got, tc.want)
			}
		})
	}
}
