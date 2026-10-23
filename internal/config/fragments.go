package config

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const MaxFragmentSize = 1 << 20

// Fragment is an uninterpreted config.d document. An empty revision denotes an
// absent file; revisions are content hashes, not modification timestamps.
type Fragment struct {
	Name     string `json:"name"`
	Data     []byte `json:"data"`
	Revision string `json:"revision"`
}

var ErrFragmentConflict = errors.New("configuration fragment changed concurrently")
var ErrInvalidFragment = errors.New("invalid configuration fragment")

// FragmentStore exposes only the drop-in directory associated with Main. It
// knows nothing about subscriptions and never publishes runtime configuration.
type FragmentStore struct{ Main string }

func fragmentName(name string) bool {
	return name != "" && !strings.HasPrefix(name, ".") && len(name) <= 240 && !strings.ContainsAny(name, "/\\:\x00\r\n") && filepath.IsLocal(name) && (strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml"))
}

func (s FragmentStore) root(create bool) (*os.Root, error) {
	// Anchor operations to an opened parent and reject a symlinked config.d.
	// Root confines subsequent operations even if names change concurrently.
	parent, err := os.OpenRoot(filepath.Dir(s.Main))
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	name := filepath.Base(DropInDir(s.Main))
	if create {
		if err := parent.Mkdir(name, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	}
	info, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("config.d must be a directory, not a symlink")
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		root.Close()
		return nil, ErrFragmentConflict
	}
	return root, nil
}

func readFragment(root *os.Root, name string) (Fragment, error) {
	f := Fragment{Name: name}
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if !info.Mode().IsRegular() {
		return f, fmt.Errorf("fragment must be a regular file")
	}
	handle, err := root.Open(name)
	if err != nil {
		return f, err
	}
	defer handle.Close()
	opened, err := handle.Stat()
	if err != nil {
		return f, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return f, ErrFragmentConflict
	}
	f.Data, err = io.ReadAll(io.LimitReader(handle, MaxFragmentSize+1))
	if err != nil {
		return f, err
	}
	if len(f.Data) > MaxFragmentSize {
		return f, fmt.Errorf("fragment exceeds size limit")
	}
	f.Revision = fmt.Sprintf("%x", sha256.Sum256(f.Data))
	return f, nil
}

func (s FragmentStore) Read(name string) (Fragment, error) {
	if !fragmentName(name) {
		return Fragment{}, ErrInvalidFragment
	}
	root, err := s.root(false)
	if errors.Is(err, os.ErrNotExist) {
		return Fragment{Name: name}, nil
	}
	if err != nil {
		return Fragment{}, err
	}
	defer root.Close()
	return readFragment(root, name)
}

func (s FragmentStore) List() ([]Fragment, error) {
	out := []Fragment{}
	root, err := s.root(false)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(4097)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	if len(entries) > 4096 {
		return nil, fmt.Errorf("too many entries in config.d")
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	for _, entry := range entries {
		if !fragmentName(entry.Name()) {
			continue
		}
		if len(out) >= 1024 {
			return nil, fmt.Errorf("too many configuration fragments")
		}
		f, err := readFragment(root, entry.Name())
		if err != nil {
			return nil, err
		}
		// Listing returns metadata only; contents are fetched individually.
		f.Data = nil
		out = append(out, f)
	}
	return out, nil
}

// Write conditionally publishes raw bytes, or removes a file when data is nil.
// The shared config lock serializes API and existing cooperative config writers;
// reload is deliberately outside this operation to keep repair paths usable.
func (s FragmentStore) Write(ctx context.Context, name, revision string, data []byte) (Fragment, error) {
	if !fragmentName(name) || len(data) > MaxFragmentSize {
		return Fragment{}, ErrInvalidFragment
	}
	unlock, err := Lock(ctx, s.Main)
	if err != nil {
		return Fragment{}, err
	}
	defer unlock()
	root, err := s.root(true)
	if err != nil {
		return Fragment{}, err
	}
	defer root.Close()
	before, err := readFragment(root, name)
	if err != nil {
		return Fragment{}, err
	}
	if before.Revision != revision {
		return Fragment{}, ErrFragmentConflict
	}
	if bytes.Equal(before.Data, data) && (before.Revision != "" || data == nil) {
		return before, nil
	}
	// Preserve the previous raw document without making backups loadable or
	// exposing them through the fragment API. A failed backup blocks publication.
	if before.Revision != "" {
		backup := name + ".bak"
		if info, err := root.Lstat(backup); err == nil && !info.Mode().IsRegular() {
			return Fragment{}, fmt.Errorf("backup must be a regular file")
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return Fragment{}, err
		}
		if err := publishFragment(root, backup, before.Data, 0o600); err != nil {
			return Fragment{}, err
		}
	}
	if data == nil {
		current, err := readFragment(root, name)
		if err != nil {
			return Fragment{}, err
		}
		if current.Revision != revision {
			return Fragment{}, ErrFragmentConflict
		}
		if err := root.Remove(name); err != nil {
			return Fragment{}, err
		}
	} else {
		mode := os.FileMode(0o600)
		if before.Revision != "" {
			info, err := root.Lstat(name)
			if err != nil {
				return Fragment{}, err
			}
			mode = info.Mode().Perm()
		}
		current, err := readFragment(root, name)
		if err != nil {
			return Fragment{}, err
		}
		if current.Revision != revision {
			return Fragment{}, ErrFragmentConflict
		}
		if err := publishFragment(root, name, data, mode); err != nil {
			return Fragment{}, err
		}
	}
	// Publication is already visible if directory syncing fails; do not imply
	// rollback. Platform-specific syncing avoids unsupported Windows operations.
	if err := syncFragmentDir(root); err != nil {
		return Fragment{}, fmt.Errorf("fragment saved but directory sync failed: %w", err)
	}
	return readFragment(root, name)
}

func publishFragment(root *os.Root, name string, data []byte, mode os.FileMode) error {
	temp := ".fragment-" + rand.Text() + ".tmp"
	file, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer root.Remove(temp)
	_, writeErr := file.Write(data)
	modeErr := file.Chmod(mode)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, modeErr, syncErr, closeErr); err != nil {
		return err
	}
	return root.Rename(temp, name)
}
