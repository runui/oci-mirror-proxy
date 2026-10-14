package daemonconf

import (
	"fmt"
	"path/filepath"
	"strings"
)

const managedHeader = "# Managed by oci-mirror-proxy. Changes are overwritten by `oci-mirror-proxy dockerd apply`.\n"

// ContainerdRenderer writes one hosts.toml per registry under the certs
// directory. containerd reads these files on every resolution, so dockerd does
// not need a reload.
type ContainerdRenderer struct {
	CertsDir string
}

// Store returns StoreContainerd.
func (ContainerdRenderer) Store() StoreKind { return StoreContainerd }

// Render produces one hosts.toml per route.
func (r ContainerdRenderer) Render(t Target, _ ReadFunc) (Plan, error) {
	if len(t.Routes) == 0 {
		return Plan{}, ErrNoRoutes
	}
	plan := Plan{Store: StoreContainerd}
	for _, rt := range t.Routes {
		plan.Files = append(plan.Files, File{
			Path:    filepath.Join(r.CertsDir, rt.Alias, "hosts.toml"),
			Content: hostsTOML(t.ProxyURL, rt),
			Mode:    0o644,
		})
	}
	return plan, nil
}

// hostsTOML renders the host configuration for one registry. containerd tries
// the [host] tables in file order and always tries the top-level server last.
// The proxy is therefore tried first, and the official origin remains the fallback.
func hostsTOML(proxyURL string, rt Route) []byte {
	var b strings.Builder
	b.WriteString(managedHeader)
	fmt.Fprintf(&b, "server = %s\n\n", tomlString(rt.Origin))
	fmt.Fprintf(&b, "[host.%s]\n", tomlString(proxyURL+proxyPathPrefix(rt.Alias)))
	b.WriteString(`  capabilities = ["pull", "resolve", "referrers"]` + "\n")
	return []byte(b.String())
}

// tomlString quotes s as a TOML basic string, escaping what the grammar requires.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
