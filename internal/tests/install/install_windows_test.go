// Keep installer tests together in this package. Extend the existing platform
// suites instead of adding a test file for each command, feature or scenario.
package install

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Keep installer integration tests away from the host's persistent environment.
// The actual PATH composition and rollback functions run against file storage.
func isolateWindowsInstallerPaths(script string) string {
	for name, replacement := range map[string]string{
		"Get-PersistentPath": `function Get-PersistentPath([string]$Target) {
    $file = $PSCommandPath + '.' + $Target + '.path'
    if (Test-Path -LiteralPath $file) { return [IO.File]::ReadAllText($file) }
    return 'C:\existing-bin;%SystemRoot%\System32'
}`,
		"Set-PersistentPath": `function Set-PersistentPath([AllowNull()][string]$Value, [string]$Target) {
    if ($env:TEST_PATH_FAILURE -eq '1') { throw 'fixture PATH write failed' }
    [IO.File]::WriteAllText(($PSCommandPath + '.' + $Target + '.path'), $Value)
}`,
		"Send-EnvironmentChange": `function Send-EnvironmentChange {}`,
	} {
		script = regexp.MustCompile(`(?ms)^function `+name+`(?:\([^\n]*\))? \{.*?^}\r?\n\r?\n`).ReplaceAllStringFunc(script, func(string) string { return replacement + "\n\n" })
	}
	return script
}

func isolatedWindowsInstaller(t *testing.T, root string) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "install.ps1")
	if err := os.WriteFile(path, []byte(isolateWindowsInstallerPaths(string(source))), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWindowsInstaller(t *testing.T) {
	for _, shell := range []string{"powershell.exe", "pwsh.exe"} {
		binary, err := exec.LookPath(shell)
		if err != nil {
			continue
		}
		t.Run(shell, func(t *testing.T) {
			var archive bytes.Buffer
			zw := zip.NewWriter(&archive)
			member, err := zw.Create("oci-mirror-proxy.exe")
			if err != nil {
				t.Fatal(err)
			}
			payload := []byte("installer fixture\n")
			if _, err := member.Write(payload); err != nil {
				t.Fatal(err)
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			var bad atomic.Bool
			var missing atomic.Bool
			var sourceRequests atomic.Int32
			var pathFailure atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/archive/") {
					sourceRequests.Add(1)
					http.NotFound(w, r)
					return
				}
				if r.URL.Path == "/releases/latest" {
					http.Redirect(w, r, "/releases/tag/vtest", http.StatusFound)
					return
				}
				if r.URL.Path == "/releases/tag/vtest" {
					fmt.Fprint(w, "release")
					return
				}
				if strings.HasSuffix(r.URL.Path, "/SHA256SUMS") {
					sum := fmt.Sprintf("%x", sha256.Sum256(archive.Bytes()))
					if bad.Load() {
						sum = strings.Repeat("0", 64)
					}
					for _, arch := range []string{"amd64", "arm64"} {
						fmt.Fprintf(w, "%s *./oci-mirror-proxy_vtest_windows_%s.zip\n", sum, arch)
					}
					return
				}
				if strings.HasSuffix(r.URL.Path, ".zip") {
					if missing.Load() {
						http.NotFound(w, r)
						return
					}
					w.Write(archive.Bytes())
					return
				}
				http.NotFound(w, r)
			}))
			defer server.Close()
			root := windowsInstallerTempDir(t)
			prefix := filepath.Join(root, "prefix with spaces")
			tmp := filepath.Join(root, "tmp")
			if err := os.Mkdir(tmp, 0o700); err != nil {
				t.Fatal(err)
			}
			script := isolatedWindowsInstaller(t, root)
			install := func(update bool) error {
				t.Helper()
				args := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script, "-Prefix", prefix, "-Version", "latest", "-DownloadBaseUrl", server.URL, "-NoService"}
				if update {
					args = append(args, "-Update")
				}
				cmd := exec.CommandContext(t.Context(), binary, args...)
				// A Go test launched from pwsh inherits its Core-only module path.
				// Let each PowerShell edition initialize its own default modules.
				for _, env := range os.Environ() {
					if !strings.HasPrefix(strings.ToUpper(env), "PSMODULEPATH=") {
						cmd.Env = append(cmd.Env, env)
					}
				}
				cmd.Env = append(cmd.Env, "TEMP="+tmp, "TMP="+tmp)
				if pathFailure.Load() {
					cmd.Env = append(cmd.Env, "TEST_PATH_FAILURE=1")
				}
				out, err := cmd.CombinedOutput()
				entries, readErr := os.ReadDir(tmp)
				if readErr != nil || len(entries) != 0 {
					t.Fatalf("installer left temporary files: %v %v", entries, readErr)
				}
				if err != nil {
					return fmt.Errorf("%w: %s", err, out)
				}
				return nil
			}
			if err := install(false); err != nil {
				t.Fatal(err)
			}
			exe := filepath.Join(prefix, "bin", "oci-mirror-proxy.exe")
			check := func() {
				t.Helper()
				data, err := os.ReadFile(exe)
				if err != nil || !bytes.Equal(data, payload) {
					t.Fatalf("installed binary changed: %q %v", data, err)
				}
				entries, err := os.ReadDir(filepath.Dir(exe))
				if err != nil || len(entries) != 1 {
					t.Fatalf("staged files remain: %v %v", entries, err)
				}
			}
			check()
			config := filepath.Join(prefix, "config.yaml")
			if err := os.WriteFile(config, []byte("preserved"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := install(true); err != nil {
				t.Fatal(err)
			}
			check()
			if data, err := os.ReadFile(config); err != nil || string(data) != "preserved" {
				t.Fatalf("configuration changed: %q %v", data, err)
			}
			bad.Store(true)
			if err := install(true); err == nil || !strings.Contains(err.Error(), "SHA256 verification failed") {
				t.Fatalf("bad checksum: %v", err)
			}
			check()
			bad.Store(false)
			missing.Store(true)
			if err := install(true); err == nil {
				t.Fatal("missing download did not fail")
			}
			check()
			if sourceRequests.Load() != 0 {
				t.Fatal("download installation attempted a source build")
			}
			missing.Store(false)
			lock, err := os.Open(exe)
			if err != nil {
				t.Fatal(err)
			}
			err = install(true)
			lock.Close()
			if err == nil {
				t.Fatal("replaced a locked executable")
			}
			check()
			pathFailure.Store(true)
			if err := os.Remove(script + ".User.path"); err != nil {
				t.Fatal(err)
			}
			if err := install(true); err == nil || !strings.Contains(err.Error(), "fixture PATH write failed") {
				t.Fatalf("PATH failure: %v", err)
			}
			check()
			pathFailure.Store(false)
		})
	}
}

