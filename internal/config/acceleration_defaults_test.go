package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAccelerationDefaultsAndOptOut(t *testing.T) {
	for _, tc := range []struct {
		name, fields string
		race, adaptive bool
	}{
		{"omitted", "{}", true, true},
		{"partial", "{race: {delay: 1s}, adaptive: {window: 2s}}", true, true},
		{"disabled", "{race: {enabled: false}, adaptive: {enabled: false}}", false, false},
		{"mixed", "{race: {enabled: false}}", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Decode([]byte("registries:\n  docker.io: "+tc.fields+"\n  ghcr.io: {}\n"))
			if err != nil { t.Fatal(err) }
			r := cfg.Registries["docker.io"]
			if r.Race.Enabled != tc.race || r.Adaptive.Enabled != tc.adaptive || r.Race.MaxParallel != 3 {
				t.Fatalf("unexpected defaults: %+v", r)
			}
			other := cfg.Registries["ghcr.io"]
			if !other.Race.Enabled || !other.Adaptive.Enabled { t.Fatal("defaults missing for additional registry") }
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := Save(path, cfg); err != nil { t.Fatal(err) }
			reloaded, err := Load(path)
			if err != nil { t.Fatal(err) }
			got := reloaded.Registries["docker.io"]
			if got.Race != r.Race || got.Adaptive != r.Adaptive { t.Fatal("save/load changed opt-out") }
		})
	}
}

func TestAccelerationDefaultsAfterFragmentMerge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("registries:\n  docker.io:\n    race: {enabled: false}\n    adaptive: {enabled: false}\n"), 0o600); err != nil { t.Fatal(err) }
	if err := os.MkdirAll(DropInDir(path), 0o700); err != nil { t.Fatal(err) }
	fragment := filepath.Join(DropInDir(path), "mirrors.yaml")
	if err := os.WriteFile(fragment, []byte("registries:\n  docker.io:\n    race: {max_parallel: 4}\n    adaptive: {window: 2s}\n  ghcr.io: {}\n"), 0o600); err != nil { t.Fatal(err) }
	cfg, err := Load(path)
	if err != nil { t.Fatal(err) }
	r := cfg.Registries["docker.io"]
	if r.Race.Enabled || r.Adaptive.Enabled || r.Race.MaxParallel != 4 { t.Fatalf("fragment undid explicit opt-out: %+v", r) }
	if !cfg.Registries["ghcr.io"].Race.Enabled || !cfg.Registries["ghcr.io"].Adaptive.Enabled { t.Fatal("fragment-only registry missing defaults") }
}
