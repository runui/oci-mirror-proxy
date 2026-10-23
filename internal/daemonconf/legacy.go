package daemonconf

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"

	"github.com/runui/oci-mirror-proxy/internal/ociapi"
)

// LegacyRenderer edits daemon.json. The legacy store accepts a mirror list for
// docker.io only, so other registries are skipped with warnings.
type LegacyRenderer struct {
	DaemonJSON string
}

// Store returns StoreLegacy.
func (LegacyRenderer) Store() StoreKind { return StoreLegacy }

// Render merges the proxy into registry-mirrors and keeps every other key intact.
func (r LegacyRenderer) Render(t Target, read ReadFunc) (Plan, error) {
	if len(t.Routes) == 0 && !t.Remove {
		return Plan{}, ErrNoRoutes
	}
	plan := Plan{Store: StoreLegacy}
	hasDockerHub := false
	for _, rt := range t.Routes {
		if rt.Alias != ociapi.DefaultAlias {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("legacy image store only supports %s mirrors; skipping %s (requires the containerd image store)", ociapi.DefaultAlias, rt.Alias))
		} else {
			hasDockerHub = true
		}
	}
	if !hasDockerHub && !t.Remove {
		plan.Notes = append(plan.Notes, "no supported registries to configure; nothing to apply")
		return plan, nil
	}

	existing, err := read(r.DaemonJSON)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Plan{}, fmt.Errorf("read %s: %w", r.DaemonJSON, err)
	}
	if !exists && t.Remove {
		return plan, nil
	}
	if exists && len(bytes.TrimSpace(existing)) == 0 {
		return Plan{}, fmt.Errorf("%s: existing configuration is empty", r.DaemonJSON)
	}
	cfg, err := decodeDaemonConfig(existing)
	if err != nil {
		return Plan{}, fmt.Errorf("%s: %w", r.DaemonJSON, err)
	}

	mirrors, err := currentMirrors(cfg)
	if err != nil {
		return Plan{}, fmt.Errorf("%s: %w", r.DaemonJSON, err)
	}
	// The proxy goes first so dockerd tries it first. Other mirrors stay as fallbacks.
	merged := []string{}
	if !t.Remove {
		merged = append(merged, t.ProxyURL)
	}
	for _, m := range mirrors {
		if !sameMirror(m, t.ProxyURL) {
			merged = append(merged, m)
		}
	}
	if slices.Equal(mirrors, merged) {
		// Preserve original bytes when already configured, including formatting.
		content := existing
		if !exists {
			return plan, nil
		}
		plan.Files = []File{{Path: r.DaemonJSON, Content: content, Mode: 0o644, CheckBefore: new(string(existing)), BeforeExists: exists}}
		return plan, nil
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		return Plan{}, err
	}
	cfg["registry-mirrors"] = raw

	content, err := encodeDaemonConfig(cfg)
	if err != nil {
		return Plan{}, err
	}
	plan.Files = []File{{Path: r.DaemonJSON, Content: content, Mode: 0o644, CheckBefore: new(string(existing)), BeforeExists: exists}}
	plan.NeedsReload = true
	plan.Notes = []string{"dockerd must be reloaded (SIGHUP) for registry-mirrors to take effect"}
	return plan, nil
}

// decodeDaemonConfig parses daemon.json as a generic object, so unknown keys survive.
// An empty file is treated as an empty object.
func decodeDaemonConfig(data []byte) (map[string]json.RawMessage, error) {
	cfg := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(data)) == 0 {
		return cfg, nil
	}
	// Reject duplicate keys before decoding: a map would silently discard one.
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := uniqueJSON(dec); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing JSON")
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("not valid JSON, refusing to modify it: %w", err)
	}
	if cfg == nil {
		return nil, fmt.Errorf("daemon configuration must be a JSON object")
	}
	return cfg, nil
}

func currentMirrors(cfg map[string]json.RawMessage) ([]string, error) {
	raw, ok := cfg["registry-mirrors"]
	if !ok {
		return nil, nil
	}
	var mirrors []string
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, fmt.Errorf("registry-mirrors must be a list of strings")
	}
	if err := json.Unmarshal(raw, &mirrors); err != nil {
		return nil, fmt.Errorf("registry-mirrors must be a list of strings: %w", err)
	}
	// encoding/json accepts null into a string as its zero value. Inspect each
	// element as well so malformed entries cannot be silently rewritten to "".
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if value := bytes.TrimSpace(entry); len(value) == 0 || value[0] != '"' {
			return nil, fmt.Errorf("registry-mirrors must contain only strings")
		}
	}
	return mirrors, nil
}

// sameMirror only normalizes the optional root slash. Scheme and non-root paths
// are significant and must not cause an unrelated mirror to be removed.
func sameMirror(a, b string) bool {
	root := func(s string) string {
		if i := strings.Index(s, "://"); i >= 0 {
			tail := s[i+3:]
			if strings.HasSuffix(tail, "/") && strings.Count(tail, "/") == 1 {
				return strings.TrimSuffix(s, "/")
			}
		}
		return s
	}
	return root(a) == root(b)
}

func uniqueJSON(d *json.Decoder) error {
	tok, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	for d.More() {
		if delim == '{' {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return fmt.Errorf("invalid object key")
			}
			if seen[name] {
				return fmt.Errorf("duplicate JSON key %q", name)
			}
			seen[name] = true
		}
		if err := uniqueJSON(d); err != nil {
			return err
		}
	}
	_, err = d.Token()
	return err
}

func encodeDaemonConfig(cfg map[string]json.RawMessage) ([]byte, error) {
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode daemon.json: %w", err)
	}
	return append(out, '\n'), nil
}