func TestWindowsInstallerBuild(t *testing.T) {
	for _, shell := range []string{"powershell.exe", "pwsh.exe"} {
		binary, err := exec.LookPath(shell)
		if err != nil {
			continue
		}
		t.Run(shell, func(t *testing.T) {
			var scenario atomic.Value
			scenario.Store("install")
			var releaseRequests atomic.Int32
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path != "/archive/vtest.zip" {
					releaseRequests.Add(1)
					http.NotFound(w, r)
					return
				}
				zw := zip.NewWriter(w)
				files := map[string]string{
					"source/go.mod": "module github.com/runui/oci-mirror-proxy\n\ngo 1.20\n",
					"source/internal/cli/version.go": `package cli
var version = "dev"
func Version() string { return version }
`,
					"source/cmd/oci-mirror-proxy/main.go": `package main
import ("fmt"; "github.com/runui/oci-mirror-proxy/internal/cli")
func main() { fmt.Println(cli.Version()) }
`,
				}
				switch scenario.Load().(string) {
				case "build failure":
					// A valid package that deliberately cannot be built for Windows.
					delete(files, "source/cmd/oci-mirror-proxy/main.go")
					files["source/cmd/oci-mirror-proxy/main_linux.go"] = "package main\nfunc main() {}\n"
				case "bad layout":
					files = map[string]string{"unexpected.txt": "no source root"}
				}
				for name, contents := range files {
					member, err := zw.Create(name)
					if err != nil {
						t.Error(err)
						return
					}
					if _, err := member.Write([]byte(contents)); err != nil {
						t.Error(err)
						return
					}
				}
				if err := zw.Close(); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			work := windowsInstallerTempDir(t)
			prefix := filepath.Join(work, "prefix with spaces & unicode 中文")
			localSource := filepath.Join(work, "local source with spaces & unicode 中文")
			localFiles := map[string]string{
				"go.mod": "module github.com/runui/oci-mirror-proxy\n\ngo 1.20\n",
				"internal/cli/version.go": `package cli
var version = "dev"
func Version() string { return version }
`,
				"cmd/oci-mirror-proxy/main.go": `package main
import ("fmt"; "github.com/runui/oci-mirror-proxy/internal/cli")
func main() { fmt.Println("local-" + cli.Version()) }
`,
			}
			for name, contents := range localFiles {
				path := filepath.Join(localSource, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			tmp := filepath.Join(work, "tmp")
			if err := os.Mkdir(tmp, 0o700); err != nil {
				t.Fatal(err)
			}
			script := isolatedWindowsInstaller(t, work)
			runner := filepath.Join(work, "run.ps1")
			if err := os.WriteFile(runner, []byte(`
$ErrorActionPreference = 'Stop'
$before = (Get-Location).Path
$env:CGO_ENABLED = '1'
$env:GOOS = 'linux'
Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
if ($env:TEST_SCENARIO -eq 'missing go') { $env:PATH = $env:TEMP }
$params = @{ Prefix=$env:TEST_PREFIX; Version='vtest'; DownloadBaseUrl=$env:TEST_URL; Build=$true; NoService=$true }
if ($env:TEST_SCENARIO -eq 'local install') { $params.Version = 'latest' }
if ($env:TEST_SCENARIO -ne 'install') { $params.Update = $true }
$failed = $false
try { & $env:TEST_INSTALLER @params } catch { Write-Output "EXPECTED_FAILURE: $_"; $failed = $true }
if ($env:CGO_ENABLED -ne '1' -or $env:GOOS -ne 'linux' -or (Test-Path Env:GOARCH)) { throw 'Build environment was not restored' }
if ((Get-Location).Path -ne $before) { throw 'Working directory was not restored' }
if ($failed) { exit 1 }
`), 0o600); err != nil {
				t.Fatal(err)
			}
			exe := filepath.Join(prefix, "bin", "oci-mirror-proxy.exe")
			var installed []byte
			for _, name := range []string{"install", "update", "build failure", "bad layout", "missing go", "local install", "local update", "local build failure"} {
				t.Run(name, func(t *testing.T) {
					scenario.Store(name)
					local := strings.HasPrefix(name, "local ")
					if name == "local build failure" {
						if err := os.WriteFile(filepath.Join(localSource, "cmd/oci-mirror-proxy/main.go"), []byte("invalid local source\n"), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					before := requests.Load()
					cmd := exec.CommandContext(t.Context(), binary, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", runner)
					cmd.Dir = work
					if local {
						cmd.Dir = localSource
					}
					for _, env := range os.Environ() {
						if !strings.HasPrefix(strings.ToUpper(env), "PSMODULEPATH=") {
							cmd.Env = append(cmd.Env, env)
						}
					}
					cmd.Env = append(cmd.Env, "TEST_PREFIX="+prefix, "TEST_URL="+server.URL, "TEST_INSTALLER="+script, "TEST_SCENARIO="+name, "TEMP="+tmp, "TMP="+tmp, "GOWORK=off", "GOTOOLCHAIN=local")
					out, err := cmd.CombinedOutput()
					if local && requests.Load() != before {
						t.Fatal("local build requested release metadata or source downloads")
					}
					if name == "install" || name == "update" || name == "local install" || name == "local update" {
						if err != nil {
							t.Fatalf("build installation: %v\n%s", err, out)
						}
						version, err := exec.CommandContext(t.Context(), exe, "version").CombinedOutput()
						wantVersion := "vtest"
						if name == "local install" {
							wantVersion = "local-dev"
						} else if local {
							wantVersion = "local-vtest"
						}
						if err != nil || strings.TrimSpace(string(version)) != wantVersion {
							t.Fatalf("built version: %v %s", err, version)
						}
						installed, err = os.ReadFile(exe)
						if err != nil {
							t.Fatal(err)
						}
					} else {
						message := map[string]string{"build failure": "Source build failed", "local build failure": "Source build failed", "bad layout": "Unexpected source archive layout", "missing go": "-Build requires Go"}[name]
						if err == nil || !strings.Contains(string(out), message) || strings.Contains(string(out), "was not restored") {
							t.Fatalf("expected %s: %v\n%s", name, err, out)
						}
						data, err := os.ReadFile(exe)
						if err != nil || !bytes.Equal(data, installed) {
							t.Fatalf("failed build changed existing binary: %v", err)
						}
					}
					entries, err := os.ReadDir(tmp)
					if err != nil || len(entries) != 0 {
						t.Fatalf("temporary files remain: %v %v", entries, err)
					}
					entries, err = os.ReadDir(filepath.Dir(exe))
					if err != nil || len(entries) != 1 {
						t.Fatalf("staged files remain: %v %v", entries, err)
					}
					for path, contents := range localFiles {
						if name == "local build failure" && path == "cmd/oci-mirror-proxy/main.go" {
							contents = "invalid local source\n"
						}
						data, err := os.ReadFile(filepath.Join(localSource, path))
						if err != nil || string(data) != contents {
							t.Fatalf("local source changed: %s: %v", path, err)
						}
					}
				})
			}
			if releaseRequests.Load() != 0 {
				t.Fatal("source installation requested a prebuilt release")
			}
		})
	}
}

func TestWindowsInstallerBuildElevationDirectory(t *testing.T) {
	for _, shell := range []string{"powershell.exe", "pwsh.exe"} {
		binary, err := exec.LookPath(shell)
		if err != nil {
			continue
		}
		t.Run(shell, func(t *testing.T) {
			work := windowsInstallerTempDir(t)
			source := filepath.Join(work, "source with spaces & unicode 中文")
			if err := os.Mkdir(source, 0o700); err != nil {
				t.Fatal(err)
			}
			script, err := filepath.Abs(filepath.Join("..", "..", "..", "install.ps1"))
			if err != nil {
				t.Fatal(err)
			}
			runner := filepath.Join(work, "run.ps1")
			if err := os.WriteFile(runner, []byte(`
$ErrorActionPreference = 'Stop'
$ast = [Management.Automation.Language.Parser]::ParseFile($env:TEST_INSTALLER, [ref]$null, [ref]$null)
$fn = $ast.Find({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq 'Invoke-ElevatedInstaller' }, $true)
. ([scriptblock]::Create($fn.Extent.Text))
function Start-Process {
    param($FilePath, $Verb, $ArgumentList, $WorkingDirectory, [switch]$Wait, [switch]$PassThru, $ErrorAction)
    $bootstrap = [Text.Encoding]::Unicode.GetString([Convert]::FromBase64String($ArgumentList[-1]))
    $bootstrap = $bootstrap.Replace('if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator))', 'if ($false)')
    $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($bootstrap))
    # Simulate the elevated process starting in the system directory, without UAC.
    Push-Location -LiteralPath $WorkingDirectory
    try { & $FilePath -NoLogo -NoProfile -EncodedCommand $encoded } finally { Pop-Location }
    $result = [pscustomobject]@{ ExitCode = $LASTEXITCODE }
    $result | Add-Member ScriptMethod Dispose {}
    return $result
}
$capture = Join-Path $env:TEMP 'capture.ps1'
[IO.File]::WriteAllText($capture, 'param([switch]$Build, [switch]$NoElevate); if ((Get-Location).ProviderPath -cne $env:TEST_SOURCE) { throw "Local build directory lost during elevation" }')
Set-Location -LiteralPath $env:TEST_SOURCE
$code = Invoke-ElevatedInstaller $capture @{ Build = $true } ''
if ($code -ne 0) { throw "Elevated build failed: $code" }
`), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), binary, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", runner)
			for _, env := range os.Environ() {
				if !strings.HasPrefix(strings.ToUpper(env), "PSMODULEPATH=") {
					cmd.Env = append(cmd.Env, env)
				}
			}
			cmd.Env = append(cmd.Env, "TEST_INSTALLER="+script, "TEST_SOURCE="+source, "TEMP="+work, "TMP="+work)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("build directory after elevation: %v\n%s", err, out)
			}
		})
	}
}

// Match the Unix installer fixtures: substitute the platform service manager,
// never register a host service or modify host directory permissions.
func TestWindowsInstallerService(t *testing.T) {
	root := windowsInstallerTempDir(t)
	source := filepath.Join(root, "fixture.go")
	if err := os.WriteFile(source, []byte(`package main
import ("os"; "strings"; "fmt")
func main() {
 args := os.Args[1:]
 if len(args)==0 { os.Exit(1) }
 switch args[0] {
 case "init":
  for i,a := range args { if a=="--config" { if err:=os.WriteFile(args[i+1],[]byte("registries:\n  docker.io:\n    adaptive:\n      enabled: false\n"),0600); err!=nil { panic(err) }; return } }
  os.Exit(1)
 case "status":
  if strings.Contains(os.Getenv("TEST_SCENARIO"),"failed") { os.Exit(1) }
  case "remote":
   fmt.Fprintln(os.Stderr, "warning: subscription diagnostic")
   if os.Getenv("TEST_SCENARIO")=="sync failure" { os.Exit(17) }
  case "server":
   fmt.Fprintln(os.Stderr, "warning: server diagnostic")
   if os.Getenv("TEST_SCENARIO")=="info failure" { os.Exit(17) }
   if os.Getenv("TEST_SCENARIO")=="invalid info" { fmt.Println("{}"); return }
   fmt.Println("{\"schema_version\":1,\"config\":{\"proxy_url\":\"http://127.0.0.1:24817\",\"registries\":[{\"alias\":\"docker.io\",\"origin\":\"https://registry-1.docker.io\"}]}}")
  case "dockerd":
   fmt.Fprintln(os.Stderr, "warning: skipping gcr.io")
   if os.Getenv("TEST_SCENARIO")=="docker failure" { os.Exit(17) }
 default: os.Exit(1)
 }
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "fixture.exe")
	if out, err := exec.CommandContext(t.Context(), "go", "build", "-o", exe, source).CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, out)
	}
	payload, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	member, err := zw.Create("oci-mirror-proxy.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := member.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	var sourceArchive bytes.Buffer
	szw := zip.NewWriter(&sourceArchive)
	fixtureSource, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string][]byte{
		"source/go.mod":                       []byte("module github.com/runui/oci-mirror-proxy\n\ngo 1.20\n"),
		"source/cmd/oci-mirror-proxy/main.go": fixtureSource,
	} {
		member, err := szw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := member.Write(contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := szw.Close(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/archive/vtest.zip" {
			_, _ = w.Write(sourceArchive.Bytes())
		} else if strings.HasSuffix(r.URL.Path, "/SHA256SUMS") {
			for _, arch := range []string{"amd64", "arm64"} {
				fmt.Fprintf(w, "%x  oci-mirror-proxy_vtest_windows_%s.zip\n", sha256.Sum256(archive.Bytes()), arch)
			}
		} else if strings.HasSuffix(r.URL.Path, ".zip") {
			_, _ = w.Write(archive.Bytes())
		} else {
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	original, err := os.ReadFile(filepath.Join("..", "..", "..", "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, shell := range []string{"powershell.exe", "pwsh.exe"} {
		binary, err := exec.LookPath(shell)
		if err != nil {
			continue
		}
		for _, testCase := range []string{"install", "sync failure", "info failure", "invalid info", "docker failure", "update", "failed install", "failed update", "custom definition", "custom account", "bad wrapper", "build install", "build update", "build failed update"} {
			t.Run(shell+"/"+testCase, func(t *testing.T) {
				scenario := strings.TrimPrefix(testCase, "build ")
				work := windowsInstallerTempDir(t)
				prefix := filepath.Join(work, "prefix with spaces & unicode 中文")
				config := filepath.Join(work, "config with spaces", "config.yaml")
				script := isolateWindowsInstallerPaths(string(original))
				script = strings.Replace(script, "$env:GOENV = 'off'", "$env:GOENV = 'off'; if ($env:GOPROXY -cne 'https://fixture-proxy.invalid') { throw 'Go proxy configuration was discarded' }", 1)
				script = regexp.MustCompile(`(?ms)^function Test-IsAdministrator \{.*?^}`).ReplaceAllString(script, `function Test-IsAdministrator { return $$true }`)
				// Only these platform operations are substituted. Download, checksum,
				// publication, definition comparison and rollback execute unchanged.
				script = regexp.MustCompile(`(?ms)^function Protect-ServicePath\(.*?^}`).ReplaceAllString(script, `function Protect-ServicePath {}`)
				script = regexp.MustCompile(`(?ms)^function Invoke-WinSW\(.*?^}`).ReplaceAllStringFunc(script, func(string) string {
					return `function Invoke-WinSW([string]$Command) {
    Add-Content -LiteralPath $env:TEST_LOG -Value "winsw $Command"
    if ($Command -eq 'install') { $global:fixtureExists = $true }
    if ($Command -eq 'uninstall') { $global:fixtureExists = $false }
}`
				})
				wrapper := []byte("WinSW fixture\n")
				script = strings.ReplaceAll(script, "923111C7142B3DC783A3C722B19B8A21BCB78222D7A136AC33F0CA8A29F4CB66", fmt.Sprintf("%X", sha256.Sum256(wrapper)))
				installer := filepath.Join(work, "install.ps1")
				if err := os.WriteFile(installer, []byte(script), 0o600); err != nil {
					t.Fatal(err)
				}
				prelude := `
$ErrorActionPreference = 'Stop'
$global:fixtureExists = $false
$global:fixtureStatus = 'Stopped'
function Invoke-WebRequest {
 param($Uri, $OutFile, $Headers, $TimeoutSec, [switch]$UseBasicParsing)
 if ($Uri -like '*WinSW.NET4.exe') {
  [IO.File]::WriteAllText($OutFile, "WinSW fixture` + "`n" + `")
 } else { Microsoft.PowerShell.Utility\Invoke-WebRequest -Uri $Uri -OutFile $OutFile -UseBasicParsing }
}
function Get-CimInstance {
 param($ClassName, $Filter)
 if ($global:fixtureExists) {
  $account = if ($env:TEST_SCENARIO -eq 'custom account') { 'CustomUser' } else { 'LocalSystem' }
  [pscustomobject]@{ PathName = '"' + $env:TEST_PREFIX + '\service\oci-mirror-proxy.exe"'; StartName = $account; StartMode = 'Manual' }
 }
}
function Get-Service {
 param($Name)
 if (-not $global:fixtureExists) { throw 'No fixture service' }
 $controller = [pscustomobject]@{ Status = $global:fixtureStatus }
 $controller | Add-Member ScriptMethod WaitForStatus { param($state,$timeout) if ($state -ne $global:fixtureStatus) { throw 'Wrong status' } }
 $controller | Add-Member ScriptMethod Dispose {}
 return $controller
}
function Stop-Service { param($Name) Add-Content $env:TEST_LOG 'stop'; $global:fixtureStatus = 'Stopped' }
function Start-Service { param($Name) Add-Content $env:TEST_LOG 'start'; $global:fixtureStatus = 'Running' }
function Set-Service { param($Name,$StartupType) Add-Content $env:TEST_LOG "startup $StartupType" }
# Avoid slow readiness retries; still run the real health/rollback branches.
function Start-Sleep { param($Milliseconds) throw 'fixture readiness failed' }
$params = @{ Prefix=$env:TEST_PREFIX; Config=$env:TEST_CONFIG; Version='vtest'; DownloadBaseUrl=$env:TEST_URL }
$buildEnvironment = @{}
foreach ($name in @('GOCACHE','GOMODCACHE','GOPATH','GOTMPDIR','GOENV','GOWORK','GOFLAGS','CGO_ENABLED','GOOS','GOARCH','GOPROXY','GOSUMDB','GOPRIVATE','GONOPROXY','GONOSUMDB','GOINSECURE','GOTOOLCHAIN')) { $buildEnvironment[$name] = [Environment]::GetEnvironmentVariable($name,'Process') }
$env:GOENV = Join-Path $env:TEMP 'caller-go-env'
[IO.File]::WriteAllText($env:GOENV, "GOPROXY=https://fixture-proxy.invalid` + "`n" + `")
Remove-Item Env:GOPROXY -ErrorAction SilentlyContinue
$env:GOCACHE = Join-Path $env:TEMP 'caller-cache'
$env:GOMODCACHE = Join-Path $env:TEMP 'caller-modules'
$env:GOPATH = Join-Path $env:TEMP 'caller-gopath'
& $env:TEST_INSTALLER @params
if ($env:GOCACHE -cne (Join-Path $env:TEMP 'caller-cache') -or $env:GOMODCACHE -cne (Join-Path $env:TEMP 'caller-modules') -or $env:GOPATH -cne (Join-Path $env:TEMP 'caller-gopath')) { throw 'Build cache environment was not restored' }
if (Test-Path Env:GOPROXY) { throw 'Build proxy environment was not restored' }
foreach ($path in @($env:GOCACHE,$env:GOMODCACHE,$env:GOPATH)) { if (Test-Path -LiteralPath $path) { throw 'Elevated build wrote into caller caches' } }
if ($env:TEST_SCENARIO -in @('install', 'sync failure', 'info failure', 'invalid info', 'docker failure')) { return }
$global:fixtureStatus = 'Running'
Set-Service oci-mirror-proxy Manual
if ($env:TEST_SCENARIO -eq 'custom definition') { Add-Content "$env:TEST_PREFIX\service\oci-mirror-proxy.xml" '<!-- custom -->' }
if ($env:TEST_SCENARIO -eq 'bad wrapper') { [IO.File]::WriteAllText("$env:TEST_PREFIX\service\oci-mirror-proxy.exe", 'custom wrapper') }
[IO.File]::WriteAllText("$env:TEST_PREFIX\bin\oci-mirror-proxy.exe", 'previous binary')
[IO.File]::WriteAllText($env:TEST_CONFIG, 'preserved config')
try { & $env:TEST_INSTALLER @params -Update } catch { Write-Output "EXPECTED_FAILURE: $_" }
if (Test-Path Env:GOPROXY) { throw 'Update proxy environment was not restored' }
foreach ($name in $buildEnvironment.Keys) { [Environment]::SetEnvironmentVariable($name,$buildEnvironment[$name],'Process') }
if ($global:fixtureStatus -ne 'Running') { throw 'Previous service was not restored' }
`
				// A failed initial install must not first run a successful install.
				if strings.HasPrefix(testCase, "build ") {
					prelude = strings.Replace(prelude, "$params = @{", "$params = @{ Build=$true;", 1)
				}
				if scenario == "failed install" {
					prelude = strings.Replace(prelude, "& $env:TEST_INSTALLER @params\n", "try { & $env:TEST_INSTALLER @params } catch { Write-Output \"EXPECTED_FAILURE: $_\" }; return\n", 1)
				} else if scenario == "failed update" {
					prelude = strings.Replace(prelude, "& $env:TEST_INSTALLER @params\n", "$env:TEST_SCENARIO = 'install'; & $env:TEST_INSTALLER @params; $env:TEST_SCENARIO = 'failed update'\n", 1)
				}
				// Go sources and PowerShell fixture paths include non-ASCII names;
				// Windows PowerShell 5.1 needs a UTF-8 BOM for script literals.
				runner := filepath.Join(work, "run.ps1")
				if err := os.WriteFile(runner, append([]byte{0xef, 0xbb, 0xbf}, []byte(prelude)...), 0o600); err != nil {
					t.Fatal(err)
				}
				cmd := exec.CommandContext(t.Context(), binary, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", runner)
				for _, env := range os.Environ() {
					if !strings.HasPrefix(strings.ToUpper(env), "PSMODULEPATH=") {
						cmd.Env = append(cmd.Env, env)
					}
				}
				log := filepath.Join(work, "operations.log")
				cmd.Env = append(cmd.Env, "TEST_LOG="+log, "TEST_PREFIX="+prefix, "TEST_CONFIG="+config, "TEST_URL="+server.URL, "TEST_INSTALLER="+installer, "TEST_SCENARIO="+scenario, "TEMP="+work, "TMP="+work)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("installer fixture: %v\n%s", err, out)
				}
				ops, _ := os.ReadFile(log)
				installed, readErr := os.ReadFile(filepath.Join(prefix, "bin", "oci-mirror-proxy.exe"))
				matchesPayload := bytes.Equal(installed, payload)
				if strings.HasPrefix(testCase, "build ") && (scenario == "install" || scenario == "update") {
					out, err := exec.CommandContext(t.Context(), filepath.Join(prefix, "bin", "oci-mirror-proxy.exe"), "status").CombinedOutput()
					if err != nil {
						t.Fatalf("built service binary: %v\n%s", err, out)
					}
					matchesPayload = true
				}
				if scenario == "failed install" {
					if !os.IsNotExist(readErr) || !bytes.Contains(ops, []byte("winsw uninstall")) {
						t.Fatalf("initial rollback: %q %v\n%s\n%s", installed, readErr, ops, out)
					}
				} else if scenario == "install" || scenario == "sync failure" || scenario == "info failure" || scenario == "invalid info" || scenario == "docker failure" {
					if scenario == "install" && bytes.Contains(out, []byte("Proxy installation succeeded")) {
						t.Fatalf("warning caused a false Docker failure:\n%s", out)
					}
					if scenario == "sync failure" && !bytes.Contains(out, []byte("Remote subscription synchronization failed")) {
						t.Fatalf("missing synchronization warning:\n%s", out)
					}
					if (scenario == "info failure" || scenario == "invalid info" || scenario == "docker failure") && bytes.Count(out, []byte("Proxy installation succeeded")) != 1 {
						t.Fatalf("expected one Docker recovery warning:\n%s", out)
					}
					if !matchesPayload || !bytes.Contains(ops, []byte("startup Automatic")) || !bytes.Contains(ops, []byte("start")) {
						t.Fatalf("install: %v\n%s\n%s", readErr, ops, out)
					}
					definition, err := os.ReadFile(filepath.Join(prefix, "service", "oci-mirror-proxy.xml"))
					if err != nil {
						t.Fatal(err)
					}
					var service struct {
						Executable string `xml:"executable"`
						Arguments  string `xml:"arguments"`
						StartMode  string `xml:"startmode"`
					}
					if err := xml.Unmarshal(definition, &service); err != nil {
						t.Fatal(err)
					}
					if service.Executable != filepath.Join(prefix, "bin", "oci-mirror-proxy.exe") || service.Arguments != `serve --config "`+config+`" --admin-socket "\\.\pipe\oci-mirror-proxy-admin"` || service.StartMode != "Automatic" {
						t.Fatalf("incorrect service definition: %+v", service)
					}
				} else {
					if (scenario == "update" && !matchesPayload) || (scenario != "update" && !bytes.Equal(installed, []byte("previous binary"))) {
						t.Fatalf("wrong published binary: %v\n%s\n%s", readErr, ops, out)
					}
					data, err := os.ReadFile(config)
					if err != nil || string(data) != "preserved config" {
						t.Fatalf("config overwritten: %q %v", data, err)
					}
					if scenario == "failed update" && !bytes.Contains(ops, []byte("startup Manual")) {
						t.Fatalf("startup type not restored: %s", ops)
					}
				}
				entries, _ := filepath.Glob(filepath.Join(work, "oci-mirror-proxy-install-*"))
				staged, _ := filepath.Glob(filepath.Join(prefix, "bin", ".oci-mirror-proxy-*"))
				if len(entries)+len(staged) > 0 {
					t.Fatalf("temporary files remain: %v %v", entries, staged)
				}
			})
		}
	}
}

func windowsInstallerTempDir(t *testing.T) string {
	t.Helper()
	// Go may return an 8.3 TEMP path while PowerShell expands it to long names.
	// Share one representation across environment variables and assertions.
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWindowsInstallerElevation(t *testing.T) {
	original, err := os.ReadFile(filepath.Join("..", "..", "..", "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, shell := range []string{"powershell.exe", "pwsh.exe"} {
		binary, err := exec.LookPath(shell)
		if err != nil {
			continue
		}
		for _, scenario := range []string{"success", "child failure", "not elevated", "cancel", "launch failure", "no elevate", "no service", "administrator", "build", "switch false"} {
			t.Run(shell+"/"+scenario, func(t *testing.T) {
				root := windowsInstallerTempDir(t)
				installer := filepath.Join(root, "installer with spaces.ps1")
				script := regexp.MustCompile(`(?ms)^function Test-IsAdministrator \{.*?^}`).ReplaceAllString(string(original), `function Test-IsAdministrator { return ($$env:TEST_SCENARIO -eq 'administrator') }`)
				// Exercise the real elevation transport without network/compiler work.
				script = regexp.MustCompile(`(?ms)^function Prepare-Installation \{.*?^}`).ReplaceAllString(script, `function Prepare-Installation {
    $$binary = Join-Path $$tempDir 'prepared.exe'
    [IO.File]::WriteAllText($$binary, 'prepared fixture')
    $$wrapperDownload = $$binary
}`)
				// Stop before querying SCM, downloading or changing any host files.
				script = strings.Replace(script, "$existingService = Get-CimInstance", "throw 'REACHED_INSTALLATION'; $existingService = Get-CimInstance", 1)
				script = strings.Replace(script, "\n$nativeArch = $env:PROCESSOR_ARCHITEW6432", "\nthrow 'REACHED_INSTALLATION'; $nativeArch = $env:PROCESSOR_ARCHITECTURE", 1)
				if err := os.WriteFile(installer, []byte(script), 0o600); err != nil {
					t.Fatal(err)
				}
				runner := `
$ErrorActionPreference = 'Stop'
function Start-Process {
 param($FilePath,$Verb,$ArgumentList,$WorkingDirectory,[switch]$Wait,[switch]$PassThru,$ErrorAction)
 if ($Verb -ne 'RunAs' -or -not $Wait -or -not $PassThru -or -not [IO.File]::Exists($FilePath)) { throw 'Incorrect elevation invocation' }
 if ($ArgumentList -contains '-ExecutionPolicy') { throw 'Execution policy was bypassed' }
 if ($env:TEST_SCENARIO -eq 'cancel') { throw (New-Object ComponentModel.Win32Exception(1223)) }
 if ($env:TEST_SCENARIO -eq 'launch failure') { throw (New-Object ComponentModel.Win32Exception(2)) }
 $encoded = $ArgumentList[-1]
 $bootstrap = [Text.Encoding]::Unicode.GetString([Convert]::FromBase64String($encoded))
 $tokens=$null; $errors=$null
 [Management.Automation.Language.Parser]::ParseInput($bootstrap,[ref]$tokens,[ref]$errors) > $null
 if ($errors.Count) { throw 'Invalid bootstrap' }
 if ($bootstrap -notmatch "FromBase64String\('([A-Za-z0-9+/=]+)'\)") { throw 'Missing structured payload' }
 $payload = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($Matches[1])) | ConvertFrom-Json
 if ($payload.Script -cne $env:TEST_INSTALLER) { throw 'Script path changed' }
 $p = $payload.Parameters
 if ($p.Prefix -cne $env:TEST_PREFIX -or $p.Config -cne $env:TEST_CONFIG -or $p.Version -cne 'vtest' -or $p.DownloadBaseUrl -cne $env:DOWNLOAD_BASE_URL -or -not $p.NoElevate) { throw 'Parameters changed' }
 if ($p.Update -ne $true -or $p.NoService -ne $false) { throw 'Switch values changed' }
 if ($env:TEST_SCENARIO -eq 'build') {
  if (-not $p.Build -or $payload.GoPath -cne (Get-Command go -CommandType Application).Source) { throw 'Build context lost' }
 } elseif ($null -ne $p.Build -and $p.Build -ne $false) { throw 'Build unexpectedly enabled' }
 # Execute the actual generated bootstrap with a harmless parameter-capture
 # script. Only substitute its privilege check; never trigger real UAC.
 $capture = Join-Path $env:TEMP 'capture.ps1'
  [IO.File]::WriteAllText($capture, 'param($Prefix,$Config,$Version,$DownloadBaseUrl,$WinSWDownloadBaseUrl,$ServiceName,$Command,$PreparedBinary,$PreparedWrapper,$PreparedHash,$InfoID,[switch]$ServiceStageOnly,[switch]$Update,[switch]$NoService,[switch]$Build,[switch]$NoElevate); if ($Prefix -cne $env:TEST_PREFIX -or $DownloadBaseUrl -cne $env:DOWNLOAD_BASE_URL -or -not $Update -or $NoService) { throw "Capture mismatch" }; if (-not $PreparedBinary -or (Get-FileHash -LiteralPath $PreparedBinary -Algorithm SHA256).Hash -ne $PreparedHash) { throw "Missing prepared binary" }; if ($env:TEST_SCENARIO -eq "child failure") { throw "child failed" }')
 $payload.Script = $capture
 $data = $payload | ConvertTo-Json -Compress -Depth 5
 $replacement = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($data))
 $bootstrap = $bootstrap.Replace($Matches[1], $replacement)
 $condition = if ($env:TEST_SCENARIO -eq 'not elevated') { 'if ($true)' } else { 'if ($false)' }
 $bootstrap = $bootstrap.Replace('if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator))', $condition)
 $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($bootstrap))
 & $FilePath -NoLogo -NoProfile -EncodedCommand $encoded
 $result = [pscustomobject]@{ ExitCode=$LASTEXITCODE }
 $result | Add-Member ScriptMethod Dispose {}
 return $result
}
$params = @{ Prefix=$env:TEST_PREFIX; Config=$env:TEST_CONFIG; Update=$true; NoService=$false }
switch ($env:TEST_SCENARIO) {
 'no elevate' { $params.NoElevate=$true }
 'no service' { $params.NoService=$true }
 'build' { $params.Build=$true }
 'switch false' { $params.Build=$false; $params.NoElevate=$false }
}
try { & $env:TEST_INSTALLER @params; exit $LASTEXITCODE } catch {
 $expected = switch ($env:TEST_SCENARIO) {
  'cancel' { 'approval was canceled' }
  'launch failure' { 'specified' }
  'no elevate' { '-NoElevate disables UAC' }
  'no service' { 'REACHED_INSTALLATION' }
  'administrator' { 'REACHED_INSTALLATION' }
  default { throw }
 }
 if ($env:TEST_SCENARIO -ne 'launch failure' -and $_.ToString() -notlike "*$expected*") { throw }
 Write-Output 'EXPECTED_FAILURE'
 exit 0
}
`
				path := filepath.Join(root, "run.ps1")
				if err := os.WriteFile(path, []byte(runner), 0o600); err != nil {
					t.Fatal(err)
				}
				cmd := exec.CommandContext(t.Context(), binary, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path)
				for _, env := range os.Environ() {
					if !strings.HasPrefix(strings.ToUpper(env), "PSMODULEPATH=") {
						cmd.Env = append(cmd.Env, env)
					}
				}
				cmd.Env = append(cmd.Env, "TEST_SCENARIO="+scenario, "TEST_INSTALLER="+installer, "TEST_PREFIX="+filepath.Join(root, "prefix with spaces & 中文's"), "TEST_CONFIG="+filepath.Join(root, "config 中文's", "config.yaml"), "VERSION=vtest", "DOWNLOAD_BASE_URL=https://example.invalid/a?x='quote'&y=$notcode", "TEMP="+root, "TMP="+root)
				out, err := cmd.CombinedOutput()
				if scenario == "child failure" || scenario == "not elevated" {
					if err == nil || !strings.Contains(string(out), "Elevated installation failed") {
						t.Fatalf("child failure not propagated: %v\n%s", err, out)
					}
				} else if err != nil {
					t.Fatalf("elevation: %v\n%s", err, out)
				}
				if _, err := os.Stat(filepath.Join(root, "prefix with spaces & 中文's")); !os.IsNotExist(err) {
					t.Fatal("parent modified installation before approval")
				}
			})
		}
	}
}

func TestWindowsInstallerPermissionRules(t *testing.T) {
	for _, shell := range []string{"powershell.exe", "pwsh.exe"} {
		binary, err := exec.LookPath(shell)
		if err != nil {
			continue
		}
		t.Run(shell, func(t *testing.T) {
			script, err := filepath.Abs(filepath.Join("..", "..", "..", "install.ps1"))
			if err != nil {
				t.Fatal(err)
			}
			command := fmt.Sprintf(`$ErrorActionPreference='Stop'
$ast=[Management.Automation.Language.Parser]::ParseFile('%s',[ref]$null,[ref]$null)
$fn=$ast.Find({param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq 'Protect-ServicePath'},$true)
. ([scriptblock]::Create($fn.Extent.Text))
function Set-Acl { param($LiteralPath,$AclObject) $script:acl=$AclObject }
Protect-ServicePath 'fixture' -Directory -ReadUsers
$sddl=$acl.GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]::Access)
if ($sddl -notlike '*;;;BU)*' -or $sddl -like '*FA;;;BU)*' -or $sddl -notlike 'D:P*') { throw "Wrong executable directory permissions: $sddl" }
Protect-ServicePath 'fixture' -ReadUsers
$sddl=$acl.GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]::Access)
if ($sddl -notlike '*;;;BU)*' -or $sddl -like '*FA;;;BU)*') { throw "Wrong executable permissions: $sddl" }
Protect-ServicePath 'fixture'
$sddl=$acl.GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]::Access)
if ($sddl -like '*;;;BU)*' -or $sddl -notlike '*FA;;;SY)*' -or $sddl -notlike '*FA;;;BA)*') { throw "Wrong service configuration permissions: $sddl" }
`, strings.ReplaceAll(script, "'", "''"))
			cmd := exec.CommandContext(t.Context(), binary, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command)
			for _, env := range os.Environ() {
				if !strings.HasPrefix(strings.ToUpper(env), "PSMODULEPATH=") {
					cmd.Env = append(cmd.Env, env)
				}
			}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("permissions: %v\n%s", err, out)
			}
		})
	}
}

func TestWindowsInstallerDockerFailureWarns(t *testing.T) {
	for _, shell := range []string{"powershell.exe", "pwsh.exe"} {
		binary, err := exec.LookPath(shell)
		if err != nil {
			continue
		}
		t.Run(shell, func(t *testing.T) {
			root := windowsInstallerTempDir(t)
			installer, err := filepath.Abs(filepath.Join("..", "..", "..", "install.ps1"))
			if err != nil {
				t.Fatal(err)
			}
			runner := filepath.Join(root, "run.ps1")
			if err := os.WriteFile(runner, []byte(`
