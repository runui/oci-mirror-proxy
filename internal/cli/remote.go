package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/admin"
	"github.com/runui/oci-mirror-proxy/internal/config"
	"github.com/runui/oci-mirror-proxy/internal/fsutil"
)

const defaultRemoteURL = "https://git.zmide.com/bin/oci-mirror-proxy/raw/branch/master/subscriptions/mirrors.json"
const maxCatalogSize = 4 << 20

func fetchCatalog(ctx context.Context, raw string) (remoteCatalog, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if _, err := config.NormalizeURL(req.URL.String()); err != nil {
			return err
		}
		if via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return errors.New("HTTPS downgrade redirect refused")
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return remoteCatalog{}, errors.New("invalid remote URL")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		var e *url.Error
		if errors.As(err, &e) {
			err = e.Err
		}
		return remoteCatalog{}, fmt.Errorf("download catalog: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return remoteCatalog{}, fmt.Errorf("download catalog: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogSize+1))
	if err != nil {
		return remoteCatalog{}, err
	}
	if len(data) > maxCatalogSize {
		return remoteCatalog{}, errors.New("catalog exceeds 4 MiB")
	}
	return decodeCatalog(data)
}

func cmdRemote(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: want remote list, update or remove", errUsage)
	}
	action := args[0]
	if action != "list" && action != "update" && action != "remove" {
		return fmt.Errorf("%w: unknown remote action %q", errUsage, action)
	}
	fs := newFlagSet("remote "+action, stderr)
	configFlag := fs.String("config", "", "main configuration file")
	var raw, region, socket *string
	var all, dry, noReload, allowEmpty *bool
	if action != "list" {
		raw = fs.String("url", "", "remote JSON URL (update defaults to the repository catalog)")
		dry = fs.Bool("dry-run", false, "preview without writing files or reloading")
		noReload = fs.Bool("no-reload", false, "write files without notifying the proxy")
		socket = fs.String("admin-socket", "", "admin socket to notify after writing")
	}
	if action == "update" {
		region = fs.String("region", "", "comma-separated regions (empty selects all sites)")
		all = fs.Bool("all", false, "update every configured remote")
		allowEmpty = fs.Bool("allow-empty", false, "allow an empty selected mirror list")
	}
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	main, err := resolveConfigPath(*configFlag)
	if err != nil {
		return err
	}
	if action == "list" {
		files, scanErr := scanRemotes(main)
		tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "URL\tREGIONS\tUPSTREAMS\tFILE")
		for _, f := range files {
			count := 0
			d, e := config.ParseDocument(f.path, f.data)
			if e == nil {
				var c config.Config
				e = d.Root.Decode(&c)
				if e == nil {
					for _, r := range c.Registries {
						count += len(r.Upstreams)
					}
				}
			}
			if e != nil {
				scanErr = errors.Join(scanErr, e)
			}
			fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", displayRemoteURL(f.meta.URL), strings.Join(f.meta.Regions, ","), count, f.path)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		return scanErr
	}
	if action == "remove" && *raw == "" {
		return fmt.Errorf("%w: remote remove requires --url", errUsage)
	}
	var urls []string
	if action == "update" && *all {
		if *raw != "" || *region != "" {
			return fmt.Errorf("%w: --all cannot be combined with --url or --region", errUsage)
		}
		files, err := scanRemotes(main)
		if err != nil {
			return err
		}
		for _, f := range files {
			urls = append(urls, f.meta.URL)
		}
	} else {
		u := *raw
		if u == "" {
			u = defaultRemoteURL
		}
		canonical, err := config.NormalizeURL(u)
		if err != nil {
			return err
		}
		urls = []string{canonical}
	}
	var regions []string
	if region != nil && *region != "" {
		regions, err = cleanRegions(*region)
		if err != nil {
			return fmt.Errorf("%w: %w", errUsage, err)
		}
	}
	if len(urls) == 0 {
		fmt.Fprintln(stdout, "no configured remotes")
		return nil
	}
	// Resolve the socket before deleting a fragment: that fragment may have
	// contributed config needed for discovery. Do not require a running proxy.
	var adminSocket string
	if !*noReload && !*dry {
		// A broken target fragment can be repaired/removed by this command.
		// Retry discovery after committing instead of blocking the repair.
		adminSocket, _ = resolveAdminSocket(*socket, main)
	}
	var errs []error
	changed := false
	succeeded := 0
	for _, u := range urls {
		if strings.HasPrefix(u, "http:") {
			fmt.Fprintf(stderr, "warning: unencrypted remote %s\n", displayRemoteURL(u))
		}
		var catalog remoteCatalog
		if action == "update" {
			catalog, err = fetchCatalog(ctx, u)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", displayRemoteURL(u), err))
				continue
			}
		}
		mutated, e := editRemote(ctx, main, u, regions, catalog, action, *dry, allowEmpty != nil && *allowEmpty, stdout)
		// A directory-sync error can follow a successful rename/unlink. Its
		// visible file change still needs a reload, even though we return 1.
		changed = changed || mutated
		if e != nil {
			errs = append(errs, fmt.Errorf("%s: %w", displayRemoteURL(u), e))
			continue
		}
		succeeded++
	}
	// Never wait for the server while holding the configuration lock: reload
	// reads the very files that remote editing just locked and replaced.
	// An unchanged retry still reloads: the previous write may have succeeded
	// while its reload failed. Idempotence must not trap the service on old data.
	if (changed || succeeded > 0) && !*dry {
		if *noReload {
			fmt.Fprintln(stdout, "configuration saved; reload skipped")
		} else {
			var e error
			if adminSocket == "" {
				adminSocket, e = resolveAdminSocket(*socket, main)
			}
			if e == nil {
				e = admin.NewClient(adminSocket).Reload()
			}
			switch {
			case e == nil:
				fmt.Fprintln(stdout, "configuration reloaded")
			case errors.Is(e, os.ErrNotExist) || errors.Is(e, syscall.ECONNREFUSED):
				fmt.Fprintln(stdout, "configuration saved; proxy is not running (effective at next start)")
			default:
				errs = append(errs, fmt.Errorf("files saved, but running configuration is not confirmed: %w", e))
			}
		}
	}
	return errors.Join(errs...)
}

