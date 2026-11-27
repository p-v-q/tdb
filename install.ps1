# Install the TDB terminal database client from GitHub Releases.
#
#   irm https://raw.githubusercontent.com/p-v-q/tdb/main/install.ps1 | iex
#
# Parameters / environment variables:
#   -Version / TDB_VERSION        release to install (default: latest)
#   -InstallDir / TDB_INSTALL_DIR directory for tdb.exe
#                                 (default: %LOCALAPPDATA%\Programs\tdb)
#   -NoPath                       do not add the install directory to PATH
#   TDB_REPO                      owner/repo to download from (default: p-v-q/tdb)
[CmdletBinding()]
param(
    [string]$Version = $env:TDB_VERSION,
    [string]$InstallDir = $(if ($env:TDB_INSTALL_DIR) { $env:TDB_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\tdb' }),
    [switch]$NoPath
)

$ErrorActionPreference = 'Stop'

try {
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
} catch {
    # Older PowerShell builds may not need or expose this.
}

$repo = if ($env:TDB_REPO) { $env:TDB_REPO } else { 'p-v-q/tdb' }

$archRaw = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
$arch = switch ($archRaw.ToUpperInvariant()) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    default { throw "unsupported architecture: $archRaw" }
}

if (-not $Version) {
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest" -Headers @{ 'User-Agent' = 'tdb-install' }
    $Version = $release.tag_name
}
$Version = $Version -replace '^v', ''
$asset = "tdb_${Version}_windows_${arch}.zip"
$base = "https://github.com/$repo/releases/download/v$Version"

$tmp = Join-Path ([IO.Path]::GetTempPath()) ('tdb-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    $zip = Join-Path $tmp $asset
    Write-Host "Downloading $asset"
    Invoke-WebRequest -Uri "$base/$asset" -OutFile $zip -UseBasicParsing

    $sums = Join-Path $tmp 'SHA256SUMS'
    $haveSums = $false
    try {
        Invoke-WebRequest -Uri "$base/SHA256SUMS" -OutFile $sums -UseBasicParsing
        $haveSums = $true
    } catch {
        Write-Warning "checksum verification skipped: $($_.Exception.Message)"
    }
    if ($haveSums) {
        $line = Get-Content $sums | Where-Object { $_ -match "\s$([regex]::Escape($asset))$" } | Select-Object -First 1
        if ($line) {
            $expected = ($line -split '\s+')[0].ToLowerInvariant()
            $actual = (Get-FileHash -Algorithm SHA256 -Path $zip).Hash.ToLowerInvariant()
            if ($actual -ne $expected) {
                throw "checksum mismatch for $asset"
            }
        }
    }

    Expand-Archive -Path $zip -DestinationPath $tmp -Force
    $exe = Join-Path (Join-Path $tmp ([IO.Path]::GetFileNameWithoutExtension($asset))) 'tdb.exe'
    if (-not (Test-Path $exe)) {
        throw 'archive did not contain tdb.exe'
    }

    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    $target = Join-Path $InstallDir 'tdb.exe'
    Copy-Item $exe $target -Force
    Write-Host "Installed tdb to $target"

    if (-not $NoPath) {
        $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
        $parts = @($userPath -split ';' | Where-Object { $_ })
        if ($parts -notcontains $InstallDir) {
            [Environment]::SetEnvironmentVariable('Path', (($parts + $InstallDir) -join ';'), 'User')
            Write-Host "Added $InstallDir to the user PATH; restart the shell to use it."
        }
    }

    & $target -version
} finally {
    Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
}
