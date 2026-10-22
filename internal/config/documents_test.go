package config

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeDocument(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDropInsMergeByURLAndExplicitFields(t *testing.T) {
	main := filepath.Join(t.TempDir(), "proxy.yml")
	writeDocument(t, main, `version: 1
registries:
  docker.io:
    race: {enabled: true, delay: 700ms}
    upstreams:
      - url: https://MIRROR.example:443/
        timeouts: {first_byte: 2s}
`)
	writeDocument(t, filepath.Join(DropInDir(main), "a.yaml"), `registries:
  docker.io:
    race: {enabled: false}
    upstreams:
      - url: https://mirror.example
        disabled: true
      - url: https://second.example/hub
`)
	writeDocument(t, filepath.Join(DropInDir(main), "z.yml"), `registries:
  docker.io:
    upstreams:
      - url: https://mirror.example
        disabled: false
`)
	writeDocument(t, filepath.Join(DropInDir(main), "ignored.yaml.bak"), "invalid: [")
	writeDocument(t, filepath.Join(DropInDir(main), ".temp.yaml"), "invalid: [")
	c, err := Load(main)
	if err != nil {
		t.Fatal(err)
	}
	r := c.Registries["docker.io"]
	if r.Race.Enabled || r.Race.Delay != 700*time.Millisecond {
		t.Fatalf("race=%+v", r.Race)
	}
	if len(r.Upstreams) != 2 || r.Upstreams[0].URL != "https://mirror.example" || r.Upstreams[0].Disabled || r.Upstreams[0].Timeouts.FirstByte != 2*time.Second || r.Upstreams[0].ID != "" {
		t.Fatalf("upstreams=%+v", r.Upstreams)
	}
}

func TestFragmentsRejectInvalidInputBeforeOverride(t *testing.T) {
	for _, body := range []string{"version: 9\n", "lisen: typo\n", "listen: a\nlisten: b\n", "registries: {}\n---\nregistries: {}\n", "listen: &alias foo\nadvertise: *alias\n"} {
		t.Run(body, func(t *testing.T) {
			main := filepath.Join(t.TempDir(), "config.yaml")
			writeDocument(t, main, body)
			writeDocument(t, filepath.Join(DropInDir(main), "z.yaml"), "version: 1\nlisten: 127.0.0.1:24817\n")
			if _, err := Load(main); err == nil {
				t.Fatal("accepted invalid fragment")
			}
		})
	}
}

func TestLocalDisableSurvivesUpdateWithoutResurrectingSource(t *testing.T) {
	main := filepath.Join(t.TempDir(), "config.yaml")
	writeDocument(t, main, "registries: {}\n")
	source := filepath.Join(DropInDir(main), "zz-source.yaml")
	writeDocument(t, source, "# original source comment\nregistries:\n  docker.io:\n    upstreams:\n      - url: https://mirror.example\n")
	before, _ := os.ReadFile(source)
	b, err := LoadBundle(main)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.EditUpstream("disable", "docker.io", "https://mirror.example", Upstream{}, true); err != nil {
		t.Fatal(err)
	}
	if err := b.Persist(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(source)
	if !bytes.Equal(before, after) {
		t.Fatal("disable rewrote remote source")
	}
	c, err := Load(main)
	if err != nil || !c.Registries["docker.io"].Upstreams[0].Disabled {
		t.Fatalf("disabled: %+v, %v", c, err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	c, err = Load(main)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Registries["docker.io"].Upstreams) != 0 {
		t.Fatal("orphan override resurrected removed upstream")
	}
}

func TestCommitRejectsConcurrentEditAndSymlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeDocument(t, path, "old\n")
	if err := Commit(path, []byte("stale\n"), []byte("new\n")); err == nil {
		t.Fatal("lost editor changes")
	}
	link := path + ".link"
	if err := os.Symlink(path, link); err != nil {
		if errors.Is(err, os.ErrPermission) {
			t.Skipf("symlink creation requires Developer Mode or elevated privileges: %v", err)
		}
		t.Fatal(err)
	}
	if err := Commit(link, []byte("old\n"), []byte("new\n")); err == nil {
		t.Fatal("followed symlink")
	}
}

func TestCommitDoesNotRecreateConcurrentlyDeletedEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Commit(path, []byte{}, []byte("new\n")); err == nil {
		t.Fatal("did not distinguish an empty previous file from a missing file")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("recreated deleted file: %v", err)
	}
}

func TestPersistRollsBackEarlierEditsOnLaterFileFailure(t *testing.T) {
	main := filepath.Join(t.TempDir(), "config.yaml")
	body := "registries:\n  docker.io:\n    upstreams:\n      - url: https://mirror.example\n"
	writeDocument(t, main, body)
	fragment := filepath.Join(DropInDir(main), "source.yaml")
	writeDocument(t, fragment, body)
	b, err := LoadBundle(main)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.EditUpstream("remove", "docker.io", "https://mirror.example", Upstream{}, false); err != nil {
		t.Fatal(err)
	}
	// An editor changes the inode without changing its bytes. The initial
	// collection check passes, but Commit must refuse the now-symlinked file.
	backing := filepath.Join(t.TempDir(), "backing.yaml")
	writeDocument(t, backing, body)
	if err := os.Remove(fragment); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(backing, fragment); err != nil {
		if errors.Is(err, os.ErrPermission) {
			t.Skipf("symlink creation requires Developer Mode or elevated privileges: %v", err)
		}
		t.Fatal(err)
	}
	if err := b.Persist(); err == nil {
		t.Fatal("persist followed symlink")
	}
	for _, path := range []string{main, backing} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != body {
			t.Fatalf("rollback lost original bytes in %s: %q %v", path, data, err)
		}
	}
}

func TestLockCancellationAndReadOnlyLoad(t *testing.T) {
	main := filepath.Join(t.TempDir(), "config.yaml")
	writeDocument(t, main, "registries: {}\n")
	if _, err := Load(main); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(main + ".lock"); !os.IsNotExist(err) {
		t.Fatal("read created a lock file")
	}
	unlock, err := Lock(t.Context(), main)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	if _, err := ReadLock(ctx, main); err == nil {
		t.Fatal("read did not wait for writer")
	}
}

func FuzzDocumentMerge(f *testing.F) {
	f.Add("registries:\n  docker.io:\n    upstreams:\n      - url: https://mirror.example\n")
	f.Add("listen: 127.0.0.1:24817\n")
	f.Fuzz(func(t *testing.T, body string) {
		if len(body) > 65536 {
			return
		}
		d, err := ParseDocument("fuzz.yaml", []byte(body))
		if err != nil {
			return
		}
		b := &Bundle{Main: d.Path, Documents: []*Document{d}}
		_ = b.Merge()
	})
}

func TestNormalizeURLPreservesMeaningfulParts(t *testing.T) {
	got, err := NormalizeURL("https://EXAMPLE.com:443/a%2Fb?b=2&a=1")
	if err != nil || got != "https://example.com/a%2Fb?b=2&a=1" {
		t.Fatalf("%q %v", got, err)
	}
	for _, u := range []string{"file:///etc/passwd", "https://user:pw@example.com", "https://example.com/#fragment"} {
		if _, err := NormalizeURL(u); err == nil {
			t.Fatal(u)
		}
	}
	got, err = NormalizeURL("HTTPS://EXAMPLE.com/%2F")
	if err != nil || got != "https://example.com/%2F" {
		t.Fatalf("escaped path changed: %q %v", got, err)
	}
}
