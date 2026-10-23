package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/config"
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
		return fmt.Errorf("%w: want remote list, show, update or remove", errUsage)
	}
	action := args[0]
	// Inspect catalogs before resolving configuration: show needs no local files.
	if action == "show" {
		return cmdRemoteShow(ctx, args[1:], stdout, stderr)
	}
	if action != "list" && action != "update" && action != "remove" {
		return fmt.Errorf("%w: unknown remote action %q", errUsage, action)
	}
	fs := newFlagSet("remote "+action, stderr)
	configFlag := fs.String("config", "", "deprecated compatibility flag; ignored")
	socket := fs.String("admin-socket", "", "explicit admin endpoint")
	var raw, region *string
	var all, dry, noReload, allowEmpty *bool
	if action != "list" {
		raw = fs.String("url", "", "remote JSON URL (update defaults to the repository catalog)")
		dry = fs.Bool("dry-run", false, "preview without writing files or reloading")
		noReload = fs.Bool("no-reload", false, "write files without notifying the proxy")
	}
	if action == "update" {
		region = fs.String("region", "", "comma-separated regions (empty selects all sites)")
		all = fs.Bool("all", false, "update every configured remote")
		allowEmpty = fs.Bool("allow-empty", false, "allow an empty selected mirror list")
	}
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	_ = configFlag
	client, err := adminClient(*socket, "")
	if err != nil {
		return err
	}
	if action == "list" {
		files, scanErr := scanRemoteClient(client)
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
	savedRegions := map[string][]string{}
	if action == "update" && *all {
		if *raw != "" || *region != "" {
			return fmt.Errorf("%w: --all cannot be combined with --url or --region", errUsage)
		}
		files, err := scanRemoteClient(client)
		if err != nil {
			return err
		}
		for _, f := range files {
			urls = append(urls, f.meta.URL)
			savedRegions[f.meta.URL] = f.meta.Regions
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
		selection := regions
		if all != nil && *all {
			selection = savedRegions[u]
		}
		mutated, e := editRemoteClient(client, u, selection, catalog, action, *dry, allowEmpty != nil && *allowEmpty, stdout)
		// Successful writes are reloaded together after processing all sources.
		changed = changed || mutated
		if e != nil {
			errs = append(errs, fmt.Errorf("%s: %w", displayRemoteURL(u), e))
			continue
		}
		succeeded++
	}
	// An unchanged retry still reloads: the previous write may have succeeded
	// while its reload failed. Idempotence must not trap the service on old data.
	if (changed || succeeded > 0) && !*dry {
		if *noReload {
			fmt.Fprintln(stdout, "configuration saved; reload skipped")
		} else {
			e := client.Reload()
			switch {
			case e == nil:
				fmt.Fprintln(stdout, "configuration reloaded")
			case errors.Is(e, os.ErrNotExist) || errors.Is(e, syscall.ECONNREFUSED):
				errs = append(errs, fmt.Errorf("files saved, but proxy disconnected before reload: %w", e))
			default:
				errs = append(errs, fmt.Errorf("files saved, but running configuration is not confirmed: %w", e))
			}
		}
	}
	return errors.Join(errs...)
}
