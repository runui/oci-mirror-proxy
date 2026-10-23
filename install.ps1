#Requires -Version 5.1

<#
.SYNOPSIS
Install, update or uninstall oci-mirror-proxy and its Windows service.
.DESCRIPTION
Downloads and builds run before UAC. Requests administrator approval only for
system publication and service management; NoService never requests UAC.
The default installation uses WinSW v2.12.0 (.NET Framework 4.x required), starts
the service automatically at boot and configures failure recovery. Downloads are
verified with SHA256. Existing configuration is preserved; failed service updates
restore the previous binary. Service installs synchronize subscriptions and
configure the current user's local Docker without restarting it. Docker setup
failures warn with manual recovery commands; they do not undo installation.

The binary directory is added to system PATH for service installs and current-user
PATH with NoService. Existing entries are preserved and duplicate additions are
avoided. Open a new terminal after installation. Standard users can execute the
system-installed binary and use their own configuration, but service management
and service configuration require administrator privileges.
.PARAMETER Command
install (default), update or uninstall. -Update remains supported. Uninstall
preserves configuration, caches and logs. Revert managed Docker integration as
the Docker user first, or explicitly use -SkipDocker to leave it unchanged.
.PARAMETER Prefix
Dedicated application directory. Defaults to %ProgramFiles%\oci-mirror-proxy
for service installs, or %LOCALAPPDATA%\oci-mirror-proxy with NoService. The
executable is placed in its bin subdirectory. Service installs require local,
non-reparse paths. Prefix and the configuration directory must not overlap.
.PARAMETER Version
Release tag, such as v1.0.1. Defaults to VERSION, or latest. Local source builds
use dev when Version is latest; an explicit tag is embedded in the binary.
.PARAMETER DownloadBaseUrl
Repository root URL with /releases and /archive layouts. Defaults to
DOWNLOAD_BASE_URL, or https://git.zmide.com/bin/oci-mirror-proxy. Release archives
must include SHA256SUMS. Not used for a recognized local source build.
.PARAMETER Update
Update an existing installation and restart its service while preserving config.
Use the same Prefix, Config and ServiceName for a custom service installation.
With NoService and no explicit Prefix, update the executable found on PATH.
.PARAMETER Config
Service configuration file. Defaults to %ProgramData%\<ServiceName>\config.yaml.
Use a dedicated directory separate from Prefix; it and its logs are restricted
to SYSTEM and Administrators. Not used with NoService.
.PARAMETER ServiceName
Windows service name. Defaults to oci-mirror-proxy. Also determines the default
configuration directory and the service admin pipe. Custom service definitions
or accounts are not overwritten.
.PARAMETER NoService
Install only the binary, without registering a service or changing service ACLs.
Never requests elevation. Adds bin to the current user's PATH and does not create
configuration; run oci-mirror-proxy init and serve as that user when needed.
.PARAMETER WinSWDownloadBaseUrl
HTTPS repository root for the pinned WinSW release, with the same release layout
as https://github.com/winsw/winsw. Downloaded wrappers must match the pinned hash.
.PARAMETER Build
Compile using Go instead of downloading a prebuilt binary. From this project's
source root, build the current working tree without fetching release metadata;
otherwise download the selected release source archive. Requires Go 1.26.8 or a
compatible auto-downloaded toolchain. Can be combined with Update or NoService.
Download failures do not fall back to a source build unless Build is specified.
Service builds use temporary caches rather than modifying the caller's caches.
.PARAMETER NoElevate
Fail immediately when a service install lacks administrator privileges instead
of requesting UAC approval. Useful for unattended jobs. Canceling a UAC prompt
aborts installation; NoService never prompts, regardless of this setting.
.PARAMETER SkipDocker
Do not configure Docker. NoService always skips Docker. Service updates only
maintain integration that is still managed, so a previous revert is respected.
On uninstall, bypass the managed Docker state check and leave settings unchanged.
.EXAMPLE
.\install.ps1
Install the latest release as an automatic Windows service; approve UAC if asked.
.EXAMPLE
.\install.ps1 update
Update the default installation. The legacy -Update switch is also supported.
.EXAMPLE
.\install.ps1 uninstall
Remove the managed service and executables, preserving configuration and logs.
.EXAMPLE
.\install.ps1 -Update
Update the default installation, preserving its service configuration.
.EXAMPLE
.\install.ps1 -NoService
Install for the current user without a Windows service or an elevation prompt.
.EXAMPLE
.\install.ps1 -Build -Update
Build local source when run from its root, otherwise build the selected release,
then update the installation.
.EXAMPLE
.\install.ps1 -Prefix C:\Apps\oci-mirror-proxy -Config C:\ProxyData\config.yaml -ServiceName my-proxy -Version v1.0.1
Install a selected release with dedicated custom application and data directories.
#>
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('install', 'update', 'uninstall')]
    [string]$Command = 'install',
    [string]$Prefix = (Join-Path $env:ProgramFiles 'oci-mirror-proxy'),
    [string]$Version = $(if ($env:VERSION) { $env:VERSION } else { 'latest' }),
    [string]$DownloadBaseUrl = $(if ($env:DOWNLOAD_BASE_URL) { $env:DOWNLOAD_BASE_URL } else { 'https://git.zmide.com/bin/oci-mirror-proxy' }),
    [switch]$Update,
    [string]$Config = '',
    [string]$ServiceName = 'oci-mirror-proxy',
    [switch]$NoService,
    [string]$WinSWDownloadBaseUrl = 'https://github.com/winsw/winsw',
    [switch]$Build,
    [switch]$NoElevate,
    [switch]$SkipDocker,
    [Parameter(DontShow = $true)]
    [switch]$ServiceStageOnly,
    [Parameter(DontShow = $true)]
    [string]$PreparedBinary,
    [Parameter(DontShow = $true)]
    [string]$PreparedWrapper,
    [Parameter(DontShow = $true)]
    [ValidatePattern('^[a-fA-F0-9]{64}$')]
    [string]$PreparedHash,
    [Parameter(DontShow = $true)]
    [string]$InfoID = ([Guid]::NewGuid().ToString('N'))
)

