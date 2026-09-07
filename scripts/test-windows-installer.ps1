param([Parameter(Mandatory = $true)][string]$Binary)
$ErrorActionPreference = 'Stop'
$script:fixtureBinaryPath = (Resolve-Path -LiteralPath $Binary).Path
$installer = Join-Path $PSScriptRoot '..\install.ps1'
$testDir = Join-Path ([IO.Path]::GetTempPath()) ('ledger-test-' + [guid]::NewGuid().ToString('N'))
$null = [IO.Directory]::CreateDirectory($testDir)
$installDir = Join-Path $testDir 'Atlas client with spaces'
$arch = if (($env:PROCESSOR_ARCHITEW6432 -eq 'ARM64') -or ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64')) { 'arm64' } else { 'amd64' }
$assetName = "ledger_windows_$arch.exe"
$tag = (& $script:fixtureBinaryPath version).Trim()
$digest = (Get-FileHash -LiteralPath $script:fixtureBinaryPath -Algorithm SHA256).Hash.ToLowerInvariant()
$script:fixtureAssetUrl = "https://github.com/CesarPetrescu/ledger/releases/download/$tag/$assetName"
$script:fixtureMetadata = @{
    tag_name = $tag; draft = $false; prerelease = $false
    assets = @(@{name = $assetName; browser_download_url = $script:fixtureAssetUrl; digest = "sha256:$digest"; size = (Get-Item -LiteralPath $script:fixtureBinaryPath).Length})
}
$script:corrupt = $false
$script:requestedAssets = 0
# No network or real account data: exercise the installer with the actual Windows binary.
function curl.exe {
    $destination = $args[[Array]::IndexOf($args, '--output') + 1]
    $url = $args[[Array]::IndexOf($args, '--output') - 1]
    if ($url -eq 'https://api.github.com/repos/CesarPetrescu/ledger/releases/latest') {
        $script:fixtureMetadata | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath $destination -Encoding UTF8
    } elseif ($url -eq $script:fixtureAssetUrl) {
        $script:requestedAssets++
        if ($script:corrupt) { [IO.File]::WriteAllText($destination, 'corrupt') }
        else { [IO.File]::Copy($script:fixtureBinaryPath, $destination) }
    } else { throw 'Installer requested an unexpected URL.' }
    $global:LASTEXITCODE = 0
}
try {
    & $installer -InstallDir $installDir -NoPath
    $installed = Join-Path $installDir 'ledger.exe'
    if ((& $installed version).Trim() -ne $tag) { throw 'Installed client did not run.' }
    # Reinstallation exercises Windows replacement and leaves a rollback copy.
    & $installer -InstallDir $installDir -NoPath
    if (-not (Test-Path -LiteralPath "$installed.old")) { throw 'Rollback copy is missing.' }
    $script:corrupt = $true
    $rejected = $false
    try { & $installer -InstallDir $installDir -NoPath } catch { $rejected = $true }
    if (-not $rejected) { throw 'Installer accepted a corrupt download.' }
    if ((Get-FileHash -LiteralPath $installed -Algorithm SHA256).Hash.ToLowerInvariant() -ne $digest) { throw 'Failed installation changed the working client.' }
    $script:fixtureMetadata.assets[0].browser_download_url = 'https://evil.example/ledger.exe'
    $requestsBefore = $script:requestedAssets
    $rejected = $false
    try { & $installer -InstallDir $installDir -NoPath } catch { $rejected = $true }
    if (-not $rejected -or $script:requestedAssets -ne $requestsBefore) { throw 'Installer did not reject the foreign URL before downloading.' }
    Write-Host 'Installer passed: install, replace, corrupt-download preservation, and foreign-URL rejection.'
} finally {
    Remove-Item -LiteralPath $testDir -Recurse -Force
}
