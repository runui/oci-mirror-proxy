package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/runui/oci-mirror-proxy/internal/fsutil"
)

// Document keeps the original syntax tree: editing an effective Config would
// flatten drop-ins into the main file and lose both ownership and comments.
type Document struct {
	Path string
	Data []byte
	Root *yaml.Node
}

type Bundle struct {
	Main      string
	Documents []*Document
	Effective Config
}

func DropInDir(path string) string {
	return strings.TrimSuffix(path, filepath.Ext(path)) + ".d"
}

// NormalizeURL deliberately does not clean paths or reorder queries: signed
// subscriptions and registry path prefixes may attach meaning to those bytes.
func NormalizeURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("URL must be an absolute http(s) URL without credentials or fragment")
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", errors.New("URL port must be between 1 and 65535")
		}
	}
	if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	u.Host = host
	if u.Path == "/" && (u.RawPath == "" || u.RawPath == "/") {
		u.Path, u.RawPath = "", ""
	}
	return u.String(), nil
}

// ConfigPaths returns only direct, visible YAML files, in deterministic order.
func ConfigPaths(main string) ([]string, error) {
	paths := []string{main}
	entries, err := os.ReadDir(DropInDir(main))
	if errors.Is(err, os.ErrNotExist) {
		return paths, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		if !strings.HasPrefix(e.Name(), ".") && !e.IsDir() && (ext == ".yaml" || ext == ".yml") {
			paths = append(paths, filepath.Join(DropInDir(main), e.Name()))
		}
	}
	sort.Strings(paths[1:])
	for i, p := range paths {
		if i > 0 && filepath.Base(p) == LocalOverrides {
			paths = append(append(paths[:i:i], paths[i+1:]...), p)
			break
		}
	}
	return paths, nil
}

func ParseDocument(path string, data []byte) (*Document, error) {
	var root yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("%s: expected exactly one YAML document", path)
	}
	if len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: configuration must be a mapping", path)
	}
	if err := checkNode(root.Content[0]); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	// Validate fragments before merging so an overridden typo or unsupported
	// version cannot disappear. Defaults are NOT applied to individual fragments.
	var c Config
	strict := yaml.NewDecoder(bytes.NewReader(data))
	strict.KnownFields(true)
	if err := strict.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if v := MappingValue(root.Content[0], "version"); v != nil && c.Version != SchemaVersion {
		return nil, fmt.Errorf("%s: unsupported config version %d", path, c.Version)
	}
	return &Document{Path: path, Data: bytes.Clone(data), Root: &root}, nil
}

func checkNode(n *yaml.Node) error {
	// Alias expansion makes ownership ambiguous when editing a single node.
	if n.Kind == yaml.AliasNode {
		return fmt.Errorf("line %d: YAML aliases are not supported", n.Line)
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || k.Value == "<<" || seen[k.Value] {
				return fmt.Errorf("line %d: duplicate or unsupported key %q", k.Line, k.Value)
			}
			seen[k.Value] = true
		}
	}
	for _, child := range n.Content {
		if err := checkNode(child); err != nil {
			return err
		}
	}
	return nil
}

func LoadBundle(main string) (*Bundle, error) {
	// Reread the file set and bytes to detect ordinary editor races. Callers
	// needing a cooperative multi-file snapshot also hold ReadLock/Lock;
	// config.Load additionally handles first-time sidecar creation.
	for attempt := 0; attempt < 3; attempt++ {
		b, err := loadBundle(main)
		if err != nil {
			return nil, err
		}
		if err := b.CheckUnchanged(); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("configuration kept changing while loading; retry")
}

func loadBundle(main string) (*Bundle, error) {
	paths, err := ConfigPaths(main)
	if err != nil {
		return nil, err
	}
	b := &Bundle{Main: main}
	for _, path := range paths {
		data, err := fsutil.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
		d, err := ParseDocument(path, data)
		if err != nil {
			return nil, err
		}
		b.Documents = append(b.Documents, d)
	}
	if err := b.Merge(); err != nil {
		return nil, err
	}
	return b, nil
}

func cloneNode(n *yaml.Node) *yaml.Node {
	c := *n
	c.Content = make([]*yaml.Node, len(n.Content))
	for i, child := range n.Content {
		c.Content[i] = cloneNode(child)
	}
	return &c
}

func MappingValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func SetMapping(n *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			n.Content[i+1] = value
			return
		}
	}
	n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

func EnsureMapping(n *yaml.Node, key string) *yaml.Node {
	v := MappingValue(n, key)
	if v == nil || v.Kind != yaml.MappingNode {
		v = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		SetMapping(n, key, v)
	}
	return v
}

func mergeNode(dst, src *yaml.Node, field string) error {
	if dst.Kind == yaml.MappingNode && src.Kind == yaml.MappingNode {
		for i := 0; i < len(src.Content); i += 2 {
			key, value := src.Content[i].Value, src.Content[i+1]
			old := MappingValue(dst, key)
			if old == nil {
				// Recurse into empty mappings too, so nested upstream sequences
				// are normalized even in the very first configuration document.
				old = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				SetMapping(dst, key, old)
			}
			if err := mergeNode(old, value, key); err != nil {
				return err
			}
		}
		return nil
	}
	if field == "upstreams" && src.Kind == yaml.SequenceNode {
		if dst.Kind != yaml.SequenceNode {
			*dst = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		}
		for _, item := range src.Content {
			v := MappingValue(item, "url")
			if v == nil {
				return fmt.Errorf("line %d: upstream requires url", item.Line)
			}
			key, err := NormalizeURL(v.Value)
			if err != nil {
				return fmt.Errorf("line %d: %w", v.Line, err)
			}
			var existing *yaml.Node
			for _, candidate := range dst.Content {
				if MappingValue(candidate, "url").Value == key {
					existing = candidate
					break
				}
			}
			copy := cloneNode(item)
			MappingValue(copy, "url").Value = key
			// First occurrence owns ordering; later explicit fields override only
			// that URL's options. No generated ID participates in identity.
			if existing == nil {
				dst.Content = append(dst.Content, copy)
			} else if err := mergeNode(existing, copy, ""); err != nil {
				return err
			}
		}
		return nil
	}
	*dst = *cloneNode(src)
	return nil
}

