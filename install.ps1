#Requires -Version 5.1
[CmdletBinding()]
param(
    [string]$Prefix = (Join-Path $env:LOCALAPPDATA 'oci-mirror-proxy'),
    [string]$Version = $(if ($env:VERSION) { $env:VERSION } else { 'latest' }),
    [string]$DownloadBaseUrl = $(if ($env:DOWNLOAD_BASE_URL) { $env:DOWNLOAD_BASE_URL } else { 'https://git.zmide.com/bin/oci-mirror-proxy' }),
    [switch]$Update
)

$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
if ($env:OS -ne 'Windows_NT') { throw 'Use install.sh on Unix systems.' }
$repo = $DownloadBaseUrl.TrimEnd('/')
if ($repo -notmatch '^https?://') { throw 'DownloadBaseUrl must be an HTTP or HTTPS repository root URL.' }
if ($Version -notmatch '^[a-zA-Z0-9][a-zA-Z0-9._+-]*$') { throw 'Invalid release tag.' }
if (-not [IO.Path]::IsPathRooted($Prefix)) { throw 'Prefix must be an absolute path.' }

$binDir = Join-Path $Prefix 'bin'
if ($Update -and -not $PSBoundParameters.ContainsKey('Prefix')) {
    $existing = Get-Command oci-mirror-proxy.exe -CommandType Application -ErrorAction Stop | Select-Object -First 1
    $binDir = Split-Path -Parent $existing.Source
}
$destination = Join-Path $binDir 'oci-mirror-proxy.exe'
if ((Test-Path -LiteralPath $destination) -and ((Get-Item -LiteralPath $destination).Attributes -band [IO.FileAttributes]::ReparsePoint)) {
    throw "Refusing to replace symlink $destination."
}

# PROCESSOR_ARCHITEW6432 identifies the native OS when running a 32-bit shell.
$nativeArch = $env:PROCESSOR_ARCHITEW6432
if (-not $nativeArch) { $nativeArch = $env:PROCESSOR_ARCHITECTURE }
$arch = switch ($nativeArch) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    default { throw "Unsupported Windows architecture: $nativeArch" }
}
$tempDir = Join-Path ([IO.Path]::GetTempPath()) ('oci-mirror-proxy-install-' + [Guid]::NewGuid().ToString('N'))
$staged = $null
New-Item -ItemType Directory -Path $tempDir | Out-Null
try {
    if ($Version -eq 'latest') {
        $response = Invoke-WebRequest -UseBasicParsing -Uri "$repo/releases/latest"
        $uri = $response.BaseResponse.ResponseUri
        if (-not $uri) { $uri = $response.BaseResponse.RequestMessage.RequestUri }
        if ($uri.AbsolutePath -notmatch '/releases/tag/([a-zA-Z0-9][a-zA-Z0-9._+-]*)/?$') {
            throw 'Cannot resolve latest release; specify -Version explicitly.'
        }
        $Version = $Matches[1]
    }
    $asset = "oci-mirror-proxy_${Version}_windows_${arch}.zip"
    $archive = Join-Path $tempDir $asset
    Write-Host "Downloading $asset..."
    Invoke-WebRequest -UseBasicParsing -Uri "$repo/releases/download/$Version/$asset" -OutFile $archive
    $checksums = Join-Path $tempDir 'SHA256SUMS'
    Invoke-WebRequest -UseBasicParsing -Uri "$repo/releases/download/$Version/SHA256SUMS" -OutFile $checksums
    $pattern = '^([a-fA-F0-9]{64})\s+\*?(?:\./)?' + [regex]::Escape($asset) + '$'
    $expected = @(Get-Content -LiteralPath $checksums | ForEach-Object {
        if ($_ -match $pattern) { $Matches[1] }
    })
    if ($expected.Count -ne 1) { throw "Missing or invalid checksum for $asset." }
    if ((Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash -ne $expected[0]) {
        throw 'SHA256 verification failed; installation aborted.'
    }
    $extracted = Join-Path $tempDir 'extracted'
    Expand-Archive -LiteralPath $archive -DestinationPath $extracted
    $binary = Join-Path $extracted 'oci-mirror-proxy.exe'
    if (-not (Test-Path -LiteralPath $binary -PathType Leaf)) { throw 'Release archive is missing the executable.' }
    New-Item -ItemType Directory -Path $binDir -Force | Out-Null
    $staged = Join-Path $binDir ('.oci-mirror-proxy-' + [Guid]::NewGuid().ToString('N') + '.exe')
    Copy-Item -LiteralPath $binary -Destination $staged
    # Windows locks running executables; fail without deleting the installed binary.
    if (Test-Path -LiteralPath $destination) {
        [IO.File]::Replace($staged, $destination, $null)
    } else {
        [IO.File]::Move($staged, $destination)
    }
    $staged = $null
    Write-Host "Installed $destination ($Version)"
    Write-Host "Add $binDir to PATH, then run: oci-mirror-proxy serve"
    Write-Host 'Configuration preserved. Windows service installation is not managed by this script.'
} finally {
    if ($staged -and (Test-Path -LiteralPath $staged)) { Remove-Item -LiteralPath $staged -Force }
    Remove-Item -LiteralPath $tempDir -Recurse -Force
}
