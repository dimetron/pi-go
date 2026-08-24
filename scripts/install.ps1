# pi-go installer for Windows.
#
# Works on Windows PowerShell 5.1 (Windows 10/11 built-in) and PowerShell 7+;
# no bash, curl aliases, or && chains — everything uses cmdlets available in
# 5.1. Downloads the latest pi-go windows/amd64 release zip and installs
# pi.exe into %LOCALAPPDATA%\Programs, adding that directory to the user PATH
# when missing.
#
# Usage:
#   powershell -NoProfile -ExecutionPolicy Bypass -File install.ps1
#   or one line from an existing shell:
#   powershell -NoProfile -Command "iwr https://raw.githubusercontent.com/dimetron/pi-go/main/scripts/install.ps1 -UseBasicParsing | iex"

$ErrorActionPreference = "Stop"

# Older Windows PowerShell 5.1 images default to TLS 1.0/1.1, which GitHub
# rejects: the download then hangs or fails with "The underlying connection was
# closed".
#
# Only force TLS 1.2 when the process is pinned to something older. A current
# build reports SystemDefault, meaning "let the OS negotiate", which already
# reaches TLS 1.2 and can reach 1.3 — and `SystemDefault -bor Tls12` would
# replace that with a hard TLS 1.2 pin, giving up 1.3 to fix a problem the
# machine does not have.
if ([Net.ServicePointManager]::SecurityProtocol -ne [Net.SecurityProtocolType]::SystemDefault) {
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
}

$repo = "dimetron/pi-go"
$installDir = Join-Path $env:LOCALAPPDATA "Programs"

# --- Resolve the latest release tag via the GitHub API -----------------------
$apiUrl = "https://api.github.com/repos/$repo/releases/latest"
$headers = @{ "User-Agent" = "pi-go-installer" }
$token = $env:GITHUB_TOKEN
if (-not $token) { $token = $env:GH_TOKEN }
if ($token) { $headers["Authorization"] = "Bearer $token" }

Write-Host "Resolving latest release..."
$release = Invoke-RestMethod -Uri $apiUrl -Headers $headers -UseBasicParsing
$tag = $release.tag_name
Write-Host "Latest release: $tag"

# --- Pick the windows amd64 asset --------------------------------------------
$assetName = "pi-go_$($tag.TrimStart("v"))_windows_amd64.zip"
$asset = $release.assets | Where-Object { $_.name -eq $assetName } | Select-Object -First 1
if (-not $asset) {
    # Fall back to any windows amd64 zip in case naming changes.
    $asset = $release.assets | Where-Object { $_.name -like "*windows_amd64*.zip" -and $_.name -notlike "*.sbom.*" } | Select-Object -First 1
}
if (-not $asset) {
    throw "No windows_amd64 zip found in release $tag"
}

# --- Download ------------------------------------------------------------------
$zipPath = Join-Path $env:TEMP $asset.name
$extractDir = Join-Path $env:TEMP ("pi-go-" + [guid]::NewGuid().ToString("N").Substring(0, 8))

Write-Host "Downloading $($asset.name)..."
Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $zipPath -UseBasicParsing

# --- Verify the download against the release checksums -------------------------
# checksums.txt is a GoReleaser artifact listing "<sha256>  <filename>" for every
# archive in the release. Checking it catches a truncated or corrupted download
# and a swapped asset, which is what an installer can check on its own.
#
# It is not proof of origin: checksums.txt travels the same path as the archive,
# so anyone who could substitute one could substitute both. `pi verify` is the
# check that answers that — it looks the binary's digest up in GitHub's
# attestations API and verifies the Sigstore bundle against this repo's release
# workflow. The last line of this script points at it.
#
# Missing or unlisted checksums are a hard failure rather than a warning. A
# release without them is a broken release, and an installer that shrugs and
# continues is an installer whose verification means nothing.
$sumAsset = $release.assets | Where-Object { $_.name -eq "checksums.txt" } | Select-Object -First 1
if (-not $sumAsset) {
    Remove-Item -Force $zipPath -ErrorAction SilentlyContinue
    throw "Release $tag publishes no checksums.txt; refusing to install an unverifiable download."
}

Write-Host "Verifying checksum..."
$sumsPath = Join-Path $env:TEMP "pi-go-checksums-$tag.txt"
Invoke-WebRequest -Uri $sumAsset.browser_download_url -OutFile $sumsPath -UseBasicParsing

# Lines are "<hex>  <name>"; split on whitespace rather than a fixed width so a
# one- or two-space variant both parse.
$expected = $null
foreach ($line in Get-Content $sumsPath) {
    $parts = $line.Trim() -split "\s+", 2
    if ($parts.Count -eq 2 -and $parts[1].Trim() -eq $asset.name) {
        $expected = $parts[0].Trim()
        break
    }
}
Remove-Item -Force $sumsPath -ErrorAction SilentlyContinue

if (-not $expected) {
    Remove-Item -Force $zipPath -ErrorAction SilentlyContinue
    throw "checksums.txt for $tag does not list $($asset.name); refusing to install."
}

$actual = (Get-FileHash -Path $zipPath -Algorithm SHA256).Hash
if ($actual -ine $expected) {
    Remove-Item -Force $zipPath -ErrorAction SilentlyContinue
    throw "Checksum mismatch for $($asset.name): expected $expected, got $actual. The download was discarded."
}
Write-Host "  sha256 $($actual.ToLower())"

# --- Extract -------------------------------------------------------------------
Write-Host "Extracting..."
New-Item -ItemType Directory -Force -Path $extractDir | Out-Null
Expand-Archive -Path $zipPath -DestinationPath $extractDir -Force

$exe = Get-ChildItem -Path $extractDir -Filter "pi.exe" -Recurse | Select-Object -First 1
if (-not $exe) { throw "pi.exe not found in archive" }

# --- Install -------------------------------------------------------------------
New-Item -ItemType Directory -Force -Path $installDir | Out-Null
$dest = Join-Path $installDir "pi.exe"
Copy-Item -Path $exe.FullName -Destination $dest -Force
Remove-Item -Recurse -Force $extractDir
Remove-Item -Force $zipPath

# --- Add install dir to the user PATH when missing ------------------------------
# Compare complete entries (not substrings) so e.g. an existing
# "...\Programs\Microsoft VS Code\bin" does not hide the missing
# "...\Programs" entry itself.
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
$entries = $userPath -split ";" | ForEach-Object { $_.Trim().TrimEnd("\") } | Where-Object { $_ }
$want = $installDir.TrimEnd("\")
$present = $entries | Where-Object { $_ -ieq $want }
if (-not $present) {
    Write-Host "Adding $installDir to user PATH..."
    # A fresh account can have no user PATH at all, and "$null;$installDir"
    # would write a leading separator — an empty entry that some tools read as
    # the current directory.
    if ([string]::IsNullOrWhiteSpace($userPath)) { $newPath = $installDir }
    else { $newPath = $userPath.TrimEnd(";") + ";" + $installDir }
    [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
    $env:PATH = "$env:PATH;$installDir"
    Write-Host "PATH updated. Restart your terminal for it to take effect everywhere."
} else {
    $env:PATH = "$env:PATH;$installDir"
}

# --- Verify ----------------------------------------------------------------------
Write-Host ""
Write-Host (& $dest --version)
Write-Host "Installed to $dest" -ForegroundColor Green
Write-Host "Run 'pi verify' to check this binary against its build provenance."
