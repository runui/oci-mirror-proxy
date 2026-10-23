#Requires -Version 5.1
# Keep installation checks in this directory; extend these platform suites
# rather than adding separate scripts for individual features or scenarios.
# Isolated SCM integration check for elevated Windows CI runners.
$ErrorActionPreference = 'Stop'
# A local non-elevated pass is not a substitute for this integration suite.
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'SCM integration checks require an elevated Windows runner.' }
Write-Host "SCM runner: $([Environment]::OSVersion.VersionString); PowerShell $($PSVersionTable.PSVersion)"
foreach ($tool in @('powershell.exe','pwsh.exe','python','go')) {
    Get-Command $tool -CommandType Application -ErrorAction Stop | Select-Object Name,Source | Out-Host
}
$repo = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..\..'))
$id = 'oci-mirror-proxy-test-' + [Guid]::NewGuid().ToString('N')
$root = Join-Path $env:ProgramFiles $id
$data = Join-Path $env:ProgramData $id
$fixture = Join-Path ([IO.Path]::GetTempPath()) $id
$server = $null
$oldPath = $env:PATH
$installerAst = [Management.Automation.Language.Parser]::ParseFile((Join-Path $repo 'install.ps1'), [ref]$null, [ref]$null)
foreach ($name in @('Get-PersistentPath','Set-PersistentPath','Send-EnvironmentChange')) {
    $fn = $installerAst.Find({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq $name }, $true)
    . ([scriptblock]::Create($fn.Extent.Text))
}
$initialMachinePath = Get-PersistentPath 'Machine'

function Invoke-Installer([string]$Shell, [string[]]$InstallerArgs, [switch]$Update) {
    $modulePath = $env:PSModulePath
    Push-Location -LiteralPath $fixture
    try {
        if ($Shell -eq 'powershell.exe') {
            $env:PSModulePath = "$env:ProgramFiles\WindowsPowerShell\Modules;$env:SystemRoot\System32\WindowsPowerShell\v1.0\Modules"
        }
        if ($Update) { & $Shell @InstallerArgs -Update } else { & $Shell @InstallerArgs }
    } finally { $env:PSModulePath = $modulePath; Pop-Location }
}

function Wait-ProxyReady([string]$Binary, [string]$Pipe) {
    $ErrorActionPreference = 'Continue'
    for ($i = 0; $i -lt 30; $i++) {
        & $Binary status --admin-socket $Pipe *> $null
        if ($LASTEXITCODE -eq 0) { return }
        Start-Sleep -Milliseconds 500
    }
    throw 'Admin endpoint unavailable.'
}

