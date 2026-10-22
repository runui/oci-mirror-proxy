package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestWindowsInstallerElevation(t *testing.T) {
	original, err := os.ReadFile(filepath.Join("..", "..", "install.ps1"))
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
				root := t.TempDir()
				installer := filepath.Join(root, "installer with spaces.ps1")
				script := regexp.MustCompile(`(?ms)^function Test-IsAdministrator \{.*?^}`).ReplaceAllString(string(original), `function Test-IsAdministrator { return ($$env:TEST_SCENARIO -eq 'administrator') }`)
				// Stop before querying SCM, downloading or changing any host files.
				script = strings.Replace(script, "$existingService = Get-CimInstance", "throw 'REACHED_INSTALLATION'; $existingService = Get-CimInstance", 1)
				script = strings.Replace(script, "$nativeArch = $env:PROCESSOR_ARCHITEW6432", "throw 'REACHED_INSTALLATION'; $nativeArch = $env:PROCESSOR_ARCHITEW6432", 1)
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
 [IO.File]::WriteAllText($capture, 'param($Prefix,$Config,$Version,$DownloadBaseUrl,$WinSWDownloadBaseUrl,$ServiceName,[switch]$Update,[switch]$NoService,[switch]$Build,[switch]$NoElevate); if ($Prefix -cne $env:TEST_PREFIX -or $DownloadBaseUrl -cne $env:DOWNLOAD_BASE_URL -or -not $Update -or $NoService) { throw "Capture mismatch" }; if ($env:TEST_SCENARIO -eq "child failure") { throw "child failed" }')
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
			script, err := filepath.Abs(filepath.Join("..", "..", "install.ps1"))
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
