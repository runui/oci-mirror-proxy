package cli

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/runui/oci-mirror-proxy/internal/config"
)

func cmdRemoteShow(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("remote show", stderr)
	raw := fs.String("url", defaultRemoteURL, "remote JSON URL (defaults to the repository catalog)")
	region := fs.String("regions", "", "comma-separated regions (empty shows all sites)")
	if err := parseRemoteFlags(fs, args); err != nil {
		return err
	}
	u, err := remoteShowURL(*raw)
	if err != nil {
		return err
	}
	var regions []string
	if *region != "" {
		regions, err = cleanRegions(*region)
		if err != nil {
			return fmt.Errorf("%w: %w", errUsage, err)
		}
	}
	if strings.HasPrefix(u, "http:") {
		fmt.Fprintf(stderr, "warning: unencrypted remote %s\n", displayRemoteURL(u))
	}
	catalog, err := fetchCatalog(ctx, u)
	if err != nil {
		return fmt.Errorf("%s: %w", displayRemoteURL(u), err)
	}
	mirrors, err := selectCatalogMirrors(catalog, regions)
	if err != nil {
		return err
	}
	return printCatalogMirrors(stdout, mirrors)
}

func remoteShowURL(raw string) (string, error) {
	u, err := config.NormalizeURL(cmp.Or(raw, defaultRemoteURL))
	if err != nil {
		// Validation errors must not echo a subscription URL's private query token.
		return "", fmt.Errorf("%w: invalid remote URL", errUsage)
	}
	return u, nil
}

func selectCatalogMirrors(c remoteCatalog, regions []string) ([]catalogMirror, error) {
	wanted := make(map[string]bool, len(regions))
	for _, r := range regions {
		if _, ok := c.Regions[r]; !ok {
			available := slices.Sorted(maps.Keys(c.Regions))
			return nil, fmt.Errorf("unknown region %q (available: %v)", r, available)
		}
		wanted[r] = true
	}
	var out []catalogMirror
	// Keep site identity and catalog order; upstream selection flattens and
	// deduplicates endpoints, losing the information needed for inspection.
	for _, m := range c.Mirrors {
		// An empty filter includes every site; explicit regions use OR matching.
		match := len(wanted) == 0
		for _, r := range m.Regions {
			match = match || wanted[r]
		}
		if match {
			out = append(out, m)
		}
	}
	return out, nil
}

func printCatalogMirrors(out io.Writer, mirrors []catalogMirror) error {
	if len(mirrors) == 0 {
		_, err := fmt.Fprintln(out, "no matching mirrors")
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "NAME\tREGIONS\tREGISTRY\tURL\tWEBSITE"); err != nil {
		return err
	}
	for _, m := range mirrors {
		website := "-"
		if m.Website != "" {
			website = displayRemoteURL(m.Website)
		}
		for _, e := range m.Endpoints {
			if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", m.Name, strings.Join(m.Regions, ","), e.Registry, e.URL, website); err != nil {
				return err
			}
		}
	}
	return tw.Flush()
}
