package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"

	"github.com/runui/oci-mirror-proxy/internal/config"
	"github.com/runui/oci-mirror-proxy/internal/ociapi"
)

type remoteCatalog struct {
	Version int               `json:"version"`
	Regions map[string]string `json:"regions"`
	Mirrors []catalogMirror   `json:"mirrors"`
}
type catalogMirror struct {
	Name      string            `json:"name"`
	Website   string            `json:"website,omitempty"`
	Regions   []string          `json:"regions"`
	Endpoints []catalogEndpoint `json:"endpoints"`
}
type catalogEndpoint struct {
	Registry string `json:"registry"`
	URL      string `json:"url"`
}

var regionPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// rejectDuplicateJSON walks tokens rather than decoding into a map: normal
// encoding/json decoding silently accepts duplicate keys (last value wins).
func rejectDuplicateJSON(dec *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("JSON nesting exceeds 64 levels")
	}
	t, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			s, ok := key.(string)
			if !ok {
				return errors.New("expected JSON object key")
			}
			if seen[s] {
				return fmt.Errorf("duplicate JSON key %q", s)
			}
			seen[s] = true
			if err := rejectDuplicateJSON(dec, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := rejectDuplicateJSON(dec, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	_, err = dec.Token()
	return err
}

func strictJSON(data []byte, out any) error {
	check := json.NewDecoder(bytes.NewReader(data))
	if err := rejectDuplicateJSON(check, 0); err != nil {
		return err
	}
	if _, err := check.Token(); err != io.EOF {
		return errors.New("expected exactly one JSON value")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}

func decodeCatalog(data []byte) (remoteCatalog, error) {
	var c remoteCatalog
	if err := strictJSON(data, &c); err != nil {
		return c, err
	}
	if c.Version != 1 {
		return c, fmt.Errorf("unsupported catalog version %d", c.Version)
	}
	if len(c.Regions) == 0 {
		return c, errors.New("catalog requires regions")
	}
	for id, name := range c.Regions {
		if !regionPattern.MatchString(id) || name == "" {
			return c, fmt.Errorf("invalid region %q", id)
		}
	}
	if c.Mirrors == nil {
		return c, errors.New("catalog requires mirrors array")
	}
	for i, m := range c.Mirrors {
		if m.Name == "" || len(m.Regions) == 0 || len(m.Endpoints) == 0 {
			return c, fmt.Errorf("mirror #%d requires name, regions and endpoints", i+1)
		}
		if m.Website != "" {
			if _, err := config.NormalizeURL(m.Website); err != nil {
				return c, fmt.Errorf("mirror #%d: invalid website", i+1)
			}
		}
		seenRegions := map[string]bool{}
		for _, r := range m.Regions {
			if seenRegions[r] {
				return c, fmt.Errorf("mirror #%d repeats region %q", i+1, r)
			}
			seenRegions[r] = true
			if _, ok := c.Regions[r]; !ok {
				return c, fmt.Errorf("mirror #%d references unknown region %q", i+1, r)
			}
		}
		registries := map[string]bool{}
		for j, e := range m.Endpoints {
			if !ociapi.ValidAlias(e.Registry) || registries[e.Registry] {
				return c, fmt.Errorf("mirror #%d: invalid or duplicate registry %q", i+1, e.Registry)
			}
			registries[e.Registry] = true
			u, err := config.NormalizeURL(e.URL)
			if err != nil {
				return c, fmt.Errorf("mirror #%d: invalid endpoint URL", i+1)
			}
			// Reuse the proxy's validation (including no endpoint query string)
			// without accepting arbitrary proxy configuration from the catalog.
			test := config.Default()
			reg := test.Registries[ociapi.DefaultAlias]
			reg.Upstreams = []config.Upstream{{URL: u}}
			test.Registries[ociapi.DefaultAlias] = reg
			test.ApplyDefaults()
			if err := test.Validate(); err != nil {
				return c, fmt.Errorf("mirror #%d: %w", i+1, err)
			}
			c.Mirrors[i].Endpoints[j].URL = u
		}
	}
	return c, nil
}

func (c remoteCatalog) selectEndpoints(regions []string) (map[string][]config.Upstream, error) {
	wanted := map[string]bool{}
	for _, r := range regions {
		if _, ok := c.Regions[r]; !ok {
			var names []string
			for k := range c.Regions {
				names = append(names, k)
			}
			sort.Strings(names)
			return nil, fmt.Errorf("unknown region %q (available: %v)", r, names)
		}
		wanted[r] = true
	}
	out := map[string][]config.Upstream{}
	seen := map[string]bool{}
	for _, m := range c.Mirrors {
		match := false
		for _, r := range m.Regions {
			match = match || wanted[r]
		}
		if !match {
			continue
		}
		for _, e := range m.Endpoints {
			key := e.Registry + "\x00" + e.URL
			if seen[key] {
				continue
			}
			seen[key] = true
			// Anonymous Bearer challenges require token auth; noneAuth deliberately
			// does not answer challenges. No credentials are accepted remotely.
			out[e.Registry] = append(out[e.Registry], config.Upstream{URL: e.URL, Auth: config.Auth{Type: config.AuthToken}})
		}
	}
	return out, nil
}