$ErrorActionPreference = 'Stop'
$ast = [Management.Automation.Language.Parser]::ParseFile($env:TEST_INSTALLER, [ref]$null, [ref]$null)
foreach ($name in @('Write-DockerRecovery', 'Connect-UserDocker', 'Show-NativeResult')) {
    $fn = $ast.Find({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq $name }, $true)
    . ([scriptblock]::Create($fn.Extent.Text))
}
$SkipDocker = $false; $NoService = $false; $Update = $true
$binDir = $env:TEMP; $InfoID = 'fixture'; $Config = 'C:\private config\config.yaml'
$adminPipe = '\\.\pipe\custom-proxy-admin'
function Fail-Docker { $global:LASTEXITCODE = 17 }
function Throw-Docker { throw 'Docker unavailable' }
function Invoke-InstallerNative($Executable, $Arguments) {
    & $Executable
    return [pscustomobject]@{ ExitCode = $LASTEXITCODE; Stdout = ''; Stderr = '' }
}
foreach ($failure in @('Fail-Docker', 'Throw-Docker')) {
    $destination = $failure
    $target = Join-Path $binDir "server-info-$InfoID.json"
    [IO.File]::WriteAllText($target, '{}')
    Connect-UserDocker
    if ($LASTEXITCODE -ne 0 -or (Test-Path -LiteralPath $target)) { throw 'Optional Docker failure leaked exit status or handoff' }
}
`), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), binary, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", runner)
			cmd.Env = append(os.Environ(), "TEST_INSTALLER="+installer, "TEMP="+root, "TMP="+root)
			out, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(out), "Proxy installation succeeded") || !strings.Contains(string(out), "dockerd apply --server-info server-info.json --verify") || !strings.Contains(string(out), `\\.\pipe\custom-proxy-admin`) {
				t.Fatalf("Docker recovery warning: %v\n%s", err, out)
			}
		})
	}
}

func TestWindowsInstallerWrapperDownload(t *testing.T) {
	for _, shell := range []string{"powershell.exe", "pwsh.exe"} {
		binary, err := exec.LookPath(shell)
		if err != nil {
			continue
		}
		t.Run(shell, func(t *testing.T) {
			installer, err := filepath.Abs(filepath.Join("..", "..", "..", "install.ps1"))
			if err != nil {
				t.Fatal(err)
			}
			root := windowsInstallerTempDir(t)
			runner := filepath.Join(root, "wrapper.ps1")
			if err := os.WriteFile(runner, []byte(`
