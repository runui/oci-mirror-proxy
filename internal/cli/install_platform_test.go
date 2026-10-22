package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallerReleasePlatforms(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("installer requires a Unix environment")
	}
	for _, tc := range []struct {
		os, machine, platform, member string
		badChecksum                   bool
	}{
		{"Linux", "x86_64", "linux_amd64", "./oci-mirror-proxy", false},
		{"Linux", "aarch64", "linux_arm64", "oci-mirror-proxy", false},
		{"Linux", "armv7l", "linux_armv7", "oci-mirror-proxy", false},
		{"Linux", "riscv64", "linux_riscv64", "oci-mirror-proxy", false},
		{"Darwin", "x86_64", "darwin_amd64", "oci-mirror-proxy", false},
		{"Darwin", "arm64", "darwin_arm64", "oci-mirror-proxy", false},
		{"FreeBSD", "amd64", "freebsd_amd64", "oci-mirror-proxy", false},
		{"FreeBSD", "arm64", "freebsd_arm64", "oci-mirror-proxy", false},
		{"Darwin", "arm64", "darwin_arm64", "oci-mirror-proxy", true},
	} {
		t.Run(fmt.Sprintf("%s/corrupt=%t", tc.platform, tc.badChecksum), func(t *testing.T) {
			root := t.TempDir()
			tools := filepath.Join(root, "tools")
			if err := os.Mkdir(tools, 0o755); err != nil {
				t.Fatal(err)
			}
			uname := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in -s) echo %s ;; -m) echo %s ;; esac\n", tc.os, tc.machine)
			if err := os.WriteFile(filepath.Join(tools, "uname"), []byte(uname), 0o755); err != nil {
				t.Fatal(err)
			}
			binary := []byte("#!/bin/sh\nexit 0\n")
			var archive bytes.Buffer
			gz := gzip.NewWriter(&archive)
			tw := tar.NewWriter(gz)
			if err := tw.WriteHeader(&tar.Header{Name: tc.member, Mode: 0o755, Size: int64(len(binary))}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write(binary); err != nil {
				t.Fatal(err)
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			asset := "oci-mirror-proxy_v1.2.3_" + tc.platform + ".tar.gz"
			sum := fmt.Sprintf("%x", sha256.Sum256(archive.Bytes()))
			if tc.badChecksum {
				sum = strings.Repeat("0", 64)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/releases/download/v1.2.3/" + asset:
					_, _ = w.Write(archive.Bytes())
				case "/releases/download/v1.2.3/SHA256SUMS":
					_, _ = fmt.Fprintf(w, "%s  %s\n", sum, asset)
				default:
					http.Error(w, "unexpected request", http.StatusBadRequest)
				}
			}))
			defer server.Close()
			prefix := filepath.Join(root, "prefix")
			cmd := exec.CommandContext(t.Context(), "sh", "../../install.sh", "--version", "v1.2.3", "--prefix", prefix)
			cmd.Env = append(os.Environ(), "PATH="+tools+":"+os.Getenv("PATH"), "DOWNLOAD_BASE_URL="+server.URL, "HOME="+root, "XDG_CONFIG_HOME="+root, "TMPDIR="+root)
			out, err := cmd.CombinedOutput()
			installed, readErr := os.ReadFile(filepath.Join(prefix, "bin", "oci-mirror-proxy"))
			if tc.badChecksum {
				if err == nil || !bytes.Contains(out, []byte("SHA256 verification failed")) || !os.IsNotExist(readErr) {
					t.Fatalf("corrupt archive accepted: %v, %v\n%s", err, readErr, out)
				}
				return
			}
			if err != nil || readErr != nil || !bytes.Equal(installed, binary) {
				t.Fatalf("install failed: %v, %v\n%s", err, readErr, out)
			}
		})
	}
}
