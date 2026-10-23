package daemonconf

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestActualConfigLifecycle(t *testing.T) {
	for _, oldState := range []string{"", "broken JSON", `{"files":[{"path":"/unrelated"}]}`} {
		t.Run(oldState, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "daemon.json")
			if oldState != "" {
				if err := os.WriteFile(filepath.Join(root, "dockerd-state.json"), []byte(oldState), 0o000); err != nil {
					t.Fatal(err)
				}
			}
			original := []byte(`{"debug":true,"registry-mirrors":["https://other"]}`)
			if err := os.WriteFile(path, original, 0o640); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			a := Applier{BackupDir: filepath.Join(root, "backups")}
			r := LegacyRenderer{DaemonJSON: path}
			target := dockerHubTarget()
			plan, err := r.Render(target, os.ReadFile)
			if err != nil {
				t.Fatal(err)
			}
			result, err := a.Apply(plan)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Files) != 1 || len(result.Backups) != 1 {
				t.Fatalf("result: %+v", result)
			}
			backup, _ := os.ReadFile(result.Backups[0])
			if !bytes.Equal(backup, original) {
				t.Fatal("backup is not original bytes")
			}
			info, _ := os.Stat(path)
			if info.Mode().Perm() != before.Mode().Perm() {
				t.Fatal("mode changed")
			}
			plan, err = r.Render(target, os.ReadFile)
			if err != nil {
				t.Fatal(err)
			}
			result, err = a.Apply(plan)
			if err != nil || len(result.Files) != 0 || len(result.Backups) != 0 {
				t.Fatalf("not idempotent: %+v %v", result, err)
			}
			after, _ := os.Stat(path)
			if !after.ModTime().Equal(info.ModTime()) {
				t.Fatal("no-op rewrote file")
			}
			data, _ := os.ReadFile(path)
			data = bytes.Replace(data, []byte(`"debug": true`), []byte(`"debug": false`), 1)
			if err := os.WriteFile(path, data, 0o640); err != nil {
				t.Fatal(err)
			}
			target.Remove = true
			plan, err = r.Render(target, os.ReadFile)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.Apply(plan); err != nil {
				t.Fatal(err)
			}
			data, _ = os.ReadFile(path)
			if bytes.Contains(data, []byte(proxyURL)) || !bytes.Contains(data, []byte(`"debug": false`)) || !bytes.Contains(data, []byte("https://other")) {
				t.Fatalf("remove lost edits: %s", data)
			}
		})
	}
}

func TestSnapshotAndLockConflicts(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "daemon.json")
	a := Applier{BackupDir: filepath.Join(root, "backups")}
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := (LegacyRenderer{DaemonJSON: path}).Render(dockerHubTarget(), os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"debug":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Apply(plan); err == nil || !strings.Contains(err.Error(), "changed while planning") {
		t.Fatalf("lost conflict: %v", err)
	}
	lock := path + ".oci-mirror-proxy.lock"
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Apply(plan); err == nil || !strings.Contains(err.Error(), "lock") {
		t.Fatalf("lost lock: %v", err)
	}
}

func TestStrictJSON(t *testing.T) {
	for _, raw := range []string{"", "null", "[]", `{"registry-mirrors":null}`, `{"registry-mirrors":[null]}`, `{"registry-mirrors":1}`, `{"debug":true,"debug":false}`, `{"x":{"a":1,"a":2}}`, `{} {}`} {
		t.Run(raw, func(t *testing.T) {
			_, err := (LegacyRenderer{DaemonJSON: "daemon.json"}).Render(dockerHubTarget(), func(string) ([]byte, error) { return []byte(raw), nil })
			if err == nil {
				t.Fatal("invalid JSON accepted")
			}
		})
	}
}

func TestHostsPreserveAndRemove(t *testing.T) {
	before := []byte("# user configuration\nserver = \"https://custom\"\n[host.\"https://other\"]\n  capabilities = [\"pull\"]\n  skip_verify = true\n[host.\"https://other\".header]\n  X-Test = [\"value\"]\n")
	merged, err := mergeHosts(before, proxyURL, "https://origin", false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(merged, []byte("https://custom")) || !bytes.Contains(merged, []byte("X-Test")) {
		t.Fatalf("lost options: %s", merged)
	}
	again, err := mergeHosts(merged, proxyURL, "https://origin", false)
	if err != nil || !bytes.Equal(merged, again) {
		t.Fatalf("not idempotent: %s %v", again, err)
	}
	removed, err := mergeHosts(merged, proxyURL, "", true)
	if err != nil || !bytes.Equal(before, removed) {
		t.Fatalf("unrelated bytes changed: %s %v", removed, err)
	}
}

func TestHostsExistingTargetOptions(t *testing.T) {
	before := []byte("[host.\"https://other\"]\ncapabilities=[\"pull\"]\n[host.\"" + proxyURL + "\"]\ncapabilities=[\"pull\",\"resolve\"]\nca=\"/custom/ca\"\n[host.\"" + proxyURL + "\".header]\nAuthorization=[\"secret\"]\n")
	merged, err := mergeHosts(before, proxyURL, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(merged, []byte("[host.\""+proxyURL)) || !bytes.Contains(merged, []byte("secret")) || !bytes.Contains(merged, []byte("/custom/ca")) {
		t.Fatalf("lost target options: %s", merged)
	}
}

func TestRemovalProducesValidEmptyMirrorList(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "daemon.json")
	if err := os.WriteFile(path, []byte(`{"registry-mirrors":["`+proxyURL+`/"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	target := dockerHubTarget()
	target.Remove = true
	plan, err := (LegacyRenderer{DaemonJSON: path}).Render(target, os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(plan.Files[0].Content, []byte(`"registry-mirrors": []`)) {
		t.Fatalf("invalid empty list: %s", plan.Files[0].Content)
	}
}

func TestBackupFailureLeavesAllTargetsUntouched(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "daemon.json")
	original := []byte(`{"debug":true}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(backupDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := (LegacyRenderer{DaemonJSON: path}).Render(dockerHubTarget(), os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (Applier{BackupDir: backupDir}).Apply(plan); err == nil {
		t.Fatal("backup failure ignored")
	}
	data, _ := os.ReadFile(path)
	if !bytes.Equal(data, original) {
		t.Fatal("target changed before backup")
	}
}

func TestContainerdRemovalDiscoversDroppedRoutes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "ghcr.io", "hosts.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, hostsTOML(proxyURL, Route{Alias: "ghcr.io", Origin: "https://ghcr.io"}), 0o600); err != nil {
		t.Fatal(err)
	}
	target := dockerHubTarget()
	target.Remove = true
	plan, err := (ContainerdRenderer{CertsDir: root}).Render(target, os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (Applier{BackupDir: filepath.Join(t.TempDir(), "backups")}).Apply(plan); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if bytes.Contains(data, []byte(proxyURL)) || !bytes.Contains(data, []byte("https://ghcr.io")) {
		t.Fatalf("dropped route not removed safely: %s", data)
	}
}
