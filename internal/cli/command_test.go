package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
)

func commandPaths(root *cobra.Command) [][]string {
	paths := [][]string{{}}
	var visit func(*cobra.Command, []string)
	visit = func(cmd *cobra.Command, prefix []string) {
		for _, child := range cmd.Commands() {
			path := append(append([]string{}, prefix...), child.Name())
			paths = append(paths, path)
			visit(child, path)
		}
	}
	visit(root, nil)
	return paths
}

func TestEveryCommandHelp(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OCI_MIRROR_PROXY_CONFIG_HOME", root)
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	before := directorySnapshot(t, root)
	for _, path := range commandPaths(newRootCommand(t.Context(), io.Discard, io.Discard)) {
		t.Run(strings.Join(path, "/"), func(t *testing.T) {
			var want string
			calls := [][]string{
				append(append([]string{}, path...), "-h"),
				append(append([]string{}, path...), "--help"),
				append([]string{"help"}, path...),
			}
			if len(path) > 0 {
				calls = append(calls, append([]string{path[0], "help"}, path[1:]...))
				// A leaf's trailing "help" remains an ordinary positional argument.
				if len(path) == 1 && !childCommand(newRootCommand(t.Context(), io.Discard, io.Discard), path[0]).HasSubCommands() {
					calls = calls[:len(calls)-1]
				}
			}
			for _, args := range calls {
				code, out, stderr := run(t, args...)
				if code != 0 || stderr != "" || !strings.Contains(out, "Usage:") {
					t.Fatalf("%v: exit=%d stdout=%q stderr=%q", args, code, out, stderr)
				}
				if want != "" && out != want {
					t.Fatalf("help differs for %v", args)
				}
				want = out
			}
		})
	}
	if after := directorySnapshot(t, root); !reflect.DeepEqual(before, after) {
		t.Fatalf("help changed files: before=%v after=%v", before, after)
	}
}

func TestCommandUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{}, {"upstream"}, {"remote"}, {"server"}, {"dockerd"},
		{"unknown"}, {"upstream", "unknown"}, {"remote", "unknown"},
		{"server", "unknown"}, {"dockerd", "unknown"},
		{"help", "unknown"}, {"help", "upstream", "unknown"}, {"upstream", "help", "unknown"},
		{"upstream", "add", "--unknown", "--help"},
		{"upstream", "add", "-hv"},
		{"upstream", "--", "list"},
		{"upstream", "--unknown", "list"},
		{"upstream", "add", "--url"}, {"upstream", "add", "--", "--help"},
		{"upstream", "add", "extra", "--help"},
		{"init", "--force=invalid", "--help"},
	} {
		code, out, stderr := run(t, args...)
		if code != 2 || out != "" || stderr == "" {
			t.Errorf("%v: exit=%d stdout=%q stderr=%q", args, code, out, stderr)
		}
	}
}

func TestLegacyHelpStopsParsing(t *testing.T) {
	for _, args := range [][]string{
		{"upstream", "add", "--help", "--unknown"},
		{"upstream", "add", "--help=false"},
		{"upstream", "add", "-h=false"},
		{"remote", "show", "-help", "extra"},
	} {
		if code, out, stderr := run(t, args...); code != 0 || stderr != "" || !strings.Contains(out, "Usage:") {
			t.Fatalf("%v: %d %q %q", args, code, out, stderr)
		}
	}
}

func TestLegacyFlagBoundaries(t *testing.T) {
	root := newRootCommand(t.Context(), io.Discard, io.Discard)
	cmd := childCommand(childCommand(root, "remote"), "show")
	for _, tc := range []struct{ in, want []string }{
		{[]string{"-url", "--help", "-region=cn"}, []string{"--url", "--help", "--regions=cn"}},
		{[]string{"--url=--help"}, []string{"--url=--help"}},
		{[]string{"--url", "-region"}, []string{"--url", "-region"}},
		{[]string{"--", "-region", "cn"}, []string{"--", "-region", "cn"}},
		{[]string{"extra", "-region=cn"}, []string{"extra", "-region=cn"}},
	} {
		original := append([]string{}, tc.in...)
		if got := legacyFlags(cmd, tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%v: got %v, want %v", tc.in, got, tc.want)
		}
		if !reflect.DeepEqual(tc.in, original) {
			t.Fatal("modified caller arguments")
		}
	}
	for _, args := range [][]string{{"--url", "--help"}, {"--url=--help"}} {
		code, out, _ := run(t, append([]string{"remote", "show"}, args...)...)
		if code != 2 || out != "" {
			t.Fatalf("value interpreted as help: %v", args)
		}
	}
}

func TestLegacyInitAndIndependentExecutions(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "first.yaml"), filepath.Join(root, "second.yaml")
	code, _, stderr := run(t, "init", "-config", first, "-listen=127.0.0.1:25000", "-force=false")
	if code != 0 {
		t.Fatalf("legacy init: %d %s", code, stderr)
	}
	// Repeated options retain last-value-wins behavior, including values starting with '-'.
	code, _, stderr = run(t, "init", "--config", "--help", "--config", second)
	if code != 0 {
		t.Fatalf("second init: %d %s", code, stderr)
	}
	if _, err := os.Stat(second); err != nil {
		t.Fatal(err)
	}
	code, _, _ = run(t, "init", "--config", first)
	if code != 1 {
		t.Fatalf("flag state leaked: exit=%d", code)
	}
	if code, out, stderr := run(t, "version", "--unknown", "extra"); code != 0 || stderr != "" || strings.TrimSpace(out) != version {
		t.Fatalf("version compatibility: %d %q %q", code, out, stderr)
	}
}

func TestHelpDoesNotContactEndpoints(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	for _, args := range [][]string{
		{"remote", "show", "--url", server.URL, "--help"},
		{"remote", "update", "--url", server.URL, "--admin-socket", "missing", "--help"},
		{"dockerd", "apply", "--server-info", "missing", "--help"},
		{"serve", "--config", "missing", "--help"},
	} {
		if code, _, stderr := run(t, args...); code != 0 || stderr != "" {
			t.Fatalf("%v: %d %s", args, code, stderr)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("help contacted the remote catalog")
	}
}
