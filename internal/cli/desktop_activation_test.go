package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/runui/oci-mirror-proxy/internal/config"
	"github.com/runui/oci-mirror-proxy/internal/dockerintegration"
)

type activationAdapter struct {
	dockerintegration.Adapter
	calls int
}

func (*activationAdapter) RequiresRestart() bool { return true }
func (a *activationAdapter) Activate(_ context.Context, restart bool) error {
	if restart {
		a.calls++
	}
	return nil
}

func TestDesktopActivationWithoutFileChanges(t *testing.T) {
	for _, restart := range []bool{false, true} {
		a := &activationAdapter{}
		var out bytes.Buffer
		flags := dockerFlags{adapter: a, restart: restart}
		if err := activateIntegration(t.Context(), &out, false, &flags); err != nil {
			t.Fatal(err)
		}
		if (a.calls == 1) != restart {
			t.Fatalf("restart=%v: activation calls=%d", restart, a.calls)
		}
		if !restart && !bytes.Contains(out.Bytes(), []byte("pending")) {
			t.Fatalf("missing pending activation message: %s", &out)
		}
	}
}

func TestInitDesktopAddresses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	code, _, errOut := run(t, "init", "--config", path, "--listen", "0.0.0.0:24817", "--advertise", "host.docker.internal:24817")
	if code != 0 {
		t.Fatal(errOut)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "0.0.0.0:24817" || cfg.ProxyURL() != "http://host.docker.internal:24817" {
		t.Fatalf("unexpected Desktop config: %+v", cfg)
	}
}
