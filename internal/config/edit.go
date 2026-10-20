package config

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/runui/oci-mirror-proxy/internal/fsutil"
)

// LocalOverrides is loaded last regardless of lexical ordering. Keeping user
// choices outside generated fragments makes disable survive any source update.
const LocalOverrides = "local-overrides.yaml"

func (b *Bundle) document(path string) (*Document, error) {
	for _, d := range b.Documents {
		if d.Path == path {
			return d, nil
		}
	}
	d, err := ParseDocument(path, []byte("registries: {}\n"))
	if err != nil {
		return nil, err
	}
	d.Data = nil
	b.Documents = append(b.Documents, d)
	return d, nil
}

func upstreamSequence(d *Document, alias string) *yaml.Node {
	reg := EnsureMapping(EnsureMapping(d.Root.Content[0], "registries"), alias)
	seq := MappingValue(reg, "upstreams")
	if seq == nil || seq.Kind != yaml.SequenceNode {
		seq = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		SetMapping(reg, "upstreams", seq)
	}
	return seq
}

// EditUpstream accepts a URL or a legacy label; no generated identifiers are
// introduced. Disable is a local override, while remove deletes all definitions
// of the URL so an earlier source cannot unexpectedly become visible again.
func (b *Bundle) EditUpstream(action, alias, selector string, spec Upstream, disabled bool) error {
	reg, ok := b.Effective.Registries[alias]
	if !ok {
		return fmt.Errorf("registry %q not found", alias)
	}
	key, _ := NormalizeURL(selector)
	var found *Upstream
	for _, u := range reg.Upstreams {
		if u.URL == key || (selector != "" && u.ID == selector) {
			copy := u
			found = &copy
			break
		}
	}
	switch action {
	case "add":
		key, err := NormalizeURL(spec.URL)
		if err != nil {
			return err
		}
		spec.URL = key
		for _, u := range reg.Upstreams {
			if u.URL == key || (spec.ID != "" && spec.ID == u.ID) {
				return fmt.Errorf("upstream already exists: %s", key)
			}
		}
		d, err := b.document(b.Main)
		if err != nil {
			return err
		}
		var node yaml.Node
		if err := node.Encode(spec); err != nil {
			return err
		}
		seq := upstreamSequence(d, alias)
		seq.Content = append(seq.Content, &node)
	case "disable":
		if found == nil {
			return fmt.Errorf("upstream %q not found", selector)
		}
		d, err := b.document(filepath.Join(DropInDir(b.Main), LocalOverrides))
		if err != nil {
			return err
		}
		seq := upstreamSequence(d, alias)
		var item *yaml.Node
		for _, n := range seq.Content {
			v := MappingValue(n, "url")
			if v != nil && v.Value == found.URL {
				item = n
				break
			}
		}
		if item == nil {
			item = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			SetMapping(item, "url", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: found.URL})
			seq.Content = append(seq.Content, item)
		}
		var value yaml.Node
		_ = value.Encode(disabled)
		SetMapping(item, "disabled", &value)
	case "remove":
		if found == nil {
			return fmt.Errorf("upstream %q not found", selector)
		}
		for _, d := range b.Documents {
			reg := MappingValue(MappingValue(d.Root.Content[0], "registries"), alias)
			seq := MappingValue(reg, "upstreams")
			if seq == nil {
				continue
			}
			var kept []*yaml.Node
			for _, item := range seq.Content {
				v := MappingValue(item, "url")
				canonical := ""
				if v != nil {
					canonical, _ = NormalizeURL(v.Value)
				}
				if canonical != found.URL {
					kept = append(kept, item)
				}
			}
			seq.Content = kept
		}
	default:
		return fmt.Errorf("unknown edit %s", action)
	}
	b.sort()
	return b.Merge()
}

// Persist edits only syntax trees whose semantic content changed. Untouched
// files remain byte-for-byte identical, including hand-written formatting.
func (b *Bundle) Persist() error {
	if err := b.CheckUnchanged(); err != nil {
		return err
	}
	type change struct {
		path          string
		before, after []byte
	}
	var changes []change
	for _, d := range b.Documents {
		after, err := d.Encode()
		if err != nil {
			return err
		}
		if d.Data != nil {
			old, err := ParseDocument(d.Path, d.Data)
			if err != nil {
				return err
			}
			encoded, _ := old.Encode()
			if bytes.Equal(encoded, after) {
				continue
			}
		}
		changes = append(changes, change{d.Path, d.Data, after})
	}
	for i, c := range changes {
		if err := Commit(c.path, c.before, c.after); err != nil {
			// Best-effort rollback is conditional on the bytes we just wrote;
			// it must never discard an intervening editor's work.
			var failures []string
			last := i - 1
			// Rename can have succeeded before directory sync failed. Include
			// that file in rollback too, still checking its exact written bytes.
			if published, ok := errors.AsType[*fsutil.DurabilityError](err); ok && published.Path == c.path {
				last = i
			}
			for j := last; j >= 0; j-- {
				p := changes[j]
				if rerr := Commit(p.path, p.after, p.before); rerr != nil {
					failures = append(failures, rerr.Error())
				}
			}
			if len(failures) > 0 {
				return fmt.Errorf("%w; rollback: %s", err, strings.Join(failures, "; "))
			}
			return err
		}
	}
	return nil
}