function Save-ServiceDiagnostics([string]$ServiceName, [string]$Prefix, [string]$Config) {
    # Capture before finally removes the isolated service and its log directory.
    # Diagnostics must not hide the original installation/permission failure.
    try {
        $output = if ($env:WINDOWS_SERVICE_DIAGNOSTICS) { $env:WINDOWS_SERVICE_DIAGNOSTICS } else { Join-Path $fixture 'diagnostics' }
        $output = Join-Path $output $ServiceName
        New-Item -ItemType Directory -Path $output -Force | Out-Null
        Get-CimInstance Win32_Service -Filter "Name='$ServiceName'" | Format-List * | Out-String | Tee-Object -FilePath (Join-Path $output 'service.txt') | Out-Host
        foreach ($path in @((Join-Path (Split-Path -Parent $Config) 'logs'), (Join-Path $Prefix 'service'))) {
            if (Test-Path -LiteralPath $path) {
                Get-ChildItem -LiteralPath $path -File -Recurse | Where-Object Extension -in @('.log','.xml') | ForEach-Object {
                    Copy-Item -LiteralPath $_.FullName -Destination $output -Force
                    if ($_.Extension -eq '.log') {
                        Write-Host "Service diagnostic: $($_.FullName)"
                        Get-Content -LiteralPath $_.FullName -Tail 100 | Out-Host
                    }
                }
            }
        }
    } catch { Write-Warning "Could not collect service diagnostics: $_" }
}
try {
    New-Item -ItemType Directory -Path $fixture -Force | Out-Null
    $build = Join-Path $fixture 'build'
    New-Item -ItemType Directory -Path $build | Out-Null
    $binary = Join-Path $build 'oci-mirror-proxy.exe'
    & go build -o $binary (Join-Path $repo 'cmd/oci-mirror-proxy')
    if ($LASTEXITCODE -ne 0) { throw 'Build failed.' }
    $release = Join-Path $fixture 'releases/download/vtest'
    New-Item -ItemType Directory -Path $release -Force | Out-Null
    $arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
    $asset = "oci-mirror-proxy_vtest_windows_$arch.zip"
    Compress-Archive -Path $binary -DestinationPath (Join-Path $release $asset)
    $hash = (Get-FileHash -LiteralPath (Join-Path $release $asset)).Hash
    [IO.File]::WriteAllText((Join-Path $release 'SHA256SUMS'), "$hash  $asset`n")
    # Publish a source fixture too; exercise the real -Build path without
    # depending on a remote release containing these uncommitted changes.
    $source = Join-Path $fixture 'source'
    New-Item -ItemType Directory -Path $source | Out-Null
    Copy-Item -LiteralPath (Join-Path $repo 'go.mod'),(Join-Path $repo 'go.sum') -Destination $source
    foreach ($dir in @('cmd','internal')) {
        Get-ChildItem -LiteralPath (Join-Path $repo $dir) -Filter '*.go' -Recurse | ForEach-Object {
            $relative = $_.FullName.Substring($repo.Length + 1)
            $target = Join-Path $source $relative
            New-Item -ItemType Directory -Path (Split-Path -Parent $target) -Force | Out-Null
            Copy-Item -LiteralPath $_.FullName -Destination $target
        }
    }
    $archiveDir = Join-Path $fixture 'archive'
    New-Item -ItemType Directory -Path $archiveDir | Out-Null
    Compress-Archive -Path $source -DestinationPath (Join-Path $archiveDir 'vtest.zip')
    $listener = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, 0)
    $listener.Start()
    $port = $listener.LocalEndpoint.Port
    $listener.Stop()
    $server = Start-Process python -ArgumentList @('-m', 'http.server', "$port", '--bind', '127.0.0.1', '--directory', ('"' + $fixture + '"')) -PassThru -NoNewWindow
    $url = "http://127.0.0.1:$port"
    $ready = $false
    for ($i = 0; $i -lt 30; $i++) {
        try { Invoke-WebRequest -UseBasicParsing "$url/" | Out-Null; $ready = $true; break } catch { Start-Sleep -Milliseconds 250 }
    }
    if (-not $ready) { throw 'Fixture server did not start.' }
    # Both PowerShell editions must be able to register and update the service.
    foreach ($shell in @('powershell.exe', 'pwsh.exe')) {
        $prefix = Join-Path $root $shell
        $config = Join-Path (Join-Path $data $shell) 'config.yaml'
        $serviceName = "$id-$($shell.Split('.')[0])"
        $installed = Join-Path $prefix 'bin/oci-mirror-proxy.exe'
        $wrapper = Join-Path $prefix "service/$serviceName.exe"
        $pipe = "\\.\pipe\$serviceName-admin"
        # This suite owns a proxy service, not the runner's Docker installation.
        # Docker setup/cleanup is covered by the isolated installer fixtures.
        $args = @('-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', (Join-Path $repo 'install.ps1'), '-Prefix', $prefix, '-Config', $config, '-ServiceName', $serviceName, '-Version', 'vtest', '-DownloadBaseUrl', $url, '-SkipDocker')
        try {
            Invoke-Installer $shell $args
            if ($LASTEXITCODE -ne 0) { throw "Install failed in $shell" }
            $service = Get-CimInstance Win32_Service -Filter "Name='$serviceName'"
            if ($service.State -ne 'Running' -or $service.StartMode -ne 'Auto' -or $service.PathName -cne ('"' + $wrapper + '"')) { throw 'Unexpected service configuration.' }
            $infoJSON = & $installed server info --admin-socket $pipe --json
            if ($LASTEXITCODE -ne 0) { throw 'Could not query service addresses.' }
            $info = $infoJSON | ConvertFrom-Json
            if ($info.config.listen -ne '127.0.0.1:24817' -or $info.config.proxy_url -ne 'http://host.docker.internal:24817') { throw 'Unexpected service addresses.' }
            $proxyProcesses = @(Get-CimInstance Win32_Process -Filter "ParentProcessId=$($service.ProcessId)" | Where-Object { $_.ExecutablePath -ieq $installed })
            if ($proxyProcesses.Count -ne 1) { throw 'Could not identify the proxy service process.' }
            $listeners = @(Get-NetTCPConnection -State Listen -OwningProcess $proxyProcesses[0].ProcessId)
            if ($listeners.Count -ne 1 -or $listeners[0].LocalAddress -ne '127.0.0.1' -or $listeners[0].LocalPort -ne 24817) { throw 'Service is not listening exclusively on IPv4 loopback.' }
            $original = (Get-FileHash -LiteralPath $config).Hash
            & (Join-Path $PSScriptRoot 'test-windows-user.ps1') -Binary $installed -ServiceConfig $config -AdminPipe $pipe -WorkDirectory (Join-Path $env:ProgramData "$id-standard-user")
            Invoke-Installer $shell $args -Update
            if ($LASTEXITCODE -ne 0 -or (Get-FileHash -LiteralPath $config).Hash -ne $original) { throw 'Update failed or overwrote configuration.' }
            Invoke-Installer $shell ($args + @('-Build')) -Update
            if ($LASTEXITCODE -ne 0 -or (Get-FileHash -LiteralPath $config).Hash -ne $original) { throw 'Source update failed or overwrote configuration.' }
            $version = & $installed version
            if ($LASTEXITCODE -ne 0 -or $version -ne 'vtest') { throw 'Source update lost release version.' }
            Restart-Service -Name $serviceName
            Wait-ProxyReady $installed $pipe
            & (Join-Path $PSScriptRoot 'test-windows-user.ps1') -Binary $installed -ServiceConfig $config -AdminPipe $pipe -WorkDirectory (Join-Path $env:ProgramData "$id-standard-user")
            Stop-Service -Name $serviceName
            (Get-Service -Name $serviceName).WaitForStatus('Stopped', [TimeSpan]::FromSeconds(45))
            Start-Service -Name $serviceName
            # Startup failure during update must restore the previous binary and
            # the previous running service, without touching user configuration.
            $previous = (Get-FileHash -LiteralPath $installed).Hash
            [IO.File]::WriteAllText($binary, 'invalid executable fixture')
            Remove-Item -LiteralPath (Join-Path $release $asset)
            Compress-Archive -Path $binary -DestinationPath (Join-Path $release $asset)
            $hash = (Get-FileHash -LiteralPath (Join-Path $release $asset)).Hash
            [IO.File]::WriteAllText((Join-Path $release 'SHA256SUMS'), "$hash  $asset`n")
            Invoke-Installer $shell $args -Update
            if ($LASTEXITCODE -eq 0) { throw 'Invalid update was accepted.' }
            if ((Get-FileHash -LiteralPath $installed).Hash -ne $previous -or (Get-FileHash -LiteralPath $config).Hash -ne $original) { throw 'Rollback failed.' }
            (Get-Service -Name $serviceName).WaitForStatus('Running', [TimeSpan]::FromSeconds(30))
            Wait-ProxyReady $installed $pipe
            & (Join-Path $PSScriptRoot 'test-windows-user.ps1') -Binary $installed -ServiceConfig $config -AdminPipe $pipe -WorkDirectory (Join-Path $env:ProgramData "$id-standard-user")
            # Restore the release fixture for the next PowerShell edition.
            Copy-Item -LiteralPath $installed -Destination $binary -Force
            Remove-Item -LiteralPath (Join-Path $release $asset)
            Compress-Archive -Path $binary -DestinationPath (Join-Path $release $asset)
            $hash = (Get-FileHash -LiteralPath (Join-Path $release $asset)).Hash
            [IO.File]::WriteAllText((Join-Path $release 'SHA256SUMS'), "$hash  $asset`n")
        } catch {
            Save-ServiceDiagnostics $serviceName $prefix $config
            throw
        } finally {
            $currentPath = Get-PersistentPath 'Machine'
            $cleanPath = ($currentPath -split ';' | Where-Object { $_ -ine (Join-Path $prefix 'bin') }) -join ';'
            if ($cleanPath -cne $currentPath) {
                if ($cleanPath -ceq ($initialMachinePath + '').TrimEnd(';')) { $cleanPath = $initialMachinePath }
                Set-PersistentPath $cleanPath 'Machine'
                Send-EnvironmentChange
            }
            $service = Get-Service -Name $serviceName -ErrorAction SilentlyContinue
            if ($service) {
                if ($service.Status -ne 'Stopped') { Stop-Service -Name $serviceName; $service.WaitForStatus('Stopped', [TimeSpan]::FromSeconds(45)) }
                $service.Dispose()
                & $wrapper uninstall
                if ($LASTEXITCODE -ne 0) { throw 'Service cleanup failed.' }
            }
            $eventSource = "HKLM:\SYSTEM\CurrentControlSet\Services\EventLog\Application\$serviceName"
            if (Test-Path -LiteralPath $eventSource) { Remove-Item -LiteralPath $eventSource -Recurse -Force }
        }
    }
} finally {
    $env:PATH = $oldPath
    $currentPath = Get-PersistentPath 'Machine'
    $testBins = @('powershell.exe','pwsh.exe') | ForEach-Object { Join-Path (Join-Path $root $_) 'bin' }
    $cleanPath = ($currentPath -split ';' | Where-Object { $_ -notin $testBins }) -join ';'
    if ($cleanPath -cne $currentPath) {
        # Restore exact baseline when possible; otherwise preserve unrelated edits.
        if ($cleanPath -ceq ($initialMachinePath + '').TrimEnd(';')) { $cleanPath = $initialMachinePath }
        Set-PersistentPath $cleanPath 'Machine'
        Send-EnvironmentChange
    }
    if ($server -and -not $server.HasExited) { Stop-Process -Id $server.Id; $server.WaitForExit() }
    foreach ($path in @($root, $data, $fixture)) {
        if (Test-Path -LiteralPath $path) { Remove-Item -LiteralPath $path -Recurse -Force }
    }
}
