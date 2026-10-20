package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/runui/oci-mirror-proxy/internal/config"
	"github.com/runui/oci-mirror-proxy/internal/fsutil"
)

const remoteMarker = "# Managed by oci-mirror-proxy remote."
const remotePrefix = "# remote: "

type remoteMeta struct {
	Version int      `json:"version"`
	URL     string   `json:"url"`
	Regions []string `json:"regions"`
}
type remoteFile struct {
	path string
	data []byte
	meta remoteMeta
}

var unsafeHost = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

func remotePath(main, raw string) (string, error) {
	canonical, err := config.NormalizeURL(raw)
	if err != nil {
		return "", err
	}
	u, _ := url.Parse(canonical)
	host := unsafeHost.ReplaceAllString(u.Hostname(), "_")
	if len(host) > 120 {
		host = host[:120]
	}
	hash := sha256.Sum256([]byte(canonical))
	return filepath.Join(config.DropInDir(main), fmt.Sprintf("%s@%x.yaml", host, hash[:8])), nil
}

func parseRemoteMeta(data []byte) (remoteMeta, bool, error) {
	var m remoteMeta
	if !bytes.HasPrefix(data, []byte(remoteMarker+"\n")) {
		return m, false, nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "#") {
			break
		}
		if strings.HasPrefix(line, remotePrefix) {
			if err := strictJSON([]byte(strings.TrimPrefix(line, remotePrefix)), &m); err != nil {
				return m, true, err
			}
			canonical, err := config.NormalizeURL(m.URL)
			if err != nil || m.Version != 1 || canonical != m.URL || len(m.Regions) == 0 {
				return m, true, errors.New("invalid remote metadata")
			}
			for _, r := range m.Regions {
				if !regionPattern.MatchString(r) {
					return m, true, errors.New("invalid region in remote metadata")
				}
			}
			return m, true, nil
		}
	}
	return m, true, errors.New("missing remote metadata")
}

func scanRemotes(main string) ([]remoteFile, error) {
	paths, err := config.ConfigPaths(main)
	if err != nil {
		return nil, err
	}
	var out []remoteFile
	var errs []error
	seen := map[string]bool{}
	for _, p := range paths[1:] {
		data, err := os.ReadFile(p)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		m, managed, err := parseRemoteMeta(data)
		if !managed {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p, err))
			continue
		}
		expected, _ := remotePath(main, m.URL)
		if p != expected || seen[m.URL] {
			errs = append(errs, fmt.Errorf("%s: remote identity/path mismatch", p))
			continue
		}
		seen[m.URL] = true
		out = append(out, remoteFile{p, data, m})
	}
	return out, errors.Join(errs...)
}

func remoteYAML(meta remoteMeta, ups map[string][]config.Upstream) ([]byte, error) {
	// This wire type intentionally excludes IDs, credentials, TLS and timeouts.
	// Unset fields must stay absent so a source never overwrites local settings
	// merely because the normal configuration encoder emits zero-valued structs.
	type upstream struct {
		URL  string `yaml:"url"`
		Auth struct {
			Type string `yaml:"type"`
		} `yaml:"auth"`
	}
	type registry struct {
		Upstreams []upstream `yaml:"upstreams"`
	}
	body := struct {
		Registries map[string]registry `yaml:"registries"`
	}{Registries: map[string]registry{}}
	for alias, list := range ups {
		r := registry{}
		for _, u := range list {
			x := upstream{URL: u.URL}
			x.Auth.Type = config.AuthToken
			r.Upstreams = append(r.Upstreams, x)
		}
		body.Registries[alias] = r
	}
	data, err := yaml.Marshal(body)
	if err != nil {
		return nil, err
	}
	m, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	return []byte(remoteMarker + "\n" + remotePrefix + string(m) + "\n\n" + string(data)), nil
}

// commitRemote preserves exactly the previous on-disk result, including its
// metadata, before publishing a replacement. .bak never participates in merge.
// A crash after backup but before rename leaves the current result untouched.
func commitRemote(path string, before, after []byte) error {
	if bytes.Equal(before, after) {
		return nil
	}
	if before != nil {
		current, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(current, before) {
			return errors.New("remote changed concurrently")
		}
		if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
			return errors.New("remote must be a regular file")
		}
		if info, err := os.Lstat(path + ".bak"); err == nil && !info.Mode().IsRegular() {
			return errors.New("backup must be a regular file")
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := fsutil.WriteFileAtomic(path+".bak", before, 0o600); err != nil {
			return fmt.Errorf("backup previous remote: %w", err)
		}
	}
	if err := config.Commit(path, before, after); err != nil {
		return err
	}
	return nil
}

func cleanRegions(raw string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, r := range strings.Split(raw, ",") {
		r = strings.TrimSpace(r)
		if !regionPattern.MatchString(r) {
			return nil, fmt.Errorf("invalid region %q", r)
		}
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	sort.Strings(out)
	return out, nil
}

func displayRemoteURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<invalid URL>"
	}
	if u.RawQuery != "" {
		u.RawQuery = "REDACTED"
	}
	return u.String()
}
