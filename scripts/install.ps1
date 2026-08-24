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

# Windows PowerShell 5.1 defaults to TLS 1.0/1.1, which GitHub rejects; without
# this the download can hang or fail with "The underlying connection was
# closed". Harmless on PowerShell 7+, which already uses TLS 1.2+.
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

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

# --- Download and extract ------------------------------------------------------
$zipPath = Join-Path $env:TEMP $asset.name
$extractDir = Join-Path $env:TEMP ("pi-go-" + [guid]::NewGuid().ToString("N").Substring(0, 8))

Write-Host "Downloading $($asset.name)..."
Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $zipPath -UseBasicParsing

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
    [Environment]::SetEnvironmentVariable("Path", "$userPath;$installDir", "User")
    $env:PATH = "$env:PATH;$installDir"
    Write-Host "PATH updated. Restart your terminal for it to take effect everywhere."
} else {
    $env:PATH = "$env:PATH;$installDir"
}

# --- Verify ----------------------------------------------------------------------
Write-Host ""
Write-Host (& $dest --version)
Write-Host "Installed to $dest" -ForegroundColor Green