func editRemote(ctx context.Context, main, raw string, regions []string, catalog remoteCatalog, action string, dry, allowEmpty bool, out io.Writer) (bool, error) {
	if !dry {
		lockCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		unlock, err := config.Lock(lockCtx, main)
		if err != nil {
			return false, err
		}
		defer unlock()
	}
	path, err := remotePath(main, raw)
	if err != nil {
		return false, err
	}
	before, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err == nil {
		if info, e := os.Lstat(path); e != nil || !info.Mode().IsRegular() {
			return false, errors.New("remote must be a regular file")
		}
		m, managed, e := parseRemoteMeta(before)
		if e != nil {
			return false, e
		}
		if !managed || m.URL != raw {
			return false, errors.New("refusing to replace a file owned by another source")
		}
	} else if action == "remove" {
		return false, errors.New("remote is not configured")
	}
	var after []byte
	if action == "update" {
		if len(regions) == 0 {
			regions = make([]string, 0, len(catalog.Regions))
			for r := range catalog.Regions {
				regions = append(regions, r)
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
	// Load all other documents directly so remove can repair a malformed target
	// fragment, and update can replace it with a newly validated candidate.
	paths, err := config.ConfigPaths(main)
	if err != nil {
		return false, err
	}
	b := &config.Bundle{Main: main}
	for _, p := range paths {
		if p == path {
			continue
		}
		data, e := os.ReadFile(p)
		if e != nil {
			return false, e
		}
		d, e := config.ParseDocument(p, data)
		if e != nil {
			return false, e
		}
		b.Documents = append(b.Documents, d)
	}
	snapshot := &config.Bundle{Main: main, Documents: append([]*config.Document(nil), b.Documents...)}
	if before != nil {
		snapshot.Documents = append(snapshot.Documents, &config.Document{Path: path, Data: before})
	}
	if err := b.Replace(path, after); err != nil {
		return false, err
	}
	if bytes.Equal(before, after) {
		fmt.Fprintf(out, "unchanged %s\n", displayRemoteURL(raw))
		return false, nil
	}
	verb := "updated"
	if action == "remove" {
		verb = "removed"
	}
	if dry {
		verb = "would " + action
	}
	if err := snapshot.CheckUnchanged(); err != nil {
		return false, err
	}
	if dry {
		fmt.Fprintf(out, "%s %s -> %s\n", verb, displayRemoteURL(raw), path)
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	if err := commitRemote(path, before, after); err != nil {
		if published, ok := errors.AsType[*fsutil.DurabilityError](err); ok && published.Path == path {
			return true, err
		}
		return false, err
	}
	fmt.Fprintf(out, "%s %s -> %s\n", verb, displayRemoteURL(raw), path)
	return true, nil
}
