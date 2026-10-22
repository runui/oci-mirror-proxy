package daemonconf

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/fsutil"
)

// ErrUnmanaged means a target file exists and was not created by this tool.
var ErrUnmanaged = errors.New("file exists and was not created by oci-mirror-proxy; rerun with --force to back it up and take it over")

// ErrNothingToRevert means no applied state was found.
var ErrNothingToRevert = errors.New("nothing to revert: no applied state found")

// State records what Apply changed, so Revert can undo exactly that and nothing else.
type State struct {
	Store       StoreKind   `json:"store"`
	AppliedAt   time.Time   `json:"applied_at"`
	NeedsReload bool        `json:"needs_reload"`
	Files       []FileState `json:"files"`
	// Dirs are directories Apply created, outermost first.
	Dirs []string `json:"dirs,omitempty"`
}

// FileState is the original condition of one file. It is captured the first time
// Apply touches the file, and kept until Revert.
type FileState struct {
	// WrittenHash detects edits made after apply. Older state files omit it and
	// retain their historical behavior so existing installations remain revertible.
	WrittenHash string      `json:"written_hash,omitempty"`
	Path        string      `json:"path"`
	Existed     bool        `json:"existed"`
	Backup      string      `json:"backup,omitempty"`
	Mode        fs.FileMode `json:"mode,omitempty"`
}

// Applier writes plans to disk and records what it changed.
type Applier struct {
	StatePath string
	BackupDir string
	// Now is the clock used for timestamps. It defaults to time.Now.
	Now func() time.Time
}

// Apply writes plan. The original content of each file is backed up the first
// time it is touched. A file that exists but is not yet managed is refused unless
// force is set, so user configuration is never overwritten silently.
func (a Applier) Apply(plan Plan, force bool) (State, error) {
	unlock, err := a.lock()
	if err != nil {
		return State{}, err
	}
	defer unlock()
	prev, err := a.loadState()
	if err != nil {
		return State{}, err
	}
	known := make(map[string]FileState, len(prev.Files))
	for _, f := range prev.Files {
		if err := unchanged(f); err != nil {
			return State{}, err
		}
		known[f.Path] = f
	}
	// Validate ownership for every target before writing the first file. Backups
	// alone cannot make a later ownership failure safe for multi-file plans.
	for _, f := range plan.Files {
		if f.CheckBefore != nil {
			data, err := os.ReadFile(f.Path)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return State{}, err
			}
			if string(data) != *f.CheckBefore {
				return State{}, fmt.Errorf("configuration %s changed while planning; retry the operation", f.Path)
			}
		}
		if _, managed := known[f.Path]; managed {
			continue
		}
		if _, err := os.Stat(f.Path); err == nil && !force {
			return State{}, fmt.Errorf("%w: %s", ErrUnmanaged, f.Path)
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return State{}, err
		}
	}

	now := a.now()
	next := State{
		Store:       plan.Store,
		AppliedAt:   now,
		NeedsReload: plan.NeedsReload,
		Dirs:        slices.Clone(prev.Dirs),
	}
	stamp := now.Format("20060102T150405.000000000")

	planned := make(map[string]bool, len(plan.Files))
	for i, f := range plan.Files {
		planned[f.Path] = true
		rec, managed := known[f.Path]
		if !managed {
			// Work out which directories this write creates, before writing creates them.
			next.Dirs = appendUnique(next.Dirs, missingDirs(f.Path)...)
			rec, err = a.capture(f.Path, stamp, i, force)
			if err != nil {
				return State{}, err
			}
		}
		rec.WrittenHash = fmt.Sprintf("%x", sha256.Sum256(f.Content))
		next.Files = append(next.Files, rec)
		if err := fsutil.WriteFileAtomic(f.Path, f.Content, fileMode(f.Mode)); err != nil {
			return State{}, fmt.Errorf("write %s: %w", f.Path, err)
		}
	}

	// Files that an earlier apply managed but this plan no longer needs are restored.
	for _, old := range prev.Files {
		if planned[old.Path] {
			continue
		}
		if err := restore(old); err != nil {
			return State{}, fmt.Errorf("restore %s: %w", old.Path, err)
		}
	}

	if err := a.saveState(next); err != nil {
		return State{}, err
	}
	return next, nil
}

