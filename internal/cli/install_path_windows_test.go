package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsInstallerPath(t *testing.T) {
	for _, shell := range []string{"powershell.exe", "pwsh.exe"} {
		binary, err := exec.LookPath(shell)
		if err != nil {
			continue
		}
		t.Run(shell, func(t *testing.T) {
			root := t.TempDir()
			installer, err := filepath.Abs(filepath.Join("..", "..", "install.ps1"))
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