$ErrorActionPreference = 'Stop'
# Normalize the legacy switch once; all update behavior uses the same path.
if ($Update -and $PSBoundParameters.ContainsKey('Command') -and $Command -ne 'update') { throw '-Update conflicts with the selected command.' }
if ($Update) { $Command = 'update' }
if ($Command -eq 'update') { $Update = $true }
if ($Command -eq 'uninstall') {
    foreach ($key in @('Version', 'DownloadBaseUrl', 'WinSWDownloadBaseUrl', 'Build')) {
        if ($PSBoundParameters.ContainsKey($key)) { throw "-$key is not valid for uninstall." }
    }
}
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
if ($env:OS -ne 'Windows_NT') { throw 'Use install.sh on Unix systems.' }
$repo = $DownloadBaseUrl.TrimEnd('/')
if ($Command -ne 'uninstall') {
    if ($repo -notmatch '^https?://') { throw 'DownloadBaseUrl must be an HTTP or HTTPS repository root URL.' }
    if ($Version -notmatch '^[a-zA-Z0-9][a-zA-Z0-9._+-]*$') { throw 'Invalid release tag.' }
}
if ($ServiceName -notmatch '^[a-zA-Z0-9][a-zA-Z0-9._-]{0,79}$') { throw 'Invalid service name.' }
if ($InfoID -notmatch '^[a-f0-9]{32}$') { throw 'Invalid information handoff ID.' }

function Resolve-InstallPath([string]$Path) {
    # Require a local, fully qualified path, not drive-relative or UNC paths.
    # WinSW expands %variables% in XML, so do not accept them in literal paths.
    if ($Path -notmatch '^[a-zA-Z]:[\\/]' -or $Path -match '["%;\r\n]') {
        throw "Expected an absolute local path without quotes, semicolons or percent signs: $Path"
    }
    return [IO.Path]::GetFullPath($Path)
}

function Test-IsAdministrator {
    $principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Invoke-ElevatedInstaller([string]$ScriptPath, [hashtable]$Parameters, [string]$GoPath) {
    if (-not $ScriptPath -or -not (Test-Path -LiteralPath $ScriptPath -PathType Leaf)) {
        throw 'Save install.ps1 to a local file before requesting administrator approval.'
    }
    # Data, not command-line interpolation: preserve empty strings, Unicode,
    # switch false values and explicit parameter membership across PowerShell editions.
    $values = @{}
    foreach ($key in $Parameters.Keys) {
        $value = $Parameters[$key]
        if ($value -is [Management.Automation.SwitchParameter]) { $value = [bool]$value }
        $values[$key] = $value
    }
    $values['NoElevate'] = $true
    $data = @{ Script = $ScriptPath; Parameters = $values; GoPath = $GoPath; BuildDirectory = $(if ($Parameters['Build']) { (Get-Location).ProviderPath } else { '' }) } | ConvertTo-Json -Depth 5 -Compress
    $payload = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($data))
    $bootstrap = @'
$ErrorActionPreference = 'Stop'
try {
    $data = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('__PAYLOAD__')) | ConvertFrom-Json
    $parameters = @{}
    foreach ($property in $data.Parameters.PSObject.Properties) { $parameters[$property.Name] = $property.Value }
    $principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'Administrator approval did not provide administrator privileges.' }
    # Resolve the elevated identity's temp directory, not an inherited path
    # belonging to the invoking user (UAC may use another administrator account).
    $temp = Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'Temp'
    [IO.Directory]::CreateDirectory($temp) > $null
    $env:TEMP = $temp
    $env:TMP = $temp
    if ($data.GoPath) {
        if (-not (Test-Path -LiteralPath $data.GoPath -PathType Leaf)) { throw 'The selected Go executable is unavailable to the administrator account.' }
        $env:PATH = (Split-Path -Parent $data.GoPath) + [IO.Path]::PathSeparator + $env:PATH
    }
    if ($data.BuildDirectory) { Set-Location -LiteralPath $data.BuildDirectory }
    & $data.Script @parameters
    exit 0
} catch {
    [Console]::Error.WriteLine($_.ToString())
    exit 1
}
'@
    $bootstrap = $bootstrap.Replace('__PAYLOAD__', $payload)
    $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($bootstrap))
    if ($encoded.Length -gt 28000) { throw 'Installation arguments are too long for a Windows elevation command.' }
    $shell = if ($PSVersionTable.PSEdition -eq 'Core') { Join-Path $PSHOME 'pwsh.exe' } else { Join-Path $PSHOME 'powershell.exe' }
    Write-Host 'Requesting administrator approval. Installation continues in a separate window; waiting for its result...'
    try {
        $process = Start-Process -FilePath $shell -Verb RunAs -ArgumentList @('-NoLogo', '-NoProfile', '-EncodedCommand', $encoded) -WorkingDirectory $env:SystemRoot -Wait -PassThru -ErrorAction Stop
        try { return $process.ExitCode } finally { $process.Dispose() }
    } catch {
        $cause = $_.Exception
        while ($cause) {
            if ($cause -is [ComponentModel.Win32Exception] -and $cause.NativeErrorCode -eq 1223) {
                throw 'Administrator approval was canceled. Nothing was installed.'
            }
            $cause = $cause.InnerException
        }
        throw
    }
}

function Assert-NoReparsePoint([string]$Path) {
    while ($Path) {
        if ((Test-Path -LiteralPath $Path) -and ((Get-Item -LiteralPath $Path -Force).Attributes -band [IO.FileAttributes]::ReparsePoint)) {
            throw "Service paths must not contain reparse points: $Path"
        }
        $parent = Split-Path -Parent $Path
        if ($parent -eq $Path) { break }
        $Path = $parent
    }
}

