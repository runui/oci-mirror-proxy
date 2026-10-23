#Requires -Version 5.1
# Keep installation checks in this directory; extend these platform suites
# rather than adding separate scripts for individual features or scenarios.
# Run installed-binary checks under a real standard account, not an admin token.
param(
    [Parameter(Mandatory=$true)][string]$Binary,
    [Parameter(Mandatory=$true)][string]$ServiceConfig,
    [Parameter(Mandatory=$true)][string]$AdminPipe,
    [Parameter(Mandatory=$true)][string]$WorkDirectory
)
$ErrorActionPreference = 'Stop'
$name = 'ocitest' + [Guid]::NewGuid().ToString('N').Substring(0, 10)
$user = $null
$workCreated = $false
try {
    $password = ConvertTo-SecureString ('Aa1!' + [Guid]::NewGuid().ToString('N')) -AsPlainText -Force
    $user = New-LocalUser -Name $name -Password $password -Description 'Temporary OCI installer permission check'
    $group = Get-LocalGroup -SID 'S-1-5-32-545'
    Add-LocalGroupMember -Group $group -Member $user
    if (Test-Path -LiteralPath $WorkDirectory) { throw 'Standard-user work directory already exists.' }
    New-Item -ItemType Directory -Path $WorkDirectory | Out-Null
    $workCreated = $true
    $acl = New-Object Security.AccessControl.DirectorySecurity
    $acl.SetAccessRuleProtection($true, $false)
    foreach ($sid in @('S-1-5-18','S-1-5-32-544')) {
        $identity = New-Object Security.Principal.SecurityIdentifier($sid)
        $acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($identity, 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow')))
    }
    $rule = New-Object Security.AccessControl.FileSystemAccessRule($user.SID, 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow')
    $acl.AddAccessRule($rule)
    Set-Acl -LiteralPath $WorkDirectory -AclObject $acl
    $data = @{ Binary=$Binary; ServiceConfig=$ServiceConfig; AdminPipe=$AdminPipe; Work=$WorkDirectory } | ConvertTo-Json -Compress
    $payload = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($data))
    $command = @'
$ErrorActionPreference = 'Stop'
$proxy = $null
try {
    $data = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('__PAYLOAD__')) | ConvertFrom-Json
    $principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
    if ($principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'Check must run as a standard user.' }
    $env:PATH = [Environment]::GetEnvironmentVariable('Path','Machine') + ';' + [Environment]::GetEnvironmentVariable('Path','User')
    $installedCommand = Get-Command oci-mirror-proxy.exe -CommandType Application | Select-Object -First 1
    if ($installedCommand.Source -ine $data.Binary) { throw 'Installed binary is not discoverable through PATH.' }
    & $data.Binary version
    if ($LASTEXITCODE -ne 0) { throw 'Standard user cannot execute installed binary.' }
    & $data.Binary --help
    if ($LASTEXITCODE -ne 0) { throw 'Standard user cannot read help.' }
    foreach ($path in @($data.Binary, $data.ServiceConfig)) {
        $denied = $false
        try { $file = [IO.File]::Open($path, 'Open', 'Write', 'ReadWrite'); $file.Dispose() } catch [UnauthorizedAccessException] { $denied=$true }
        if (-not $denied) { throw "System artifact is writable: $path" }
    }
    $denied = $false
    try { [IO.File]::ReadAllText($data.ServiceConfig) > $null } catch [UnauthorizedAccessException] { $denied=$true }
    if (-not $denied) { throw 'Service config unexpectedly readable.' }
    # The proxy data plane remains available without administrative privileges.
    $response = Invoke-WebRequest -UseBasicParsing 'http://127.0.0.1:24817/v2/'
    if ($response.StatusCode -ne 200) { throw 'Service proxy is unavailable to users.' }
    # SYSTEM services expose their local management API to local users, while
    # their executable/configuration files remain protected above.
    & $data.Binary status --admin-socket $data.AdminPipe
    if ($LASTEXITCODE -ne 0) { throw 'Standard user cannot query the system service.' }
    & $data.Binary server info --admin-socket $data.AdminPipe --json
    if ($LASTEXITCODE -ne 0) { throw 'Standard user cannot read system server information.' }
    & $data.Binary reload --admin-socket $data.AdminPipe
    if ($LASTEXITCODE -ne 0) { throw 'Standard user cannot manage the system service.' }
    # Isolate per-user configuration while exercising the normal default lookup.
    $env:APPDATA = Join-Path $data.Work 'Roaming'
    $env:LOCALAPPDATA = Join-Path $data.Work 'Local'
    New-Item -ItemType Directory -Path $env:APPDATA,$env:LOCALAPPDATA -Force > $null
    & $data.Binary init
    if ($LASTEXITCODE -ne 0) { throw 'User initialization failed.' }
    $config = Join-Path $env:APPDATA 'oci-mirror-proxy\config.yaml'
    $listener = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, 0)
    $listener.Start(); $port=$listener.LocalEndpoint.Port; $listener.Stop()
    $text = [IO.File]::ReadAllText($config).Replace('127.0.0.1:24817', "127.0.0.1:$port")
    [IO.File]::WriteAllText($config,$text)
    $proxy = Start-Process -FilePath $data.Binary -ArgumentList @('serve') -WorkingDirectory $data.Work -PassThru -RedirectStandardOutput (Join-Path $data.Work 'stdout.log') -RedirectStandardError (Join-Path $data.Work 'stderr.log')
    $ready=$false
    for ($i=0; $i -lt 30; $i++) {
        $ErrorActionPreference='Continue'
        & $data.Binary status *> $null
        $ready=$LASTEXITCODE -eq 0
        $ErrorActionPreference='Stop'
        if ($ready) { break }
        Start-Sleep -Milliseconds 250
    }
    if (-not $ready) { throw 'User foreground instance did not start.' }
    & $data.Binary reload
    if ($LASTEXITCODE -ne 0) { throw 'User cannot manage their own instance.' }
    [IO.File]::WriteAllText((Join-Path $data.Work 'passed'), 'standard-user checks passed')
    exit 0
} catch {
    Write-Host ($_ | Format-List * -Force | Out-String)
    [Console]::Error.WriteLine($_.ToString())
    exit 1
} finally {
    if ($proxy -and -not $proxy.HasExited) { Stop-Process -Id $proxy.Id; $proxy.WaitForExit() }
}
'@
    $command = $command.Replace('__PAYLOAD__', $payload)
    $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($command))
    $credential = New-Object Management.Automation.PSCredential("$env:COMPUTERNAME\$name", $password)
    $stdout = Join-Path $WorkDirectory 'check-stdout.log'
    $stderr = Join-Path $WorkDirectory 'check-stderr.log'
    # Do not pass pwsh's module search path into Windows PowerShell 5.1; loading
    # Security/Utility from the wrong edition can fail before the permission check.
    $modulePath = $env:PSModulePath
    try {
        $env:PSModulePath = "$env:ProgramFiles\WindowsPowerShell\Modules;$env:SystemRoot\System32\WindowsPowerShell\v1.0\Modules"
        $process = Start-Process -FilePath "$env:SystemRoot\System32\WindowsPowerShell\v1.0\powershell.exe" -ArgumentList @('-NoLogo','-NoProfile','-EncodedCommand',$encoded) -Credential $credential -LoadUserProfile -WorkingDirectory $WorkDirectory -RedirectStandardOutput $stdout -RedirectStandardError $stderr -Wait -PassThru
    } finally { $env:PSModulePath = $modulePath }
    try {
        if ($process.ExitCode -ne 0 -or -not (Test-Path -LiteralPath (Join-Path $WorkDirectory 'passed'))) {
            foreach ($log in @($stdout, $stderr, (Join-Path $WorkDirectory 'stdout.log'), (Join-Path $WorkDirectory 'stderr.log'))) {
                if (Test-Path -LiteralPath $log) {
                    Write-Host "Standard-user diagnostic: $log"
                    Get-Content -LiteralPath $log -Tail 100 | Out-Host
                }
            }
            throw "Standard-user permission check failed (exit $($process.ExitCode))."
        }
    } finally { $process.Dispose() }
} finally {
    try {
        if ($user) {
            try {
                $profile = Get-CimInstance Win32_UserProfile -Filter "SID='$($user.SID.Value)'"
                if ($profile) { $profile | Remove-CimInstance }
            } finally { Remove-LocalUser -Name $name }
        }
    } finally {
        if ($workCreated -and (Test-Path -LiteralPath $WorkDirectory)) { Remove-Item -LiteralPath $WorkDirectory -Recurse -Force }
    }
}
