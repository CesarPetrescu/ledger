# Install the latest stable Windows client without administrator access.
[CmdletBinding()]
param(
    [string]$Server,
    [string]$Name = $env:COMPUTERNAME,
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA 'Programs\Ledger'),
    [switch]$NoPath
)
$ErrorActionPreference = 'Stop'
if ($env:OS -ne 'Windows_NT') { throw 'Use install.sh on Linux.' }
$architecture = $env:PROCESSOR_ARCHITEW6432
if (-not $architecture) { $architecture = $env:PROCESSOR_ARCHITECTURE }
$arch = switch ($architecture) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    default { throw 'Ledger requires 64-bit Windows (x64 or ARM64).' }
}
$assetName = "ledger_windows_$arch.exe"
$null = Get-Command curl.exe -CommandType Application -ErrorAction Stop

function Download-LedgerFile([string]$Url, [string]$Destination, [long]$MaxSize) {
    & curl.exe --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --connect-timeout 10 --max-time 120 --max-filesize $MaxSize $Url --output $Destination
    if ($LASTEXITCODE -ne 0) { throw 'Download failed; the existing installation is unchanged.' }
}

$downloadDir = Join-Path ([IO.Path]::GetTempPath()) ('ledger-install-' + [guid]::NewGuid().ToString('N'))
$null = [IO.Directory]::CreateDirectory($downloadDir)
$staged = $null
try {
    $metadata = Join-Path $downloadDir 'release.json'
    Download-LedgerFile 'https://api.github.com/repos/CesarPetrescu/ledger/releases/latest' $metadata 1048576
    $release = Get-Content -LiteralPath $metadata -Raw | ConvertFrom-Json
    if ($release.draft -or $release.prerelease -or $release.tag_name -cnotmatch '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$') { throw 'Latest release is not a stable version.' }
    $assets = @($release.assets | Where-Object { $_.name -ceq $assetName })
    if ($assets.Count -ne 1) { throw "Release does not contain $assetName." }
    $asset = $assets[0]
    $expectedUrl = "https://github.com/CesarPetrescu/ledger/releases/download/$($release.tag_name)/$assetName"
    if ($asset.browser_download_url -cne $expectedUrl -or $asset.digest -cnotmatch '^sha256:[0-9a-f]{64}$' -or $asset.size -le 0 -or $asset.size -gt 33554432) { throw 'Invalid release asset metadata.' }
    $download = Join-Path $downloadDir $assetName
    Download-LedgerFile $expectedUrl $download $asset.size
    $digest = (Get-FileHash -LiteralPath $download -Algorithm SHA256).Hash.ToLowerInvariant()
    if ((Get-Item -LiteralPath $download).Length -ne $asset.size -or "sha256:$digest" -cne $asset.digest) { throw 'Checksum verification failed; existing installation preserved.' }
    $downloadVersion = & $download version
    if ($LASTEXITCODE -ne 0 -or "$downloadVersion".Trim() -cne $release.tag_name) { throw 'Downloaded executable failed its version check.' }

    $InstallDir = [IO.Path]::GetFullPath($InstallDir)
    $null = [IO.Directory]::CreateDirectory($InstallDir)
    if ((Get-Item -LiteralPath $InstallDir).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Install directory must not be a link or junction.' }
    $target = Join-Path $InstallDir 'ledger.exe'
    $backup = "$target.old"
    if ((Test-Path -LiteralPath $target) -and ((Get-Item -LiteralPath $target).Attributes -band ([IO.FileAttributes]::ReparsePoint -bor [IO.FileAttributes]::Directory))) { throw 'Refusing to replace a link or directory.' }
    $staged = Join-Path $InstallDir ('.ledger-' + [guid]::NewGuid().ToString('N') + '.exe')
    [IO.File]::Copy($download, $staged)
    $hadOriginal = Test-Path -LiteralPath $target
    if ($hadOriginal) {
        if (Test-Path -LiteralPath $backup) { [IO.File]::Delete($backup) }
        [IO.File]::Move($target, $backup)
    }
    try { [IO.File]::Move($staged, $target); $staged = $null }
    catch { if ($hadOriginal) { [IO.File]::Move($backup, $target) }; throw }
    if (-not $NoPath) {
        $userPath = [string][Environment]::GetEnvironmentVariable('Path', 'User')
        if ($InstallDir -notin ($userPath -split ';')) {
            [Environment]::SetEnvironmentVariable('Path', ($userPath.TrimEnd(';') + ';' + $InstallDir).TrimStart(';'), 'User')
        }
        if ($InstallDir -notin ($env:Path -split ';')) { $env:Path = $InstallDir + ';' + $env:Path }
    }
    Write-Host "Installed Ledger $($release.tag_name) at $target"
    if ($Server) {
        & $target connect codex --server $Server --name $Name
        if ($LASTEXITCODE -ne 0) { throw 'Installed, but connection failed. Rerun ledger connect codex.' }
    } else {
        Write-Host 'Open a new terminal, then connect:'
        Write-Host '  ledger connect codex --server https://ledger.example.com --name "My PC"'
    }
} finally {
    if ($staged -and (Test-Path -LiteralPath $staged)) { Remove-Item -LiteralPath $staged -Force }
    Remove-Item -LiteralPath $downloadDir -Recurse -Force
}