// Revert undoes the last Apply. Files are restored from their backups, or removed
// when they did not exist before. Empty directories that Apply created are removed,
// and the state file is deleted. Backups are kept so the operator can inspect them.
func (a Applier) Revert() (State, error) {
	unlock, err := a.lock()
	if err != nil {
		return State{}, err
	}
	defer unlock()
	st, err := a.loadState()
	if err != nil {
		return State{}, err
	}
	if st.AppliedAt.IsZero() {
		return State{}, ErrNothingToRevert
	}
	// Preflight every file before restoring any, so a conflict does not cause a
	// partially reverted installation. --force never discards concurrent edits.
	for _, f := range st.Files {
		if err := unchanged(f); err != nil {
			return st, err
		}
	}
	var errs []error
	for _, f := range st.Files {
		if err := restore(f); err != nil {
			errs = append(errs, fmt.Errorf("restore %s: %w", f.Path, err))
		}
	}
	if len(errs) > 0 {
		// Keep the state file so a retry can finish the job.
		return st, errors.Join(errs...)
	}
	for i := len(st.Dirs) - 1; i >= 0; i-- {
		_ = os.Remove(st.Dirs[i]) // only succeeds when empty; that is the intent
	}
	if err := os.Remove(a.StatePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return st, fmt.Errorf("remove state file: %w", err)
	}
	return st, nil
}

func unchanged(f FileState) error {
	if f.WrittenHash == "" {
		return nil
	}
	data, err := os.ReadFile(f.Path)
	if err != nil {
		return fmt.Errorf("inspect managed file %s: %w", f.Path, err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != f.WrittenHash {
		return fmt.Errorf("managed file %s changed externally; refusing to overwrite it", f.Path)
	}
	return nil
}

func (a Applier) lock() (func(), error) {
	if err := os.MkdirAll(filepath.Dir(a.StatePath), 0o700); err != nil {
		return nil, err
	}
	path := a.StatePath + ".lock"
	// Exclusive creation works across local processes on both supported systems.
	// A crash leaves a visible lock rather than risking concurrent recovery.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("acquire integration lock %s (remove only after confirming no operation is running): %w", path, err)
	}
	f.Close()
	return func() { _ = os.Remove(path) }, nil
}

// capture records the original condition of path before it is replaced.
func (a Applier) capture(path, stamp string, index int, force bool) (FileState, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return FileState{Path: path, Existed: false}, nil
	}
	if err != nil {
		return FileState{}, fmt.Errorf("inspect %s: %w", path, err)
	}
	if !force {
		return FileState{}, fmt.Errorf("%w: %s", ErrUnmanaged, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return FileState{}, fmt.Errorf("read %s for backup: %w", path, err)
	}
	backup := filepath.Join(a.BackupDir, stamp, strconv.Itoa(index)+"-"+filepath.Base(path))
	if err := fsutil.WriteFileAtomic(backup, data, 0o600); err != nil {
		return FileState{}, fmt.Errorf("write backup: %w", err)
	}
	return FileState{Path: path, Existed: true, Backup: backup, Mode: info.Mode().Perm()}, nil
}

func restore(f FileState) error {
	if !f.Existed {
		if err := os.Remove(f.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := os.ReadFile(f.Backup)
	if err != nil {
		return fmt.Errorf("read backup %s: %w", f.Backup, err)
	}
	return fsutil.WriteFileAtomic(f.Path, data, fileMode(f.Mode))
}

func (a Applier) loadState() (State, error) {
	data, exists, err := fsutil.ReadOptional(a.StatePath)
	if err != nil {
		return State{}, err
	}
	if !exists {
		return State{}, nil
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, fmt.Errorf("parse state file %s: %w", a.StatePath, err)
	}
	return st, nil
}

func (a Applier) saveState(st State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	// The state contains backup paths, so keep it private.
	return fsutil.WriteFileAtomic(a.StatePath, data, 0o600)
}

func (a Applier) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// missingDirs lists the ancestors of path that do not exist yet, outermost first.
func missingDirs(path string) []string {
	var missing []string
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(dir); err == nil {
			break
		}
		missing = append([]string{dir}, missing...)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return missing
}

func appendUnique(list []string, items ...string) []string {
	for _, item := range items {
		if !slices.Contains(list, item) {
			list = append(list, item)
		}
	}
	return list
}

func fileMode(m fs.FileMode) fs.FileMode {
	if m == 0 {
		return 0o644
	}
	return m
}