function New-ServiceDefinition {
    # Like the launchd/systemd definitions in install.sh, invoke the existing
    # foreground command. WinSW supplies the SCM lifecycle, logging and Ctrl+C.
    $exe = [Security.SecurityElement]::Escape($destination)
    $arguments = [Security.SecurityElement]::Escape("serve --config `"$Config`" --admin-socket `"$adminPipe`"")
    $workingDir = [Security.SecurityElement]::Escape((Split-Path -Parent $Config))
    $logs = [Security.SecurityElement]::Escape($logDir)
    return @"
<!-- Managed by oci-mirror-proxy install.ps1 -->
<service>
  <id>$ServiceName</id>
  <name>$ServiceName</name>
  <description>OCI registry mirror proxy</description>
  <executable>$exe</executable>
  <arguments>$arguments</arguments>
  <workingdirectory>$workingDir</workingdirectory>
  <startmode>Automatic</startmode>
  <stoptimeout>15 sec</stoptimeout>
  <onfailure action="restart" delay="5 sec"/>
  <resetfailure>1 day</resetfailure>
  <logpath>$logs</logpath>
  <log mode="roll-by-size">
    <sizeThreshold>10240</sizeThreshold>
    <keepFiles>3</keepFiles>
  </log>
</service>
"@
}

function Invoke-InstallerNative([string]$Executable, [string[]]$Arguments) {
    $capture = Join-Path ([IO.Path]::GetTempPath()) ('oci-native-' + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $capture -ErrorAction Stop | Out-Null
    $stdoutFile = Join-Path $capture 'stdout'
    $stderrFile = Join-Path $capture 'stderr'
    $savedConsoleEncoding = [Console]::OutputEncoding
    try {
        # PS 5.1 wraps native stderr in ErrorRecords; only the process exit code
        # determines native failure. File operations retain terminating errors.
        $ErrorActionPreference = 'Continue'
        $PSNativeCommandUseErrorActionPreference = $false
        [Console]::OutputEncoding = New-Object Text.UTF8Encoding($false)
        $LASTEXITCODE = $null
        & $Executable @Arguments 1> $stdoutFile 2> $stderrFile
        $code = $LASTEXITCODE
        $ErrorActionPreference = 'Stop'
        if ($null -eq $code) { throw "Could not start $Executable" }
        $stdout = Get-Content -LiteralPath $stdoutFile -Raw -ErrorAction Stop
        $stderr = Get-Content -LiteralPath $stderrFile -Raw -ErrorAction Stop
        return [pscustomobject]@{ ExitCode = $code; Stdout = $stdout; Stderr = $stderr }
    } finally {
        [Console]::OutputEncoding = $savedConsoleEncoding
        try { Remove-Item -LiteralPath $capture -Recurse -Force -ErrorAction Stop }
        catch { Write-Warning "Could not remove native output directory ${capture}: $_" }
    }
}

function Show-NativeResult($Result) {
    if ($Result.Stdout) { Write-Host $Result.Stdout.TrimEnd() }
    if ($Result.Stderr) { Write-Host $Result.Stderr.TrimEnd() }
}

function Invoke-WinSW([string]$Command) {
    $result = Invoke-InstallerNative $wrapper @($Command)
    Show-NativeResult $result
    if ($result.ExitCode -ne 0) { throw "WinSW $Command failed (exit $($result.ExitCode))." }
}

function Stop-ProxyService {
    $controller = Get-Service -Name $ServiceName -ErrorAction Stop
    if ($controller.Status -ne 'Stopped') {
        Stop-Service -Name $ServiceName -ErrorAction Stop
        $controller.WaitForStatus('Stopped', [TimeSpan]::FromSeconds(45))
    }
    $controller.Dispose()
}

function Test-ProxyReady {
    # Windows PowerShell 5.1 can turn native stderr into an ErrorRecord. A
    # refused connection while starting is a retry, not an installation error.
    try {
        $result = Invoke-InstallerNative $destination @('status', '--admin-socket', $adminPipe)
        return $result.ExitCode -eq 0
    } catch { return $false }
}

function Publish-File([string]$Source, [string]$Destination) {
    if (Test-Path -LiteralPath $Destination) {
        [IO.File]::Replace($Source, $Destination, [System.Management.Automation.Language.NullString]::Value)
    } else {
        [IO.File]::Move($Source, $Destination)
    }
}

function Get-PersistentPath([string]$Target) {
    $hive = if ($Target -eq 'Machine') { [Microsoft.Win32.Registry]::LocalMachine } else { [Microsoft.Win32.Registry]::CurrentUser }
    $keyName = if ($Target -eq 'Machine') { 'SYSTEM\CurrentControlSet\Control\Session Manager\Environment' } else { 'Environment' }
    $key = $hive.OpenSubKey($keyName)
    if (-not $key) { return $null }
    try { return $key.GetValue('Path', $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames) } finally { $key.Dispose() }
}

function Set-PersistentPath([AllowNull()][string]$Value, [string]$Target) {
    $hive = if ($Target -eq 'Machine') { [Microsoft.Win32.Registry]::LocalMachine } else { [Microsoft.Win32.Registry]::CurrentUser }
    $keyName = if ($Target -eq 'Machine') { 'SYSTEM\CurrentControlSet\Control\Session Manager\Environment' } else { 'Environment' }
    $key = $hive.CreateSubKey($keyName)
    try {
        if ($null -eq $Value -or $Value -eq '') { $key.DeleteValue('Path', $false); return }
        $kind = if ($key.GetValueNames() -contains 'Path') { $key.GetValueKind('Path') } else { [Microsoft.Win32.RegistryValueKind]::ExpandString }
        $key.SetValue('Path', $Value, $kind)
    } finally { $key.Dispose() }
}

function Send-EnvironmentChange {
    # Explorer and other interested applications refresh the environment for
    # future processes. Existing terminals cannot have their PATH changed here.
    try {
        if (-not ('OciMirrorProxy.InstallerEnvironment' -as [type])) {
            Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
namespace OciMirrorProxy {
    public static class InstallerEnvironment {
        [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        public static extern IntPtr SendMessageTimeout(IntPtr window, uint message, UIntPtr wParam, string lParam, uint flags, uint timeout, out UIntPtr result);
    }
}
'@
        }
        $result = [UIntPtr]::Zero
        $sent = [OciMirrorProxy.InstallerEnvironment]::SendMessageTimeout([IntPtr]0xffff, 0x001a, [UIntPtr]::Zero, 'Environment', 2, 5000, [ref]$result)
        if ($sent -eq [IntPtr]::Zero) { Write-Warning 'PATH was saved, but the environment notification timed out. Sign out and back in if new terminals do not see it.' }
    } catch { Write-Warning "PATH was saved, but its environment notification failed: $_" }
}

function Add-BinaryPath {
    $script:pathTarget = if ($NoService) { 'User' } else { 'Machine' }
    $script:previousPath = Get-PersistentPath $pathTarget
    # Expand only for comparison. Keep existing entries and their order intact.
    $present = @($previousPath -split ';' | Where-Object {
        [Environment]::ExpandEnvironmentVariables($_.Trim().Trim('"')).TrimEnd('\') -ieq $binDir.TrimEnd('\')
    }).Count -gt 0
    if ($present) { return }
    $separator = if ($previousPath -and -not $previousPath.EndsWith(';')) { ';' } else { '' }
    $script:installedPath = $previousPath + $separator + $binDir
    Set-PersistentPath $installedPath $pathTarget
    $script:pathChanged = $true
    Send-EnvironmentChange
}

function Restore-BinaryPath {
    if ($pathChanged) {
        # Never overwrite another process's concurrent environment edits.
        if ((Get-PersistentPath $pathTarget) -ceq $installedPath) {
            Set-PersistentPath $previousPath $pathTarget
            Send-EnvironmentChange
        } else { Write-Warning 'PATH changed concurrently; leaving it untouched during rollback.' }
    }
}

function Protect-ServicePath([string]$Path, [switch]$Directory, [switch]$ReadUsers) {
    $acl = if ($Directory) { New-Object Security.AccessControl.DirectorySecurity } else { New-Object Security.AccessControl.FileSecurity }
    $acl.SetAccessRuleProtection($true, $false)
    foreach ($sid in @('S-1-5-18', 'S-1-5-32-544', 'S-1-5-32-545')) {
        if ($sid -eq 'S-1-5-32-545' -and -not $ReadUsers) { continue }
        $rights = if ($sid -eq 'S-1-5-32-545') { 'ReadAndExecute' } else { 'FullControl' }
        $identity = New-Object Security.Principal.SecurityIdentifier($sid)
        $rule = if ($Directory) {
            New-Object Security.AccessControl.FileSystemAccessRule($identity, $rights, 'ContainerInherit,ObjectInherit', 'None', 'Allow')
        } else {
            New-Object Security.AccessControl.FileSystemAccessRule($identity, $rights, 'Allow')
        }
        $acl.AddAccessRule($rule)
    }
    Set-Acl -LiteralPath $Path -AclObject $acl
}

function Prepare-Installation {
    $sourceDir = $null
    if ($Build) {
        $currentDir = (Get-Location).ProviderPath
        $moduleFile = Join-Path $currentDir 'go.mod'
        if ((Test-Path -LiteralPath $moduleFile -PathType Leaf) -and
            (Test-Path -LiteralPath (Join-Path $currentDir 'cmd/oci-mirror-proxy') -PathType Container) -and
            ([IO.File]::ReadAllText($moduleFile) -match '(?m)^module\s+github\.com/runui/oci-mirror-proxy\s*\r?$')) {
            $sourceDir = $currentDir
            # Local source is not necessarily a published release. Do not resolve
            # latest over the network or label the working tree as that release.
            if ($Version -eq 'latest') { $Version = 'dev' }
        }
    }
    if ($Version -eq 'latest') {
        $response = Invoke-WebRequest -UseBasicParsing -Uri "$repo/releases/latest"
        $uri = $response.BaseResponse.ResponseUri
        if (-not $uri) { $uri = $response.BaseResponse.RequestMessage.RequestUri }
        if ($uri.AbsolutePath -notmatch '/releases/tag/([a-zA-Z0-9][a-zA-Z0-9._+-]*)/?$') {
            throw 'Cannot resolve latest release; specify -Version explicitly.'
        }
        $Version = $Matches[1]
    }
    if ($Build) {
        $go = Get-Command go -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
        if (-not $go) { throw '-Build requires Go; install Go to compile from source.' }
        if ($sourceDir) {
            Write-Host "Building local source from $sourceDir ($Version) for windows/$arch..."
        } else {
            Write-Host "Building release $Version for windows/$arch..."
            $archive = Join-Path $tempDir 'source.zip'
            Invoke-WebRequest -UseBasicParsing -Uri "$repo/archive/$Version.zip" -OutFile $archive
            $extracted = Join-Path $tempDir 'source'
            Expand-Archive -LiteralPath $archive -DestinationPath $extracted
            $roots = @(Get-ChildItem -LiteralPath $extracted -Force)
            if ($roots.Count -ne 1 -or -not $roots[0].PSIsContainer -or -not (Test-Path -LiteralPath (Join-Path $roots[0].FullName 'go.mod') -PathType Leaf)) {
                throw 'Unexpected source archive layout.'
            }
            $sourceDir = $roots[0].FullName
        }
        $binary = Join-Path $tempDir 'oci-mirror-proxy.exe'
        $savedBuildEnv = @{}
        $buildEnvNames = @('CGO_ENABLED', 'GOOS', 'GOARCH')
        if (-not $NoService) { $buildEnvNames += @('GOCACHE', 'GOMODCACHE', 'GOPATH', 'GOTMPDIR', 'GOENV', 'GOWORK', 'GOFLAGS') }
        foreach ($name in $buildEnvNames) {
            $savedBuildEnv[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
        }
        Push-Location -LiteralPath $sourceDir
        try {
            $env:CGO_ENABLED = '0'
            $env:GOOS = 'windows'
            $env:GOARCH = $arch
            if (-not $NoService) {
                # Service builds use isolated caches whether launched normally
                # or from an existing Administrator shell; restore caller env.
                $env:GOCACHE = Join-Path $tempDir 'gocache'
                $env:GOPATH = Join-Path $tempDir 'gopath'
                $env:GOMODCACHE = Join-Path $env:GOPATH 'pkg\mod'
                $env:GOTMPDIR = $tempDir
                $env:GOENV = 'off'
                $env:GOWORK = 'off'
                $env:GOFLAGS = ''
            }
            $result = Invoke-InstallerNative $go.Source @('build', '-trimpath', '-ldflags', "-s -w -X github.com/runui/oci-mirror-proxy/internal/cli.version=$Version", '-o', $binary, './cmd/oci-mirror-proxy')
            Show-NativeResult $result
            if ($result.ExitCode -ne 0) { throw "Source build failed (exit $($result.ExitCode))." }
            if (-not (Test-Path -LiteralPath $binary -PathType Leaf)) { throw 'Source build did not produce the executable.' }
        } finally {
            Pop-Location
            foreach ($name in $savedBuildEnv.Keys) {
                if ($null -eq $savedBuildEnv[$name]) {
                    Remove-Item -LiteralPath "Env:$name" -ErrorAction SilentlyContinue
                } else {
                    [Environment]::SetEnvironmentVariable($name, $savedBuildEnv[$name], 'Process')
                }
            }
        }
    } else {
        $asset = "oci-mirror-proxy_${Version}_windows_${arch}.zip"
        $archive = Join-Path $tempDir $asset
        Write-Host "Downloading $asset..."
        Invoke-WebRequest -UseBasicParsing -Uri "$repo/releases/download/$Version/$asset" -OutFile $archive
        $checksums = Join-Path $tempDir 'SHA256SUMS'
        Invoke-WebRequest -UseBasicParsing -Uri "$repo/releases/download/$Version/SHA256SUMS" -OutFile $checksums
        $pattern = '^([a-fA-F0-9]{64})\s+\*?(?:\./)?' + [regex]::Escape($asset) + '$'
        $expected = @(Get-Content -LiteralPath $checksums | ForEach-Object { if ($_ -match $pattern) { $Matches[1] } })
        if ($expected.Count -ne 1) { throw "Missing or invalid checksum for $asset." }
        if ((Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash -ne $expected[0]) {
            throw 'SHA256 verification failed; installation aborted.'
        }
        $extracted = Join-Path $tempDir 'extracted'
        Expand-Archive -LiteralPath $archive -DestinationPath $extracted
        $binary = Join-Path $extracted 'oci-mirror-proxy.exe'
        if (-not (Test-Path -LiteralPath $binary -PathType Leaf)) { throw 'Release archive is missing the executable.' }
    }
    if (-not $NoService) {
        $wrapperDownload = Join-Path $tempDir 'WinSW.exe'
        Invoke-WebRequest -UseBasicParsing -Uri "$WinSWDownloadBaseUrl/releases/download/v2.12.0/WinSW.NET4.exe" -OutFile $wrapperDownload
        if ((Get-FileHash -LiteralPath $wrapperDownload -Algorithm SHA256).Hash -ne $wrapperHash) { throw 'WinSW SHA256 verification failed.' }
    }
}

function Write-DockerRecovery([string]$Reason) {
    Write-Warning "Proxy installation succeeded, but Docker integration was not completed: $Reason. Docker was not restarted."
    # Literal quoting keeps custom installation paths usable in pasted commands.
    $exe = $destination.Replace("'", "''")
    $configuration = $Config.Replace("'", "''")
    Write-Host "Run in an Administrator PowerShell:"
    Write-Host "  `$info = & '$exe' server info --config '$configuration' --json"
    Write-Host "  if (`$LASTEXITCODE -ne 0) { throw 'Could not query server information.' }"
    Write-Host '  [IO.File]::WriteAllText((Join-Path $PWD ''server-info.json''), ($info -join "`n"), (New-Object Text.UTF8Encoding($false)))'
    Write-Host 'Copy server-info.json to the Docker user, then run as that user (not as Administrator):'
    Write-Host "  & '$exe' dockerd apply --server-info server-info.json --verify"
    Write-Host '  Remove-Item -LiteralPath server-info.json'
}

function Connect-UserDocker {
    if ($SkipDocker -or $NoService) { return }
    $targetFile = Join-Path $binDir "server-info-$InfoID.json"
    if (-not (Test-Path -LiteralPath $targetFile -PathType Leaf)) {
        Write-Host 'Docker integration skipped: server information was not exported; use the recovery instructions from the service stage.'
        return
    }
    $arguments = @('dockerd', 'apply', '--server-info', $targetFile, '--verify')
    if ($Update) { $arguments += '--if-managed' }
    try {
        $result = Invoke-InstallerNative $destination $arguments
        Show-NativeResult $result
        if ($result.ExitCode -ne 0) {
            throw "dockerd apply exited with code $($result.ExitCode)"
        }
    } catch {
        Write-DockerRecovery $_.ToString()
        $global:LASTEXITCODE = 0
    } finally { Remove-Item -LiteralPath $targetFile -Force -ErrorAction SilentlyContinue }
}

function Assert-DockerUninstallSafe {
    if ($Command -ne 'uninstall' -or $ServiceStageOnly) { return }
    if ($SkipDocker) {
        Write-Warning 'Docker recovery skipped; settings may still reference this proxy.'
        return
    }
    # State has no installation identity. Check in the original user's process,
    # before UAC can select a different account; never revert another proxy.
    foreach ($state in @(
        (Join-Path $env:APPDATA 'oci-mirror-proxy\desktop-state\dockerd-state.json'),
        (Join-Path $env:APPDATA 'oci-mirror-proxy\engine-state\dockerd-state.json')
    )) {
        if (Test-Path -LiteralPath $state) {
            throw 'Managed Docker state exists; run oci-mirror-proxy dockerd revert as the Docker user first, then retry (or explicitly use -SkipDocker).'
        }
    }
}

function Remove-Installation {
    # Only enumerated managed files are removed. Private data and unrelated
    # files survive, even when Prefix is a shared installation directory.
    if (-not $NoService) {
        if ($existingService) {
            if (-not (Test-Path -LiteralPath $wrapper -PathType Leaf)) { throw 'Service wrapper is missing; manage the service manually before removing files.' }
            Stop-ProxyService
            Invoke-WinSW 'uninstall'
            $removed = $false
            for ($i = 0; $i -lt 30; $i++) {
                if (-not (Get-CimInstance Win32_Service -Filter "Name='$ServiceName'")) { $removed = $true; break }
                Start-Sleep -Milliseconds 500
            }
            if (-not $removed) { throw 'Service deletion is pending; close service management windows and retry. Files were preserved.' }
        }
        foreach ($path in @($wrapper, $serviceFile)) {
            if (Test-Path -LiteralPath $path) { Remove-Item -LiteralPath $path -Force }
        }
    }
    if (Test-Path -LiteralPath $destination) { Remove-Item -LiteralPath $destination -Force }
    # Historical installers did not persist PATH ownership. Leave shared or
    # nonempty directories on PATH rather than remove access to other tools.
    if ((Test-Path -LiteralPath $binDir) -and @(Get-ChildItem -LiteralPath $binDir -Force).Count -eq 0) {
        $scope = if ($NoService) { 'User' } else { 'Machine' }
        $before = Get-PersistentPath $scope
        $entries = @($before -split ';' | Where-Object {
            [Environment]::ExpandEnvironmentVariables($_.Trim().Trim('"')).TrimEnd('\') -ine $binDir.TrimEnd('\')
        })
        $after = $entries -join ';'
        if ($after -cne $before) {
            if ((Get-PersistentPath $scope) -cne $before) { throw 'PATH changed concurrently; files removed but PATH was preserved.' }
            Set-PersistentPath $after $scope
            Send-EnvironmentChange
        }
    } else { Write-Warning 'PATH preserved because the binary directory is nonempty or missing.' }
    Write-Host "Uninstalled $destination. Configuration, caches and logs preserved. Docker was not restarted."
}

if ($NoService -and -not $PSBoundParameters.ContainsKey('Prefix')) {
    $Prefix = Join-Path $env:LOCALAPPDATA 'oci-mirror-proxy'
}
$Prefix = (Resolve-InstallPath $Prefix).TrimEnd('\')
$binDir = Join-Path $Prefix 'bin'
if ($NoService -and ($Update -or $Command -eq 'uninstall') -and -not $PSBoundParameters.ContainsKey('Prefix')) {
    $existing = Get-Command oci-mirror-proxy.exe -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($existing) { $binDir = Split-Path -Parent $existing.Source }
    elseif ($Update) { throw 'No installed binary found in PATH; specify -Prefix.' }
}
$destination = Join-Path $binDir 'oci-mirror-proxy.exe'
if ((Test-Path -LiteralPath $destination) -and ((Get-Item -LiteralPath $destination).Attributes -band [IO.FileAttributes]::ReparsePoint)) {
    throw "Refusing to replace symlink $destination."
}

$wrapperHash = '923111C7142B3DC783A3C722B19B8A21BCB78222D7A136AC33F0CA8A29F4CB66'
$existingService = $null
if ($Command -eq 'uninstall') {
    Assert-NoReparsePoint $destination
    if ((Test-Path -LiteralPath $destination) -and -not (Test-Path -LiteralPath $destination -PathType Leaf)) { throw 'Installed binary is not a regular file.' }
    Assert-DockerUninstallSafe
    if ($NoService) { Remove-Installation; exit 0 }
}
if (-not $NoService) {
    if (-not $Config) { $Config = Join-Path (Join-Path $env:ProgramData $ServiceName) 'config.yaml' }
    $Config = Resolve-InstallPath $Config
    $configDir = Split-Path -Parent $Config
    # Separate public executable traversal from private service data, including
    # custom installs. Overlap would either hide bin or expose service data.
    if ($configDir -ieq $Prefix -or $configDir.StartsWith($Prefix + '\', [StringComparison]::OrdinalIgnoreCase) -or $Prefix.StartsWith($configDir.TrimEnd('\') + '\', [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Prefix and Config must use separate, non-overlapping application directories.'
    }
    if (-not (Test-IsAdministrator)) {
        if ($NoElevate) { throw 'Service installation requires Administrator privileges; -NoElevate disables UAC. Run as Administrator or use -NoService.' }
        $elevationParameters = @{}
        foreach ($key in $PSBoundParameters.Keys) { $elevationParameters[$key] = $PSBoundParameters[$key] }
        $elevationParameters['Command'] = $Command
        # Freeze environment-derived defaults before a different administrator
        # account is selected. NoService never takes this branch.
        foreach ($key in @('Prefix', 'Config', 'Version', 'DownloadBaseUrl', 'WinSWDownloadBaseUrl', 'ServiceName')) {
            if ($Command -eq 'uninstall' -and $key -in @('Version', 'DownloadBaseUrl', 'WinSWDownloadBaseUrl')) { continue }
            $elevationParameters[$key] = Get-Variable -Name $key -ValueOnly
        }
        # UAC may select another administrator. Only the original process may
        # enroll the invoking user's Desktop; the elevated child owns the service.
        $elevationParameters['ServiceStageOnly'] = $true
        $elevationParameters['InfoID'] = $InfoID
        $goPath = ''
        if ($Build) {
            $go = Get-Command go -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
            if (-not $go) { throw '-Build requires Go; install Go to compile from source.' }
            $goPath = $go.Source
        }
        $prepareDir = $null
        try {
            if ($Command -ne 'uninstall') {
                # Network and compiler operations do not require UAC. Only the
                # validated snapshot is passed to the system publication stage.
                $tempDir = Join-Path ([IO.Path]::GetTempPath()) ('oci-mirror-proxy-install-' + [Guid]::NewGuid().ToString('N'))
                $prepareDir = $tempDir
                New-Item -ItemType Directory -Path $tempDir | Out-Null
                $nativeArch = $env:PROCESSOR_ARCHITEW6432
                if (-not $nativeArch) { $nativeArch = $env:PROCESSOR_ARCHITECTURE }
                $arch = switch ($nativeArch) { 'AMD64' { 'amd64' } 'ARM64' { 'arm64' } default { throw "Unsupported Windows architecture: $nativeArch" } }
                . Prepare-Installation
                $elevationParameters['Version'] = $Version
                $elevationParameters['PreparedBinary'] = $binary
                $elevationParameters['PreparedWrapper'] = $wrapperDownload
                $elevationParameters['PreparedHash'] = (Get-FileHash -LiteralPath $binary -Algorithm SHA256).Hash
            }
            $code = Invoke-ElevatedInstaller $PSCommandPath $elevationParameters $goPath
            if ($code -ne 0) { Write-Host "Elevated installation failed (exit $code)." }
            if ($code -eq 0 -and $Command -ne 'uninstall') { Connect-UserDocker }
        } finally {
            if ($prepareDir) {
                try { Remove-Item -LiteralPath $prepareDir -Recurse -Force }
                catch { Write-Warning "Could not remove preparation directory ${prepareDir}: $_" }
            }
        }
        exit $code
    }
    # Never replace ACLs on shared system directories or a drive root.
    foreach ($dir in @($Prefix, $configDir)) {
        if ($dir.TrimEnd('\') -in @([IO.Path]::GetPathRoot($dir).TrimEnd('\'), $env:ProgramFiles, ${env:ProgramFiles(x86)}, $env:ProgramData, $env:SystemRoot, $env:USERPROFILE)) {
            throw 'Prefix and Config must use dedicated application directories.'
        }
    }
    $serviceDir = Join-Path $Prefix 'service'
    $wrapper = Join-Path $serviceDir "$ServiceName.exe"
    $serviceFile = Join-Path $serviceDir "$ServiceName.xml"
    $logDir = Join-Path $configDir 'logs'
    $adminPipe = "\\.\pipe\$ServiceName-admin"
    $definition = New-ServiceDefinition
    $serviceCommand = '"' + $wrapper + '"'
    foreach ($path in @($destination, $wrapper, $serviceFile, $Config, $logDir)) { Assert-NoReparsePoint $path }
    if ($Command -eq 'uninstall') {
        foreach ($path in @($wrapper, $serviceFile)) {
            if ((Test-Path -LiteralPath $path) -and -not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Expected a managed file: $path" }
        }
    }
    $existingService = Get-CimInstance Win32_Service -Filter "Name='$ServiceName'"
    if ($existingService -and ($existingService.PathName -cne $serviceCommand -or $existingService.StartName -notin @('LocalSystem', 'NT AUTHORITY\SYSTEM'))) {
        throw "Service $ServiceName is not managed by this installation; use its original -Prefix or choose another -ServiceName."
    }
    if ($existingService -and -not (Test-Path -LiteralPath $serviceFile)) { throw 'Existing service has no managed definition.' }
    if ((Test-Path -LiteralPath $serviceFile) -and [IO.File]::ReadAllText($serviceFile) -cne $definition) {
        throw "$serviceFile differs from the generated definition; manage this service manually."
    }
    if ((Test-Path -LiteralPath $wrapper) -and -not (Test-Path -LiteralPath $serviceFile)) { throw 'Existing wrapper has no managed definition.' }
    $WinSWDownloadBaseUrl = $WinSWDownloadBaseUrl.TrimEnd('/')
    if ($WinSWDownloadBaseUrl -notmatch '^https://') { throw 'WinSWDownloadBaseUrl must use HTTPS.' }
    # AnyCPU .NET Framework build works on both amd64 and Windows ARM64.
    if ((Test-Path -LiteralPath $wrapper) -and (Get-FileHash -LiteralPath $wrapper -Algorithm SHA256).Hash -ne $wrapperHash) {
        throw 'Existing WinSW executable differs from the pinned version; refusing to overwrite it.'
    }
}

if ($Command -eq 'uninstall') { Remove-Installation; exit 0 }

$nativeArch = $env:PROCESSOR_ARCHITEW6432
if (-not $nativeArch) { $nativeArch = $env:PROCESSOR_ARCHITECTURE }
$arch = switch ($nativeArch) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    default { throw "Unsupported Windows architecture: $nativeArch" }
}
$tempDir = Join-Path ([IO.Path]::GetTempPath()) ('oci-mirror-proxy-install-' + [Guid]::NewGuid().ToString('N'))
$staged = $null
$published = $false
$serviceCreated = $false
$serviceInstallAttempted = $false
$serviceTouched = $false
$wrapperCreated = $false
$definitionCreated = $false
$configCreated = $false
$hadBinary = Test-Path -LiteralPath $destination
$backup = Join-Path $tempDir 'previous.exe'
$savedACLs = @{}
$createdDirs = @()
$pathChanged = $false
New-Item -ItemType Directory -Path $tempDir | Out-Null
try {
    if ($PreparedBinary) {
        if (-not $ServiceStageOnly -or -not (Test-IsAdministrator) -or -not $PreparedHash) { throw 'Prepared artifacts require the elevated service stage and a checksum.' }
        # Copy first, then check the private snapshot. Never publish directly from
        # a caller-writable handoff or trust its path after checking its hash.
        Assert-NoReparsePoint $PreparedBinary
        $binary = Join-Path $tempDir 'oci-mirror-proxy.exe'
        Copy-Item -LiteralPath $PreparedBinary -Destination $binary
        if ((Get-FileHash -LiteralPath $binary -Algorithm SHA256).Hash -ne $PreparedHash) { throw 'Prepared binary changed during elevation.' }
        Assert-NoReparsePoint $PreparedWrapper
        $wrapperDownload = Join-Path $tempDir 'WinSW.exe'
        Copy-Item -LiteralPath $PreparedWrapper -Destination $wrapperDownload
        if ((Get-FileHash -LiteralPath $wrapperDownload -Algorithm SHA256).Hash -ne $wrapperHash) { throw 'WinSW SHA256 verification failed.' }
    } else { . Prepare-Installation }
    if (-not $NoService) {
        foreach ($dir in @($Prefix, $binDir, $serviceDir, $configDir, $logDir)) {
            if (-not (Test-Path -LiteralPath $dir)) {
                New-Item -ItemType Directory -Path $dir -Force | Out-Null
                $createdDirs += $dir
            } else { $savedACLs[$dir] = Get-Acl -LiteralPath $dir }
            Protect-ServicePath $dir -Directory -ReadUsers:($dir -in @($Prefix, $binDir))
        }
        if (Test-Path -LiteralPath $Config) {
            $savedACLs[$Config] = Get-Acl -LiteralPath $Config
            Protect-ServicePath $Config
        }
        foreach ($path in @($wrapper, $serviceFile)) {
            if (Test-Path -LiteralPath $path) {
                $savedACLs[$path] = Get-Acl -LiteralPath $path
                Protect-ServicePath $path
            }
        }
    } else { New-Item -ItemType Directory -Path $binDir -Force | Out-Null }
    $staged = Join-Path $binDir ('.oci-mirror-proxy-' + [Guid]::NewGuid().ToString('N') + '.exe')
    Copy-Item -LiteralPath $binary -Destination $staged
    if (-not $NoService) {
        Protect-ServicePath $staged -ReadUsers
        if (-not (Test-Path -LiteralPath $Config)) {
            $configCreated = $true
            $result = Invoke-InstallerNative $staged @('init', '--config', $Config, '--admin-socket', $adminPipe)
            Show-NativeResult $result
            if ($result.ExitCode -ne 0) { throw 'Configuration initialization failed.' }
            # Change only the fresh Docker Hub adaptive setting. Existing user
            # configuration, other registries and race settings remain untouched.
            $fresh = [IO.File]::ReadAllText($Config)
            $pattern = '(?ms)(^  docker\.io:\r?\n(?:(?!^  \S).)*?^    adaptive:\r?\n(?:(?!^    \S).)*?^      enabled:) false'
            $updated = [regex]::Replace($fresh, $pattern, '${1} true')
            if ($updated -ceq $fresh) { throw 'Fresh Docker Hub adaptive setting was not found.' }
            [IO.File]::WriteAllText($Config, $updated, (New-Object Text.UTF8Encoding($false)))
        }
        if ($existingService) {
            $wasRunning = (Get-Service -Name $ServiceName).Status -ne 'Stopped'
            $serviceTouched = $true
            Stop-ProxyService
        }
    }
    if ($hadBinary) {
        Copy-Item -LiteralPath $destination -Destination $backup
        if (-not $NoService) { $savedACLs[$destination] = Get-Acl -LiteralPath $destination }
    }
    Publish-File $staged $destination
    $staged = $null
    $published = $true
    if ($NoService) {
        Add-BinaryPath
        Write-Host "Installed $destination ($Version). $binDir is in user PATH; open a new terminal to use oci-mirror-proxy."
    } else {
        Protect-ServicePath $destination -ReadUsers
        if (-not (Test-Path -LiteralPath $wrapper)) {
            Copy-Item -LiteralPath $wrapperDownload -Destination $wrapper
            $wrapperCreated = $true
            Protect-ServicePath $wrapper
        }
        if (-not (Test-Path -LiteralPath $serviceFile)) {
            [IO.File]::WriteAllText($serviceFile, $definition, (New-Object Text.UTF8Encoding($false)))
            $definitionCreated = $true
            Protect-ServicePath $serviceFile
        }
        if (-not $existingService) {
            $serviceInstallAttempted = $true
            Invoke-WinSW 'install'
            $serviceCreated = $true
        }
        Set-Service -Name $ServiceName -StartupType Automatic
        Start-Service -Name $ServiceName
        $ready = $false
        for ($i = 0; $i -lt 30; $i++) {
            if ((Test-ProxyReady) -and (Get-Service -Name $ServiceName).Status -eq 'Running') { $ready = $true; break }
            Start-Sleep -Milliseconds 500
        }
        if (-not $ready) { throw "Service did not become ready. Check $logDir" }
        try {
            $result = Invoke-InstallerNative $destination @('remote', 'update', '--config', $Config, '--admin-socket', $adminPipe)
            Show-NativeResult $result
            if ($result.ExitCode -ne 0) { throw "exit $($result.ExitCode)" }
        } catch { Write-Warning "Remote subscription synchronization failed; retry remote update as Administrator: $_" }
        # Publish only non-secret renderer inputs in the administrator-owned bin
        # directory. Never write elevated output into the caller's temp directory
        # or expose the private service configuration to the Desktop user.
        if (-not $SkipDocker) {
          try {
            $result = Invoke-InstallerNative $destination @('server', 'info', '--config', $Config, '--admin-socket', $adminPipe, '--json')
            if ($result.Stderr) { Write-Host $result.Stderr.TrimEnd() }
            if ($result.ExitCode -ne 0) { throw 'Could not query server information.' }
            $info = $result.Stdout | ConvertFrom-Json -ErrorAction Stop
            if ($info.schema_version -ne 1 -or -not $info.config.proxy_url -or -not $info.config.registries) { throw 'Invalid server information.' }
            $targetFile = Join-Path $binDir "server-info-$InfoID.json"
            Assert-NoReparsePoint $targetFile
            [IO.File]::WriteAllText($targetFile, $result.Stdout, (New-Object Text.UTF8Encoding($false)))
            Protect-ServicePath $targetFile -ReadUsers
            # Readers may delete this one-use handoff, but cannot change it or
            # create a replacement in the administrator-owned parent directory.
            $acl = Get-Acl -LiteralPath $targetFile
            $users = New-Object Security.Principal.SecurityIdentifier('S-1-5-32-545')
            $acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($users, 'Delete', 'Allow')))
            Set-Acl -LiteralPath $targetFile -AclObject $acl
        } catch {
            # Optional handoff creation must not enter installation rollback.
            if ($targetFile) { Remove-Item -LiteralPath $targetFile -Force -ErrorAction SilentlyContinue }
            Write-DockerRecovery $_.ToString()
            $global:LASTEXITCODE = 0
          }
        }
        Add-BinaryPath
        Write-Host "Installed $destination ($Version). Service $ServiceName is running and enabled at boot."
        Write-Host "Configuration: $Config"
        Write-Host "Logs: $logDir"
        Write-Host "User CLI (no elevation required): & '$destination' version"
        Write-Host 'The binary directory is in system PATH. Open a new terminal to use oci-mirror-proxy as a normal user.'
        Write-Host 'User configuration remains separate; use init and serve as your normal user. Only service administration requires elevation.'
        Write-Host "Manage: Get-Service $ServiceName; Restart-Service $ServiceName; Stop-Service $ServiceName"
        Write-Host "Admin CLI: & '$destination' status --config '$Config' --admin-socket '$adminPipe'"
    }
} catch {
    $failure = $_
    if ($targetFile -and (Test-Path -LiteralPath $targetFile)) { Remove-Item -LiteralPath $targetFile -Force -ErrorAction SilentlyContinue }
    try { Restore-BinaryPath } catch { Write-Warning "PATH rollback failed: $_" }
    if (-not $NoService) {
        try {
            if ($serviceInstallAttempted -and -not $serviceCreated) {
                $partial = Get-CimInstance Win32_Service -Filter "Name='$ServiceName'"
                $serviceCreated = $partial -and $partial.PathName -ceq $serviceCommand
            }
            if ($serviceCreated -or ($existingService -and $serviceTouched)) { Stop-ProxyService }
            if ($serviceCreated) { Invoke-WinSW 'uninstall' }
            if ($published) {
                if ($hadBinary) {
                    $restore = Join-Path $binDir ('.oci-mirror-proxy-' + [Guid]::NewGuid().ToString('N') + '.exe')
                    Copy-Item -LiteralPath $backup -Destination $restore
                    Publish-File $restore $destination
                } else { Remove-Item -LiteralPath $destination -Force }
            }
            if ($wrapperCreated) { Remove-Item -LiteralPath $wrapper -Force }
            if ($definitionCreated) { Remove-Item -LiteralPath $serviceFile -Force }
            if ($configCreated) { Remove-Item -LiteralPath $Config -Force }
            foreach ($path in $savedACLs.Keys) { Set-Acl -LiteralPath $path -AclObject $savedACLs[$path] }
            if ($existingService -and $serviceTouched) {
                $startType = switch ($existingService.StartMode) { 'Auto' { 'Automatic' } 'Disabled' { 'Disabled' } default { 'Manual' } }
                Set-Service -Name $ServiceName -StartupType $startType
                if ($wasRunning) { Start-Service -Name $ServiceName }
            }
            # Only remove empty directories created by this attempt; keep logs.
            [array]::Reverse($createdDirs)
            foreach ($dir in $createdDirs) {
                if ((Test-Path -LiteralPath $dir) -and @(Get-ChildItem -LiteralPath $dir -Force).Count -eq 0) { Remove-Item -LiteralPath $dir -Force }
            }
        } catch { Write-Warning "Rollback failed: $_. Original failure: $failure" }
    } elseif ($published) {
        try {
            if ($hadBinary) {
                $restore = Join-Path $binDir ('.oci-mirror-proxy-' + [Guid]::NewGuid().ToString('N') + '.exe')
                Copy-Item -LiteralPath $backup -Destination $restore
                Publish-File $restore $destination
            } else { Remove-Item -LiteralPath $destination -Force }
        } catch { Write-Warning "Binary rollback failed: $_. Original failure: $failure" }
    }
    throw $failure
} finally {
    # Preserve the original transaction result if temporary cleanup fails.
    if ($staged -and (Test-Path -LiteralPath $staged)) {
        try { Remove-Item -LiteralPath $staged -Force }
        catch { Write-Warning "Could not remove staged executable ${staged}: $_" }
    }
    try { Remove-Item -LiteralPath $tempDir -Recurse -Force }
    catch { Write-Warning "Could not remove temporary directory ${tempDir}: $_" }
}

# Enrollment is outside the service transaction. Docker conflicts must not roll
# back a healthy proxy, and Desktop is never restarted without explicit consent.
if (-not $ServiceStageOnly) { Connect-UserDocker }