$ErrorActionPreference = 'Stop'
$ast = [Management.Automation.Language.Parser]::ParseFile($env:TEST_INSTALLER, [ref]$null, [ref]$null)
$fn = $ast.Find({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq 'Save-ServiceWrapper' }, $true)
. ([scriptblock]::Create($fn.Extent.Text))
$path = Join-Path $env:TEMP 'wrapper.exe'
[IO.File]::WriteAllText($path, 'pinned fixture')
$wrapperHash = (Get-FileHash -LiteralPath $path).Hash
function Invoke-WebRequest {
    param($Uri, $OutFile, $Headers, $TimeoutSec, [switch]$UseBasicParsing)
    $script:calls++
    if ($TimeoutSec -ne 30) { throw 'Missing download timeout' }
    if ($script:mode -eq 'offline') { throw 'Network unavailable' }
    if ($script:mode -eq 'corrupt') { [IO.File]::WriteAllText($OutFile, 'tampered'); return }
    if ($Uri -like 'https://github.com/*') { throw 'Release web endpoint unavailable' }
    if ($Uri -notlike 'https://api.github.com/*' -or $Headers.Accept -ne 'application/octet-stream') { throw 'Wrong official fallback' }
    [IO.File]::WriteAllText($OutFile, 'pinned fixture')
}
$WinSWDownloadBaseUrl = 'https://github.com/winsw/winsw'
$script:calls = 0; $script:mode = 'fallback'
Save-ServiceWrapper $path
if ($calls -ne 2 -or (Get-FileHash -LiteralPath $path).Hash -ne $wrapperHash) { throw 'Official fallback failed' }
foreach ($base in @('https://github.com/winsw/winsw', 'https://mirror.example/winsw')) {
    $WinSWDownloadBaseUrl = $base
    $script:calls = 0; $script:mode = 'offline'
    try { Save-ServiceWrapper $path; throw 'Offline download accepted' } catch { if ($_ -notlike '*Could not download WinSW after bounded retries*') { throw } }
    $expected = if ($base -like '*github.com*') { 4 } else { 2 }
    if ($calls -ne $expected) { throw 'Retry count or custom mirror isolation failed' }
}
$script:calls = 0; $script:mode = 'corrupt'
try { Save-ServiceWrapper $path; throw 'Corrupt download accepted' } catch { if ($_ -notlike '*WinSW SHA256 verification failed*') { throw } }
if ($calls -ne 1) { throw 'Checksum failure was retried' }
`), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), binary, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", runner)
			for _, env := range os.Environ() {
				if !strings.HasPrefix(strings.ToUpper(env), "PSMODULEPATH=") {
					cmd.Env = append(cmd.Env, env)
				}
			}
			cmd.Env = append(cmd.Env, "TEST_INSTALLER="+installer, "TEMP="+root, "TMP="+root)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("wrapper download: %v\n%s", err, out)
			}
		})
	}
}

func TestWindowsNativeFixture(t *testing.T) {
	if os.Getenv("OCI_NATIVE_FIXTURE") != "1" {
		return
	}
	fmt.Fprint(os.Stdout, "{\"message\":\"native stdout "+"\u4e2d\u6587"+"\"}")
	fmt.Fprintln(os.Stderr, "warning: native stderr")
	if os.Getenv("OCI_NATIVE_LARGE") == "1" {
		fmt.Fprint(os.Stdout, strings.Repeat("o", 128*1024))
		fmt.Fprint(os.Stderr, strings.Repeat("e", 128*1024))
	}
	time.Sleep(25 * time.Millisecond)
	if marker := os.Getenv("OCI_NATIVE_MARKER"); marker != "" {
		if err := os.WriteFile(marker, []byte(strings.Join(os.Args, "\n")), 0o600); err != nil {
			os.Exit(19)
		}
	}
	if os.Getenv("OCI_NATIVE_FAIL") == "1" {
		os.Exit(17)
	}
	os.Exit(0)
}

func TestWindowsInstallerNativeStderr(t *testing.T) {
	for _, shell := range []string{"powershell.exe", "pwsh.exe"} {
		binary, err := exec.LookPath(shell)
		if err != nil {
			if os.Getenv("REQUIRE_WINDOWS_SHELLS") == "1" {
				t.Fatalf("required shell %s: %v", shell, err)
			}
			continue
		}
		t.Run(shell, func(t *testing.T) {
			root := windowsInstallerTempDir(t)
			installer, err := filepath.Abs(filepath.Join("..", "..", "..", "install.ps1"))
			if err != nil {
				t.Fatal(err)
			}
			fixture, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			runner := filepath.Join(root, "native.ps1")
			if err := os.WriteFile(runner, []byte(`
