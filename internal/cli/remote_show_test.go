package cli

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const showCatalog = `{"version":1,"regions":{"cn":"China","apac":"Asia Pacific","eu":"Europe"},"mirrors":[
{"name":"First","website":"https://first.example/?token=website-secret","regions":["cn","apac"],"endpoints":[{"registry":"docker.io","url":"https://mirror.example/hub"},{"registry":"ghcr.io","url":"https://mirror.example/ghcr"}]},
{"name":"Second","regions":["apac"],"endpoints":[{"registry":"docker.io","url":"https://mirror.example/hub"}]}]}`

func TestRemoteShow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" {
			t.Error("missing JSON accept header")
		}
		io.WriteString(w, showCatalog)
	}))
	defer server.Close()
	for _, tc := range []struct {
		name, region  string
		first, second int
	}{
		{"all", "", 2, 1},
		{"single", "cn", 2, 0},
		{"union", "cn,apac", 2, 1},
		{"clean", " apac, cn,apac ", 2, 1},
		{"none", "eu", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out, stderr := run(t, "remote", "show", "--url", server.URL+"?token=private-secret", "--region", tc.region)
			if code != 0 {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			if strings.Count(out, "First") != tc.first || strings.Count(out, "Second") != tc.second {
				t.Fatalf("unexpected site rows: %s", out)
			}
			if strings.Contains(out+stderr, "private-secret") || strings.Contains(out+stderr, "website-secret") {
				t.Fatalf("leaked query token: %s %s", out, stderr)
			}
			if !strings.Contains(stderr, "warning: unencrypted remote") {
				t.Fatalf("missing HTTP warning: %s", stderr)
			}
			if tc.first > 0 {
				for _, value := range []string{"NAME", "REGIONS", "REGISTRY", "WEBSITE", "cn,apac", "docker.io", "ghcr.io", "https://mirror.example/ghcr", "https://first.example/?REDACTED"} {
					if !strings.Contains(out, value) {
						t.Errorf("missing %q in %s", value, out)
					}
				}
			}
			if tc.second > 0 && strings.LastIndex(out, "First") > strings.Index(out, "Second") {
				t.Fatalf("catalog order changed: %s", out)
			}
			if tc.first+tc.second == 0 && out != "no matching mirrors\n" {
				t.Fatalf("unexpected empty output: %q", out)
			}
		})
	}
}

func TestRemoteShowErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body, region, want string
		status, code             int
	}{
		{"http", "", "", "HTTP 503", 503, 1},
		{"oversized", strings.Repeat(" ", maxCatalogSize+1), "", "catalog exceeds 4 MiB", 200, 1},
		{"json", "not json", "", "invalid", 200, 1},
		{"version", `{"version":2}`, "", "unsupported catalog version", 200, 1},
		{"duplicate", `{"version":1,"version":1}`, "", "duplicate JSON key", 200, 1},
		{"unknown field", `{"version":1,"extra":true}`, "", "unknown field", 200, 1},
		{"endpoint", strings.Replace(showCatalog, "https://mirror.example/hub", "file:///private", 1), "", "invalid endpoint URL", 200, 1},
		{"unknown region", showCatalog, "us", "available: [apac cn eu]", 200, 1},
		{"bad region", showCatalog, "CN", "invalid region", 200, 2},
		{"empty", `{"version":1,"regions":{"cn":"China"},"mirrors":[]}`, "", "no matching mirrors", 200, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer server.Close()
			code, out, stderr := run(t, "remote", "show", "--url", server.URL+"?token=private-secret", "--region", tc.region)
			if code != tc.code || !strings.Contains(out+stderr, tc.want) || strings.Contains(out+stderr, "private-secret") {
				t.Fatalf("exit %d, stdout %q, stderr %q", code, out, stderr)
			}
		})
	}
}

func TestRemoteShowFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--config", "missing.yaml"}, {"--all"}, {"--dry-run"}, {"--no-reload"},
		{"--admin-socket", "missing.sock"}, {"--allow-empty"}, {"extra"},
		{"--url", "file:///private?token=private-secret"},
	} {
		code, _, stderr := run(t, append([]string{"remote", "show"}, args...)...)
		if code != 2 || strings.Contains(stderr, "private-secret") {
			t.Fatalf("%v: exit %d: %s", args, code, stderr)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("help fetched the catalog")
	}))
	defer server.Close()
	code, _, stderr := run(t, "remote", "show", "--url", server.URL, "--help")
	if code != 0 || !strings.Contains(stderr, "-region") || !strings.Contains(stderr, "-url") || strings.Contains(stderr, "-config") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, raw := range []string{"", defaultRemoteURL} {
		if got, err := remoteShowURL(raw); err != nil || got != defaultRemoteURL {
			t.Fatalf("default URL: %q %v", got, err)
		}
	}
}

func TestRemoteShowDoesNotReadOrWriteConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	// A malformed local config must not prevent inspection or be rewritten.
	path := filepath.Join(root, "oci-mirror-proxy", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("invalid: [yaml")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, showCatalog)
	}))
	defer server.Close()
	for _, withConfig := range []bool{true, false} {
		if !withConfig {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
		before := directorySnapshot(t, root)
		if code, _, stderr := run(t, "remote", "show", "--url", server.URL); code != 0 {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		if after := directorySnapshot(t, root); !reflect.DeepEqual(before, after) {
			t.Fatalf("inspection changed files: before %v, after %v", before, after)
		}
	}
}

func directorySnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			files[path] = "directory"
			return nil
		}
		data, err := os.ReadFile(path)
		files[path] = string(data)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

type failingCatalogWriter struct{ err error }

func (w failingCatalogWriter) Write([]byte) (int, error) { return 0, w.err }

func TestPrintCatalogMirrorsWriteFailure(t *testing.T) {
	catalog, err := decodeCatalog([]byte(showCatalog))
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("output failed")
	for _, mirrors := range [][]catalogMirror{nil, catalog.Mirrors} {
		if err := printCatalogMirrors(failingCatalogWriter{want}, mirrors); !errors.Is(err, want) {
			t.Fatalf("got %v, want %v", err, want)
		}
	}
}

func TestRemoteShowCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("canceled request reached server")
	}))
	defer server.Close()
	if err := cmdRemoteShow(ctx, []string{"--url", server.URL}, io.Discard, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestRemoteShowDownloadSafety(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("HTTPS downgrade reached HTTP server")
	}))
	defer plain.Close()
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"?token=redirect-secret", http.StatusFound)
	}))
	defer tlsServer.Close()
	// Trust only this test server so the redirect policy, not certificate
	// rejection, determines the result. Restore the shared transport afterward.
	original := http.DefaultTransport
	http.DefaultTransport = tlsServer.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = original })
	code, _, stderr := run(t, "remote", "show", "--url", tlsServer.URL+"?token=source-secret")
	if code != 1 || !strings.Contains(stderr, "HTTPS downgrade redirect refused") || strings.Contains(stderr, "source-secret") || strings.Contains(stderr, "redirect-secret") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
}

func TestRemoteShowConnectionFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()
	code, _, stderr := run(t, "remote", "show", "--url", server.URL+"?token=private-secret")
	if code != 1 || !strings.Contains(stderr, "download catalog") || strings.Contains(stderr, "private-secret") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
}
