package daemonconf

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"github.com/runui/oci-mirror-proxy/internal/ociapi"
)

// LegacyRenderer edits daemon.json. The legacy store accepts a mirror list for
// docker.io only, so other registries are rejected instead of being silently ignored.
type LegacyRenderer struct {
	DaemonJSON string
}

// Store returns StoreLegacy.
func (LegacyRenderer) Store() StoreKind { return StoreLegacy }

// Render merges the proxy into registry-mirrors and keeps every other key intact.
func (r LegacyRenderer) Render(t Target, read ReadFunc) (Plan, error) {
	if len(t.Routes) == 0 {
		return Plan{}, ErrNoRoutes
	}
	for _, rt := range t.Routes {
		if rt.Alias != ociapi.DefaultAlias {
			return Plan{}, fmt.Errorf("the legacy image store can only mirror %s; registry %s needs the containerd image store", ociapi.DefaultAlias, rt.Alias)
		}
	}

	existing, err := read(r.DaemonJSON)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Plan{}, fmt.Errorf("read %s: %w", r.DaemonJSON, err)
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
	merged := []string{t.ProxyURL}
	for _, m := range mirrors {
		if m != t.ProxyURL {
			merged = append(merged, m)
		}
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
	return Plan{
		Store:       StoreLegacy,
		Files:       []File{{Path: r.DaemonJSON, Content: content, Mode: 0o644}},
		NeedsReload: true,
		Notes:       []string{"dockerd must be reloaded (SIGHUP) for registry-mirrors to take effect"},
	}, nil
}

// decodeDaemonConfig parses daemon.json as a generic object, so unknown keys survive.
// An empty file is treated as an empty object.
func decodeDaemonConfig(data []byte) (map[string]json.RawMessage, error) {
	cfg := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(data)) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("not valid JSON, refusing to modify it: %w", err)
	}
	return cfg, nil
}

func currentMirrors(cfg map[string]json.RawMessage) ([]string, error) {
	raw, ok := cfg["registry-mirrors"]
	if !ok {
		return nil, nil
	}
	var mirrors []string
	if err := json.Unmarshal(raw, &mirrors); err != nil {
		return nil, fmt.Errorf("registry-mirrors must be a list of strings: %w", err)
	}
	return mirrors, nil
}

func encodeDaemonConfig(cfg map[string]json.RawMessage) ([]byte, error) {
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode daemon.json: %w", err)
	}
	return append(out, '\n'), nil
}