$ErrorActionPreference = 'Stop'
$ast = [Management.Automation.Language.Parser]::ParseFile($env:TEST_INSTALLER, [ref]$null, [ref]$null)
foreach ($name in @('Invoke-InstallerNative', 'Show-NativeResult', 'Write-DockerRecovery', 'Connect-UserDocker')) {
    $fn = $ast.Find({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq $name }, $true)
    . ([scriptblock]::Create($fn.Extent.Text))
}
$env:OCI_NATIVE_FIXTURE = '1'
$env:OCI_NATIVE_MARKER = Join-Path $env:TEMP 'marker'
$arguments = @('-test.run=^TestWindowsNativeFixture$', '--', 'path with spaces', "single'quote")
$result = Invoke-InstallerNative $env:TEST_FIXTURE $arguments
if ($result.ExitCode -ne 0 -or ($result.Stdout | ConvertFrom-Json).message -ne ('native stdout ' + [char]0x4e2d + [char]0x6587) -or $result.Stderr -notmatch 'warning: native stderr') { throw 'Native streams were not isolated' }
if ($ErrorActionPreference -ne 'Stop') { throw 'Preference leaked' }
$marker = [IO.File]::ReadAllText($env:OCI_NATIVE_MARKER)
if (-not $marker.Contains('path with spaces') -or -not $marker.Contains("single'quote")) { throw 'Arguments corrupted' }
$env:OCI_NATIVE_FAIL = '1'
$result = Invoke-InstallerNative $env:TEST_FIXTURE $arguments
if ($result.ExitCode -ne 17) { throw 'Native exit code lost' }
$env:OCI_NATIVE_LARGE = '1'
$result = Invoke-InstallerNative $env:TEST_FIXTURE $arguments
if ($result.Stdout.Length -lt 131072 -or $result.Stderr.Length -lt 131072) { throw 'Large output lost' }
$env:OCI_NATIVE_LARGE = ''
$failed = $false
try { Invoke-InstallerNative (Join-Path $env:TEMP 'missing.exe') @() | Out-Null } catch { $failed = $true }
if (-not $failed) { throw 'Launch failure was accepted' }
$env:OCI_NATIVE_FAIL = ''
$SkipDocker = $false; $NoService = $false; $Update = $false
$binDir = $env:TEMP; $InfoID = 'native'; $Config = 'private config'
$destination = $env:TEST_FIXTURE
$target = Join-Path $binDir 'server-info-native.json'
[IO.File]::WriteAllText($target, '{}')
$native = ${function:Invoke-InstallerNative}
function Invoke-InstallerNative($Executable, $Arguments) {
    & $native $Executable (@('-test.run=^TestWindowsNativeFixture$', '--') + $Arguments)
}
Connect-UserDocker
if (Test-Path -LiteralPath $target) { throw 'Handoff was not cleaned' }
if (@(Get-ChildItem -LiteralPath $env:TEMP -Filter 'oci-native-*').Count -ne 0) { throw 'Capture files leaked' }
`), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), binary, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", runner)
			cmd.Env = append(os.Environ(), "TEST_INSTALLER="+installer, "TEST_FIXTURE="+fixture, "TEMP="+root, "TMP="+root)
			out, err := cmd.CombinedOutput()
			if err != nil || strings.Contains(string(out), "Proxy installation succeeded") {
				t.Fatalf("native stderr: %v\n%s", err, out)
			}
		})
	}
}

func TestWindowsInstallerPath(t *testing.T) {
	for _, shell := range []string{"powershell.exe", "pwsh.exe"} {
		binary, err := exec.LookPath(shell)
		if err != nil {
			continue
		}
		t.Run(shell, func(t *testing.T) {
			root := t.TempDir()
			installer, err := filepath.Abs(filepath.Join("..", "..", "..", "install.ps1"))
			if err != nil {
				t.Fatal(err)
			}
			runner := `
