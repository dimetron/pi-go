# Installer for the Google Antigravity ACP server used by pi-go's "agy" subagent.
#
# The agy CLI has no ACP mode: Antigravity ships a standalone ACP server binary
# distributed as a platform archive by the Agent Client Protocol registry. This
# script resolves the entry for the current platform, downloads the archive and
# extracts it into %USERPROFILE%\.pi-go\acp\agy, which is the first location the
# adapter (internal/acp/client/agy) searches.
#
# The download is large (~300 MB compressed, ~900 MB extracted).
#
# Works on Windows PowerShell 5.1 (Windows 10/11 built-in) and PowerShell 7+.
#
# Usage:
#   powershell -NoProfile -ExecutionPolicy Bypass -File install-agy-acp.ps1 [-InstallDir <path>]

param(
    [string]$InstallDir = (Join-Path $env:USERPROFILE ".pi-go\acp\agy"),
    [string]$AgentJsonUrl = $env:AGY_ACP_AGENT_JSON
)

$ErrorActionPreference = "Stop"

if (-not $AgentJsonUrl) {
    $AgentJsonUrl = "https://raw.githubusercontent.com/agentclientprotocol/registry/main/antigravity-acp/agent.json"
}

# Windows PowerShell 5.1 images can default to TLS 1.0/1.1, which the download
# hosts reject. Only force TLS 1.2 when the process is pinned to something
# older; SystemDefault already negotiates 1.2 or 1.3. See install.ps1.
if ([Net.ServicePointManager]::SecurityProtocol -ne [Net.SecurityProtocolType]::SystemDefault) {
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
}

# --- Resolve the registry entry for this platform ----------------------------
# Registry platform identifiers come from the registry's FORMAT.md.
$arch = $env:PROCESSOR_ARCHITECTURE
if (-not $arch) { $arch = "AMD64" }
switch ($arch.ToUpper()) {
    "AMD64" { $platform = "windows-x86_64" }
    "ARM64" { $platform = "windows-aarch64" }
    default { throw "Unsupported architecture $arch" }
}

Write-Host "Resolving antigravity-acp for $platform..."
$entry = Invoke-RestMethod -Uri $AgentJsonUrl -UseBasicParsing
$target = $entry.distribution.binary.$platform
if (-not $target) { throw "No binary distribution for $platform" }

$archiveUrl = $target.archive
$sha256 = $target.sha256

# --- Download ----------------------------------------------------------------
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("agy-acp-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $tmp -Force | Out-Null
try {
    $archive = Join-Path $tmp ([System.IO.Path]::GetFileName($archiveUrl.Split("?")[0]))

    Write-Host "Downloading $archiveUrl"
    # Invoke-WebRequest's progress bar makes a 300 MB download crawl on 5.1.
    $oldProgress = $ProgressPreference
    $ProgressPreference = "SilentlyContinue"
    try {
        Invoke-WebRequest -Uri $archiveUrl -OutFile $archive -UseBasicParsing
    } finally {
        $ProgressPreference = $oldProgress
    }

    if ($sha256) {
        Write-Host "Verifying sha256..."
        $actual = (Get-FileHash -Path $archive -Algorithm SHA256).Hash
        if ($actual.ToLower() -ne $sha256.ToLower()) {
            throw "sha256 mismatch: got $actual, want $sha256"
        }
    }

    # --- Extract -------------------------------------------------------------
    Write-Host "Extracting into $InstallDir"
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    if ($archive -notmatch "\.zip$") {
        throw "Unsupported archive format $archive"
    }
    # Expand-Archive -Force needs PowerShell 5.0+; it is present on every
    # supported Windows build.
    Expand-Archive -Path $archive -DestinationPath $InstallDir -Force
} finally {
    Remove-Item -Path $tmp -Recurse -Force -ErrorAction SilentlyContinue
}

$server = Join-Path $InstallDir "agy_acp_server.exe"
if (-not (Test-Path $server)) {
    throw "No agy_acp_server.exe found under $InstallDir"
}

Write-Host "Installed $server"
Write-Host "pi-go finds it automatically; override with PI_ACP_AGY_CMD if you move it."
Write-Host ""
Write-Host "Next: the server does not inherit the agy CLI login. Select an auth method in"
Write-Host "  %USERPROFILE%\.gemini\antigravity-acp\settings.json, e.g. {""auth"": {""type"": ""oauth-personal""}},"
Write-Host "then run $server once by hand to complete the one-time browser sign-in."
