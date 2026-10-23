package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"sort"

	"github.com/runui/oci-mirror-proxy/internal/config"
)

// fragmentClient separates subscription interpretation from server-side byte
// storage. No method can select a server filesystem path or reload implicitly.
type fragmentClient interface {
	ListFragments() ([]config.Fragment, error)
	ReadFragment(string) (config.Fragment, error)
	WriteFragment(string, string, []byte) (config.Fragment, error)
}

func remoteName(raw string) (string, error) {
	path, err := remotePath("config.yaml", raw)
	return filepath.Base(path), err
}

func scanRemoteClient(client fragmentClient) ([]remoteFile, error) {
	files, err := client.ListFragments()
	if err != nil {
		return nil, err
	}
	var out []remoteFile
	var errs []error
	seen := map[string]bool{}
	for _, file := range files {
		f, err := client.ReadFragment(file.Name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		meta, managed, err := parseRemoteMeta(f.Data)
		if !managed {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f.Name, err))
			continue
		}
		expected, err := remoteName(meta.URL)
		if err != nil || expected != f.Name || seen[meta.URL] {
			errs = append(errs, fmt.Errorf("%s: remote identity/path mismatch", f.Name))
			continue
		}
		seen[meta.URL] = true
		out = append(out, remoteFile{path: f.Name, data: f.Data, meta: meta})
	}
	slices.SortFunc(out, func(a, b remoteFile) int { return bytes.Compare([]byte(a.path), []byte(b.path)) })
	return out, errors.Join(errs...)
}

func editRemoteClient(client fragmentClient, raw string, regions []string, catalog remoteCatalog, action string, dry, allowEmpty bool, out io.Writer) (bool, error) {
	name, err := remoteName(raw)
	if err != nil {
		return false, err
	}
	before, err := client.ReadFragment(name)
	if err != nil {
		return false, err
	}
	if before.Revision != "" {
		meta, managed, err := parseRemoteMeta(before.Data)
		if err != nil {
			return false, err
		}
		if !managed || meta.URL != raw {
			return false, errors.New("refusing to replace a file owned by another source")
		}
	} else if action == "remove" {
		return false, errors.New("remote is not configured")
	}
	var after []byte
	if action == "update" {
		if len(regions) == 0 {
			for region := range catalog.Regions {
				regions = append(regions, region)
			}
			sort.Strings(regions)
		}
		ups, err := catalog.selectEndpoints(regions)
		if err != nil {
			return false, err
		}
		if len(ups) == 0 && !allowEmpty {
			return false, errors.New("selection is empty; use --allow-empty or remote remove")
		}
		after, err = remoteYAML(remoteMeta{Version: 1, URL: raw, Regions: regions}, ups)
		if err != nil {
			return false, err
		}
	}
	if bytes.Equal(before.Data, after) {
		fmt.Fprintf(out, "unchanged %s\n", displayRemoteURL(raw))
		return false, nil
	}
	verb := "updated"
	if action == "remove" {
		verb = "removed"
	}
	if dry {
		fmt.Fprintf(out, "would %s %s -> %s\n", action, displayRemoteURL(raw), name)
		return false, nil
	}
	if _, err := client.WriteFragment(name, before.Revision, after); err != nil {
		return false, err
	}
	fmt.Fprintf(out, "%s %s -> %s\n", verb, displayRemoteURL(raw), name)
	return true, nil
}
