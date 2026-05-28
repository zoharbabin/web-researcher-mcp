# web-researcher-mcp installer for Windows
# Usage: powershell -ExecutionPolicy Bypass -c "irm https://raw.githubusercontent.com/zoharbabin/web-researcher-mcp/main/install.ps1 | iex"
# Options (via env vars):
#   $env:INSTALL_DIR = "C:\custom\path"  — where to put the binary
#   $env:SKIP_MCP_REGISTER = "1"         — skip registering with Claude Code
#   $env:VERSION = "1.9.0"               — install a specific version
#   $env:SKIP_CHECKSUM = "1"             — skip checksum verification (not recommended)

$ErrorActionPreference = "Stop"

$Repo = "zoharbabin/web-researcher-mcp"
$Binary = "web-researcher-mcp.exe"

function Verify-Checksum {
    param([string]$FilePath, [string]$ArchiveName, [string]$ChecksumsUrl)

    if ($env:SKIP_CHECKSUM -eq "1") {
        Write-Host "  (checksum verification skipped)"
        return
    }

    Write-Host "  Verifying checksum..."
    try {
        $Checksums = (Invoke-WebRequest -Uri $ChecksumsUrl -UseBasicParsing).Content
    } catch {
        Write-Host "  Warning: could not download checksums.txt - skipping verification."
        return
    }

    $Line = ($Checksums -split "`n") | Where-Object { $_ -match "  $([regex]::Escape($ArchiveName))$" } | Select-Object -First 1
    if (-not $Line) {
        Write-Host "  Warning: no checksum found for $ArchiveName - skipping verification."
        return
    }

    $Expected = ($Line -split "  ")[0]
    $Actual = (Get-FileHash -Path $FilePath -Algorithm SHA256).Hash.ToLower()

    if ($Actual -ne $Expected) {
        Write-Error @"
Checksum mismatch!
  Expected: $Expected
  Got:      $Actual

The download may be corrupted or tampered with.
Try again or download manually from https://github.com/$Repo/releases
"@
    }
    Write-Host "  Checksum verified."
}

function Main {
    $Arch = if ([Environment]::Is64BitOperatingSystem) { "amd64" } else {
        Write-Error "Unsupported: 32-bit Windows is not supported."
        return
    }

    $InstallDir = if ($env:INSTALL_DIR) { $env:INSTALL_DIR } else {
        Join-Path $env:LOCALAPPDATA "Programs\web-researcher-mcp"
    }

    if (-not (Test-Path $InstallDir)) {
        New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    }

    # Get version
    if ($env:VERSION) {
        $Tag = "v$($env:VERSION)"
        $Version = $env:VERSION
    } else {
        try {
            $Release = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest"
            $Tag = $Release.tag_name
            $Version = $Tag.TrimStart("v")
        } catch {
            Write-Error "Could not determine latest release (GitHub API rate limit?). Try setting `$env:VERSION = '1.9.0' first."
            return
        }
    }

    $Archive = "web-researcher-mcp_${Version}_windows_${Arch}.zip"
    $Url = "https://github.com/$Repo/releases/download/$Tag/$Archive"
    $ChecksumsUrl = "https://github.com/$Repo/releases/download/$Tag/checksums.txt"

    Write-Host "Installing web-researcher-mcp $Tag (windows/$Arch)..."
    Write-Host "  from: $Url"
    Write-Host "  to:   $InstallDir\$Binary"

    $TmpDir = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid().ToString())
    New-Item -ItemType Directory -Path $TmpDir -Force | Out-Null

    try {
        $ZipPath = Join-Path $TmpDir $Archive
        Invoke-WebRequest -Uri $Url -OutFile $ZipPath -UseBasicParsing
        Verify-Checksum -FilePath $ZipPath -ArchiveName $Archive -ChecksumsUrl $ChecksumsUrl
        Expand-Archive -Path $ZipPath -DestinationPath $TmpDir -Force
        Copy-Item (Join-Path $TmpDir $Binary) (Join-Path $InstallDir $Binary) -Force
    } finally {
        Remove-Item -Recurse -Force $TmpDir -ErrorAction SilentlyContinue
    }

    Write-Host ""
    Write-Host "Installed $Binary to $InstallDir\$Binary"

    # Add to user PATH if not already there
    $UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($UserPath -notlike "*$InstallDir*") {
        [Environment]::SetEnvironmentVariable("Path", "$InstallDir;$UserPath", "User")
        $env:Path = "$InstallDir;$env:Path"
        Write-Host "Added $InstallDir to your user PATH (takes effect in new terminals)."
    }

    # Register with Claude Code if available
    if ($env:SKIP_MCP_REGISTER -ne "1" -and (Get-Command claude -ErrorAction SilentlyContinue)) {
        Write-Host ""
        Write-Host "Registering with Claude Code..."
        & claude mcp add --scope user web-researcher -- (Join-Path $InstallDir $Binary)
        Write-Host "Done - Claude Code can now use web-researcher-mcp."
    } else {
        Write-Host ""
        Write-Host "To connect to Claude Code, run:"
        Write-Host ""
        Write-Host "  claude mcp add --scope user web-researcher -- `"$InstallDir\$Binary`""
    }

    Write-Host ""
    Write-Host "Next: set up a search provider API key."
    Write-Host "See https://github.com/$Repo#configuration"
}

Main