func (b *Bundle) Merge() error {
	merged := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, d := range b.Documents {
		src := d.Root.Content[0]
		if d.Path != b.Main && filepath.Base(d.Path) == LocalOverrides {
			src = activeOverrides(src, merged)
		}
		if err := mergeNode(merged, src, ""); err != nil {
			return fmt.Errorf("%s: %w", d.Path, err)
		}
	}
	data, err := yaml.Marshal(merged)
	if err != nil {
		return err
	}
	// Default only after merging: otherwise omitted fields in a fragment would
	// overwrite explicit earlier values (especially enabled: false).
	c, err := Decode(data)
	if err != nil {
		return fmt.Errorf("merged configuration: %w", err)
	}
	b.Effective = c
	return nil
}

// Overrides patch existing URLs, never contribute new sources. Otherwise a
// disabled patch would resurrect an upstream after its last source is removed.
func activeOverrides(src, merged *yaml.Node) *yaml.Node {
	src = cloneNode(src)
	regs := MappingValue(src, "registries")
	if regs == nil {
		return src
	}
	base := MappingValue(merged, "registries")
	var keptRegs []*yaml.Node
	for i := 0; i < len(regs.Content); i += 2 {
		reg := MappingValue(base, regs.Content[i].Value)
		if reg == nil {
			continue
		}
		seq := MappingValue(regs.Content[i+1], "upstreams")
		if seq != nil {
			existing := MappingValue(reg, "upstreams")
			keys := map[string]bool{}
			if existing != nil {
				for _, item := range existing.Content {
					keys[MappingValue(item, "url").Value] = true
				}
			}
			var kept []*yaml.Node
			for _, item := range seq.Content {
				v := MappingValue(item, "url")
				if v == nil {
					continue
				}
				key, _ := NormalizeURL(v.Value)
				if keys[key] {
					kept = append(kept, item)
				}
			}
			seq.Content = kept
		}
		keptRegs = append(keptRegs, regs.Content[i:i+2]...)
	}
	regs.Content = keptRegs
	return src
}

// CheckUnchanged detects file additions/removals as well as editor changes.
// It complements cooperative file locks; it is not a lock on external editors.
func (b *Bundle) CheckUnchanged() error {
	paths, err := ConfigPaths(b.Main)
	if err != nil {
		return err
	}
	expected := map[string][]byte{}
	for _, d := range b.Documents {
		if d.Data != nil {
			expected[d.Path] = d.Data
		}
	}
	if len(paths) != len(expected) {
		return errors.New("configuration file set changed concurrently")
	}
	for _, p := range paths {
		before, ok := expected[p]
		if !ok {
			return fmt.Errorf("configuration added concurrently: %s", p)
		}
		data, err := fsutil.ReadFile(p)
		if err != nil {
			return err
		}
		if !bytes.Equal(data, before) {
			return fmt.Errorf("configuration changed concurrently: %s", p)
		}
	}
	return nil
}

func (b *Bundle) sort() {
	sort.SliceStable(b.Documents, func(i, j int) bool {
		a, c := b.Documents[i].Path, b.Documents[j].Path
		if a == c {
			return false
		}
		if a == b.Main {
			return true
		}
		if c == b.Main {
			return false
		}
		if filepath.Base(a) == LocalOverrides {
			return false
		}
		if filepath.Base(c) == LocalOverrides {
			return true
		}
		return a < c
	})
}

func (d *Document) Encode() ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(d.Root); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Replace validates a candidate with all other files, without exposing an
// unvalidated temporary .yaml file to a concurrent reload.
func (b *Bundle) Replace(path string, data []byte) error {
	var docs []*Document
	for _, d := range b.Documents {
		if d.Path != path {
			docs = append(docs, d)
		}
	}
	if data != nil {
		d, err := ParseDocument(path, data)
		if err != nil {
			return err
		}
		docs = append(docs, d)
	}
	b.Documents = docs
	b.sort()
	return b.Merge()
}

// Commit checks the originally read bytes immediately before atomic replace.
// Cooperative writers additionally hold Lock; editors need not honor that lock.
func Commit(path string, before, after []byte) error {
	info, err := os.Lstat(path)
	if err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("refusing to modify non-regular file %s", path)
	}
	current, err := fsutil.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !bytes.Equal(current, before) || (before == nil && err == nil) || (before != nil && errors.Is(err, os.ErrNotExist)) {
		return fmt.Errorf("configuration changed concurrently: %s", path)
	}
	if bytes.Equal(before, after) {
		return nil
	}
	if after == nil {
		return fsutil.RemoveFile(path)
	}
	return fsutil.WriteFileAtomic(path, after, 0o600)
}
