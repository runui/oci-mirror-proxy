package config

import (
	"testing"
	"time"
)

func TestAdaptiveDefaultsAndValidation(t *testing.T) {
	c := Default()
	r := c.Registries["docker.io"]
	if r.Adaptive.Enabled || r.Adaptive.Window != 10*time.Second || r.Adaptive.ProbeTimeout != 5*time.Second || r.Adaptive.MinBytesPerSecond != 262144 || r.Adaptive.MaxSwitches != 3 {
		t.Fatalf("defaults=%+v", r.Adaptive)
	}
	r.Adaptive.Enabled = true
	c.Registries["docker.io"] = r
	encoded, err := Encode(c)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(encoded)
	if err != nil || decoded.Registries["docker.io"].Adaptive != r.Adaptive {
		t.Fatalf("roundtrip: %v", err)
	}
	for _, field := range []string{"window", "timeout", "speed", "switches"} {
		t.Run(field, func(t *testing.T) {
			bad := r
			switch field {
			case "window":
				bad.Adaptive.Window = -1
			case "timeout":
				bad.Adaptive.ProbeTimeout = -1
			case "speed":
				bad.Adaptive.MinBytesPerSecond = -1
			case "switches":
				bad.Adaptive.MaxSwitches = -1
			}
			cfg := c.Clone()
			cfg.Registries["docker.io"] = bad
			if cfg.Validate() == nil {
				t.Fatal("negative adaptive setting accepted")
			}
		})
	}
}
