package daemonconf

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// ContainerdRenderer writes one hosts.toml per registry under the certs
// directory. containerd reads these files on every resolution, so dockerd does
// not need a reload.
type ContainerdRenderer struct {
	CertsDir string
}

// Store returns StoreContainerd.
func (ContainerdRenderer) Store() StoreKind { return StoreContainerd }

// Render produces one hosts.toml per route.
func (r ContainerdRenderer) Render(t Target, read ReadFunc) (Plan, error) {
	if len(t.Routes) == 0 && !t.Remove {
		return Plan{}, ErrNoRoutes
	}
	plan := Plan{Store: StoreContainerd}
	if read == nil {
		read = os.ReadFile
	}
	if t.Remove {
		// Discover actual registry files, including routes removed from subscriptions.
		entries, err := os.ReadDir(r.CertsDir)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return plan, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				found := false
				for _, rt := range t.Routes {
					if rt.Alias == entry.Name() {
						found = true
					}
				}
				if !found {
					t.Routes = append(t.Routes, Route{Alias: entry.Name()})
				}
			}
		}
	}
	for _, rt := range t.Routes {
		path := filepath.Join(r.CertsDir, rt.Alias, "hosts.toml")
		before, err := read(path)
		exists := err == nil
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return plan, err
		}
		if !exists && t.Remove {
			continue
		}
		content, err := mergeHosts(before, t.ProxyURL+proxyPathPrefix(rt.Alias), rt.Origin, t.Remove)
		if err != nil {
			return plan, fmt.Errorf("%s: %w", path, err)
		}
		plan.Files = append(plan.Files, File{
			Path:        path,
			Content:     content,
			Mode:        0o644,
			CheckBefore: new(string(before)), BeforeExists: exists,
		})
	}
	return plan, nil
}

// hostsTOML renders a new registry configuration with the origin as fallback.
func hostsTOML(proxyURL string, rt Route) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "server = %s\n\n", tomlString(rt.Origin))
	fmt.Fprintf(&b, "[host.%s]\n", tomlString(proxyURL+proxyPathPrefix(rt.Alias)))
	b.WriteString(`  capabilities = ["pull", "resolve", "referrers"]` + "\n")
	return []byte(b.String())
}

// mergeHosts edits complete table spans using the TOML parser, not regexes.
// Unrelated bytes (including comments and unknown options) remain untouched.
// Existing target options are preserved rather than reset to generated defaults.
func mergeHosts(before []byte, endpoint, origin string, remove bool) ([]byte, error) {
	var cfg map[string]any
	if err := toml.Unmarshal(before, &cfg); err != nil {
		return nil, err
	}
	if server, ok := cfg["server"]; ok {
		if _, ok := server.(string); !ok {
			return nil, fmt.Errorf("server must be a string")
		}
	}
	if host, ok := cfg["host"]; ok {
		if _, ok := host.(map[string]any); !ok {
			return nil, fmt.Errorf("host must be a table")
		}
	}
	type section struct {
		start, end int
		key        []string
	}
	var sections []section
	var parser unstable.Parser
	parser.Reset(before)
	for parser.NextExpression() {
		n := parser.Expression()
		if n.Kind != unstable.Table && n.Kind != unstable.ArrayTable {
			continue
		}
		keys := n.Key()
		var key []string
		offset := -1
		for keys.Next() {
			k := keys.Node()
			key = append(key, string(k.Data))
			if offset < 0 {
				offset = int(k.Raw.Offset)
			}
		}
		if offset < 0 {
			return nil, fmt.Errorf("unsupported table syntax")
		}
		start := bytes.LastIndexByte(before[:offset], '\n') + 1
		if len(sections) > 0 {
			sections[len(sections)-1].end = start
		}
		sections = append(sections, section{start, len(before), key})
	}
	if err := parser.Error(); err != nil {
		return nil, err
	}
	firstHost := len(before)
	var target []section
	for _, s := range sections {
		if len(s.key) > 0 && s.key[0] == "host" {
			firstHost = min(firstHost, s.start)
			if len(s.key) >= 2 && sameMirror(s.key[1], endpoint) {
				target = append(target, s)
			}
		}
	}
	// Inline/dotted host declarations cannot be moved as complete table spans.
	// Refuse them rather than silently rebuilding or discarding user settings.
	if hosts, ok := cfg["host"].(map[string]any); ok {
		for name := range hosts {
			found := false
			for _, s := range sections {
				if len(s.key) >= 2 && s.key[0] == "host" && s.key[1] == name {
					found = true
				}
			}
			if !found {
				return nil, fmt.Errorf("host %q requires explicit TOML table syntax for safe merging", name)
			}
		}
	}
	if len(target) > 0 && !remove {
		// Equivalent duplicate host tables may carry different TLS/auth options.
		// Refuse ambiguous merges instead of guessing which options to discard.
		name := target[0].key[1]
		for _, s := range target {
			if s.key[1] != name {
				return nil, fmt.Errorf("duplicate equivalent proxy hosts; consolidate their options first")
			}
		}
		if target[0].start == firstHost {
			return before, nil
		}
	}
	if len(target) == 0 && remove {
		return before, nil
	}
	var block []byte
	if !remove {
		if len(target) == 0 {
			block = []byte(fmt.Sprintf("[host.%s]\n  capabilities = [\"pull\", \"resolve\", \"referrers\"]\n\n", tomlString(endpoint)))
		} else {
			for _, s := range target {
				block = append(block, before[s.start:s.end]...)
			}
		}
	}
	var out []byte
	if len(before) == 0 && !remove {
		out = append(out, []byte("server = "+tomlString(origin)+"\n\n")...)
	}
	pos := 0
	for _, s := range target {
		if pos <= firstHost && firstHost <= s.start {
			out = append(out, before[pos:firstHost]...)
			out = appendSeparator(out, block)
			pos = firstHost
			block = nil
		}
		out = append(out, before[pos:s.start]...)
		pos = s.end
	}
	if block != nil {
		out = append(out, before[pos:firstHost]...)
		out = appendSeparator(out, block)
		pos = firstHost
	}
	out = append(out, before[pos:]...)
	var check map[string]any
	if err := toml.Unmarshal(out, &check); err != nil {
		return nil, fmt.Errorf("merged TOML is invalid: %w", err)
	}
	return out, nil
}

func appendSeparator(dst, block []byte) []byte {
	if len(block) == 0 {
		return dst
	}
	if len(dst) > 0 && dst[len(dst)-1] != '\n' {
		dst = append(dst, '\n')
	}
	dst = append(dst, block...)
	if dst[len(dst)-1] != '\n' {
		dst = append(dst, '\n')
	}
	return dst
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
