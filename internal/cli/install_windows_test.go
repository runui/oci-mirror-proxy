package cli

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
	source, err := os.ReadFile(filepath.Join("..", "..", "install.ps1"))
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
			root := t.TempDir()
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
			work := t.TempDir()
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
			work := t.TempDir()
			source := filepath.Join(work, "source with spaces & unicode 中文")
			if err := os.Mkdir(source, 0o700); err != nil {
				t.Fatal(err)
			}
			script, err := filepath.Abs(filepath.Join("..", "..", "install.ps1"))
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
	root := t.TempDir()
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
 case "server":
  fmt.Println("{\"schema_version\":1}")
 case "dockerd":
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
	original, err := os.ReadFile(filepath.Join("..", "..", "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, shell := range []string{"powershell.exe", "pwsh.exe"} {
		binary, err := exec.LookPath(shell)
		if err != nil {
			continue
		}
		for _, testCase := range []string{"install", "update", "failed install", "failed update", "custom definition", "custom account", "bad wrapper", "build install", "build update", "build failed update"} {
			t.Run(shell+"/"+testCase, func(t *testing.T) {
				scenario := strings.TrimPrefix(testCase, "build ")
				work := t.TempDir()
				prefix := filepath.Join(work, "prefix with spaces & unicode 中文")
				config := filepath.Join(work, "config with spaces", "config.yaml")
				script := isolateWindowsInstallerPaths(string(original))
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
 param($Uri, $OutFile, [switch]$UseBasicParsing)
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
foreach ($name in @('GOCACHE','GOMODCACHE','GOPATH','GOTMPDIR','GOENV','GOWORK','GOFLAGS','CGO_ENABLED','GOOS','GOARCH')) { $buildEnvironment[$name] = [Environment]::GetEnvironmentVariable($name,'Process') }
$env:GOCACHE = Join-Path $env:TEMP 'caller-cache'
$env:GOMODCACHE = Join-Path $env:TEMP 'caller-modules'
$env:GOPATH = Join-Path $env:TEMP 'caller-gopath'
& $env:TEST_INSTALLER @params
if ($env:GOCACHE -cne (Join-Path $env:TEMP 'caller-cache') -or $env:GOMODCACHE -cne (Join-Path $env:TEMP 'caller-modules') -or $env:GOPATH -cne (Join-Path $env:TEMP 'caller-gopath')) { throw 'Build cache environment was not restored' }
foreach ($path in @($env:GOCACHE,$env:GOMODCACHE,$env:GOPATH)) { if (Test-Path -LiteralPath $path) { throw 'Elevated build wrote into caller caches' } }
foreach ($name in $buildEnvironment.Keys) { [Environment]::SetEnvironmentVariable($name,$buildEnvironment[$name],'Process') }
if ($env:TEST_SCENARIO -eq 'install') { return }
$global:fixtureStatus = 'Running'
Set-Service oci-mirror-proxy Manual
if ($env:TEST_SCENARIO -eq 'custom definition') { Add-Content "$env:TEST_PREFIX\service\oci-mirror-proxy.xml" '<!-- custom -->' }
if ($env:TEST_SCENARIO -eq 'bad wrapper') { [IO.File]::WriteAllText("$env:TEST_PREFIX\service\oci-mirror-proxy.exe", 'custom wrapper') }
[IO.File]::WriteAllText("$env:TEST_PREFIX\bin\oci-mirror-proxy.exe", 'previous binary')
[IO.File]::WriteAllText($env:TEST_CONFIG, 'preserved config')
try { & $env:TEST_INSTALLER @params -Update } catch { Write-Output "EXPECTED_FAILURE: $_" }
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
				} else if scenario == "install" {
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
