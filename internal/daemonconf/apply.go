package daemonconf

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/fsutil"
)

// Result describes only this invocation; it is never persisted or read as state.
type Result struct {
	Files       []File
	Backups     []string
	NeedsReload bool
}

// Applier publishes plans derived from actual configuration, without ownership records.
type Applier struct{ BackupDir string }

// Apply preflights and backs up all changed files before publishing any. Atomicity
// is per file, not across the plan. On error Result reports already-visible writes.
func (a Applier) Apply(plan Plan) (result Result, err error) {
	type pending struct {
		file   File
		before []byte
		exists bool
		mode   os.FileMode
	}
	var changes []pending
	var unlocks []func()
	defer func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
	}()
	for _, f := range plan.Files {
		if info, err := os.Lstat(f.Path); err == nil {
			if !info.Mode().IsRegular() {
				return result, fmt.Errorf("refusing non-regular configuration file %s", f.Path)
			}
		} else if !os.IsNotExist(err) {
			return result, err
		}
		// Lock the actual target, independent of backup location or installation history.
		if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
			return result, err
		}
		lock := f.Path + ".oci-mirror-proxy.lock"
		handle, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return result, fmt.Errorf("acquire configuration lock %s (remove only after confirming no operation is running): %w", lock, err)
		}
		handle.Close()
		unlocks = append(unlocks, func() { _ = os.Remove(lock) })
		before, exists, err := fsutil.ReadOptional(f.Path)
		if err != nil {
			return result, err
		}
		if f.CheckBefore == nil {
			return result, fmt.Errorf("missing configuration snapshot: %s", f.Path)
		}
		if exists != f.BeforeExists || string(before) != *f.CheckBefore {
			return result, fmt.Errorf("configuration %s changed while planning; retry the operation", f.Path)
		}
		if bytes.Equal(before, f.Content) && exists {
			continue
		}
		mode := f.Mode
		if exists {
			info, err := os.Lstat(f.Path)
			if err != nil {
				return result, err
			}
			if !info.Mode().IsRegular() {
				return result, fmt.Errorf("refusing non-regular configuration file %s", f.Path)
			}
			mode = info.Mode().Perm()
		}
		changes = append(changes, pending{f, before, exists, mode})
	}
	// Each invocation gets a unique backup directory; no manifest or state is needed.
	if len(changes) > 0 {
		backupDir := a.BackupDir
		if backupDir == "" {
			backupDir = filepath.Join(filepath.Dir(changes[0].file.Path), "oci-mirror-proxy-backups")
		}
		for i, c := range changes {
			if !c.exists {
				continue
			}
			if err := os.MkdirAll(backupDir, 0o700); err != nil {
				return result, err
			}
			backup, err := os.CreateTemp(backupDir, time.Now().UTC().Format("20060102T150405")+"-"+fmt.Sprint(i)+"-"+filepath.Base(c.file.Path)+"-*")
			if err != nil {
				return result, err
			}
			path := backup.Name()
			_, writeErr := backup.Write(c.before)
			syncErr := backup.Sync()
			closeErr := backup.Close()
			if writeErr != nil {
				return result, writeErr
			}
			if syncErr != nil {
				return result, syncErr
			}
			if closeErr != nil {
				return result, closeErr
			}
			// Publish through the shared durability helper as well, syncing the directory.
			if err := fsutil.WriteFileAtomic(path, c.before, 0o600); err != nil {
				return result, err
			}
			result.Backups = append(result.Backups, path)
		}
	}
	for _, c := range changes {
		// This catches intervening external edits, but an uncooperative writer can
		// still race the final rename. The lock serializes this tool's invocations.
		before, exists, err := fsutil.ReadOptional(c.file.Path)
		if err != nil {
			return result, err
		}
		if exists != c.exists || !bytes.Equal(before, c.before) {
			return result, fmt.Errorf("configuration %s changed before publication; retry", c.file.Path)
		}
		err = fsutil.WriteConfigAtomic(c.file.Path, c.file.Content, c.mode)
		_, published := errors.AsType[*fsutil.DurabilityError](err)
		if err == nil || published {
			result.Files = append(result.Files, c.file)
			result.NeedsReload = plan.NeedsReload
		}
		if err != nil {
			return result, fmt.Errorf("write %s (%d file(s) already changed): %w", c.file.Path, len(result.Files), err)
		}
	}
	return result, nil
}