$ErrorActionPreference='Stop'
$help=Get-Help $env:TEST_INSTALLER -Full
foreach ($name in @('Prefix','Version','DownloadBaseUrl','Update','Config','ServiceName','NoService','WinSWDownloadBaseUrl','Build','NoElevate')) {
    $parameter=$help.parameters.parameter | Where-Object name -eq $name
    if (-not $parameter.description.Text) { throw "Missing installer help: $name" }
}
$ast=[Management.Automation.Language.Parser]::ParseFile($env:TEST_INSTALLER,[ref]$null,[ref]$null)
foreach ($name in @('Add-BinaryPath','Restore-BinaryPath','Resolve-InstallPath')) {
    $fn=$ast.Find({param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq $name},$true)
    . ([scriptblock]::Create($fn.Extent.Text))
}
try { Resolve-InstallPath 'C:\unsafe;C:\injected'; throw 'Semicolon accepted' } catch { if ($_ -notlike '*Expected an absolute local path*') { throw } }
function Get-PersistentPath($Target) { $script:lastTarget=$Target; return $script:persisted }
function Set-PersistentPath($Value,$Target) {
    if ($script:failWrite) { throw 'write denied' }
    $script:lastTarget=$Target; $script:persisted=$Value; $script:writes++
}
function Send-EnvironmentChange { $script:notifications++ }
$binDir='C:\Program Files\oci-mirror-proxy\bin'
foreach ($NoService in @($true,$false)) {
    $expectedTarget=if($NoService){'User'}else{'Machine'}
    foreach ($before in @($null,'','C:\first','C:\first;','%SystemRoot%\System32;C:\other')) {
        $script:persisted=$before; $script:pathChanged=$false; $script:writes=0; $script:notifications=0
        Add-BinaryPath
        $separator=if($before -and -not $before.EndsWith(';')){';'}else{''}
        if ($persisted -cne ($before+$separator+$binDir) -or $lastTarget -ne $expectedTarget -or $writes -ne 1 -or $notifications -ne 1) { throw 'PATH addition mismatch' }
        Add-BinaryPath
        if ($writes -ne 1) { throw 'Repeated installation duplicated PATH' }
        # A real second installation has its own state. Restore this attempt.
        $script:previousPath=$before
        Restore-BinaryPath
        if ($persisted -cne $before -or $notifications -ne 2) { throw 'PATH rollback mismatch' }
    }
    foreach ($existing in @('"C:\PROGRAM FILES\oci-mirror-proxy\bin\"','%ProgramFiles%\oci-mirror-proxy\bin',' C:\Program Files\oci-mirror-proxy\bin ')) {
        $script:persisted='C:\first;'+$existing+';C:\last'; $script:pathChanged=$false; $script:writes=0
        Add-BinaryPath
        if ($writes -ne 0 -or $pathChanged) { throw 'Equivalent PATH was duplicated' }
    }
    $script:persisted='C:\first'; $script:pathChanged=$false; $script:writes=0
    Add-BinaryPath
    $script:persisted+=';C:\concurrent'
    $concurrent=$persisted
    Restore-BinaryPath
    if ($persisted -cne $concurrent -or $writes -ne 1) { throw 'Rollback overwrote concurrent PATH edit' }
    $script:persisted='C:\first'; $script:pathChanged=$false; $script:failWrite=$true
    try { Add-BinaryPath; throw 'Expected write failure' } catch { if ($_ -notlike '*write denied*') { throw } }
    if ($pathChanged) { throw 'Failed write marked as successful' }
    $script:failWrite=$false
}
`
			path := filepath.Join(root, "run.ps1")
			if err := os.WriteFile(path, []byte(runner), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), binary, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", path)
			for _, env := range os.Environ() {
				if !strings.HasPrefix(strings.ToUpper(env), "PSMODULEPATH=") {
					cmd.Env = append(cmd.Env, env)
				}
			}
			cmd.Env = append(cmd.Env, "TEST_INSTALLER="+installer, "TEMP="+root, "TMP="+root)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("PATH behavior: %v\n%s", err, out)
			}
		})
	}
}

func TestWindowsInstallerUninstallCommand(t *testing.T) {
	for _, shell := range []string{"powershell.exe", "pwsh.exe"} {
		binary, err := exec.LookPath(shell)
		if err != nil {
			continue
		}
		t.Run(shell, func(t *testing.T) {
			root := t.TempDir()
			script := isolatedWindowsInstaller(t, root)
			prefix := filepath.Join(root, "application")
			bin := filepath.Join(prefix, "bin")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(bin, "oci-mirror-proxy.exe")
			config := filepath.Join(prefix, "config.yaml")
			for _, path := range []string{destination, config} {
				if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				cmd := exec.CommandContext(t.Context(), binary, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script, "uninstall", "-NoService", "-Prefix", prefix, "-SkipDocker")
				cmd.Env = append(os.Environ(), "DOWNLOAD_BASE_URL=invalid", "VERSION=invalid/tag")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("uninstall: %v: %s", err, out)
				}
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatalf("binary remains: %v", err)
			}
			if _, err := os.Stat(config); err != nil {
				t.Fatal(err)
			}
		})
	}
}
