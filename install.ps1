<#
.SYNOPSIS
    Rookery installer for Windows.

.DESCRIPTION
    irm https://rookery.cloud/install.ps1 | iex

    Puts a verified rookery.exe on your PATH and tells you what to run next. It
    does not bootstrap an owner, create a workspace or start a service —
    `rookery onboard` does all of that, in Go, where it is written once and
    tested rather than twice in shell and PowerShell.

    `iex` cannot pass arguments to what it runs, so the parameters below are
    unreachable through the advertised one-liner. To use one, build a script
    block instead:

        & ([scriptblock]::Create((irm https://rookery.cloud/install.ps1))) -Version v0.2.0

.PARAMETER Version
    Install this tag instead of the latest release.

.PARAMETER BinDir
    Install here instead of %LOCALAPPDATA%\Programs\Rookery.

.PARAMETER Yes
    Answer yes to the host-tool prompt (non-interactive installs).

.PARAMETER NoTools
    Skip the host-tool step entirely.
#>
[CmdletBinding()]
param(
    [string]$Version = $env:ROOKERY_VERSION,
    [string]$BinDir  = $env:ROOKERY_BIN_DIR,
    [switch]$Yes,
    [switch]$NoTools
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$Repo = 'rookery-ai/rookery'
$script:ToolsPrompted = $false

function Write-Step { param([string]$Message) Write-Host "==> " -ForegroundColor Cyan -NoNewline; Write-Host $Message }
function Write-Warn { param([string]$Message) Write-Host " warn " -ForegroundColor Yellow -NoNewline; Write-Host $Message }
function Write-Ok   { param([string]$Message) Write-Host "  $Message" -ForegroundColor Green }

# `throw`, never `exit`. This script is advertised as `irm ... | iex`, which
# runs it in the CALLER's session rather than a child scope — and `exit` there
# terminates the whole PowerShell session, closing the window and taking the
# error message with it. A checksum mismatch is precisely when the user needs
# to read what happened, so the failure path must not be the one that hides it.
# With $ErrorActionPreference = 'Stop' a throw ends the script and leaves the
# session alive.
# The detail is written out first and the thrown text is a one-line summary:
# throwing the full message would print a multi-line here-string twice, once as
# our own formatted output and again as the exception's.
function Stop-WithError {
    param([string]$Message)
    Write-Host "error " -ForegroundColor Red -NoNewline
    Write-Host $Message
    throw "rookery install failed (see the message above)"
}

# This message used to lead with the repository being private, which was the
# overwhelming cause while it was. The repository is public now, so that is the
# one thing a 404 can no longer mean — and leading with it pointed users away
# from the causes that remain. The platform case is first because it is the
# only one the script itself can already name precisely.
function Stop-DownloadFailed {
    param([string]$What)
    Stop-WithError @"
could not download $What

This usually means one of:
  - the requested tag has no release asset for windows/$Arch.
    Check the releases page below for which platforms that tag published.
  - the network or a proxy blocked the request.
  - the tag does not exist, or its release is still a draft.

Releases: https://github.com/$Repo/releases
"@
}

# ── platform ─────────────────────────────────────────────────────────────────

$Arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    default {
        # A 32-bit PowerShell on a 64-bit host reports x86; the host's real
        # architecture is the one that matters for which binary to fetch.
        if ($env:PROCESSOR_ARCHITEW6432 -eq 'ARM64') { 'arm64' }
        elseif ($env:PROCESSOR_ARCHITEW6432 -eq 'AMD64') { 'amd64' }
        else { Stop-WithError "unsupported architecture '$($env:PROCESSOR_ARCHITECTURE)'. Rookery ships amd64 and arm64." }
    }
}

# ── host tools ───────────────────────────────────────────────────────────────
#
# Rookery runs without any of these, but degrades in ways that are easy to
# misread as bugs. python3 is the sharpest: without it the agent-tool AST
# guardrail self-skips, so generated tool scripts run unchecked. /healthz
# reports all four at runtime.
#
# All four are on winget, so Windows reaches full feature coverage without
# Chocolatey or Scoop. Poppler is the one whose package id is not obvious —
# oschwartz10612.Poppler is the maintained Windows build, and it is what ships
# pdftotext.exe.
#
# `Names` is a list, not a single command, because python.org's distribution —
# which is what Python.Python.3.13 installs — ships python.exe and py.exe and no
# python3.exe. Probing one spelling is what let this script and `rookery
# onboard` disagree about whether Python was installed at all.
$HostTools = @(
    @{ Names = @('python', 'python3', 'py'); Winget = 'Python.Python.3.13';       Purpose = 'agent-tool AST guardrail (without it, generated scripts run unchecked)'; Verify = $true }
    @{ Names = @('rg');                      Winget = 'BurntSushi.ripgrep.MSVC';  Purpose = 'knowledge-base search' }
    @{ Names = @('pdftotext');               Winget = 'oschwartz10612.Poppler';   Purpose = 'PDF text extraction' }
    @{ Names = @('tesseract');               Winget = 'UB-Mannheim.TesseractOCR'; Purpose = 'OCR for scanned documents and images' }
)

function Test-Command {
    param([string]$Name)
    $null -ne (Get-Command $Name -ErrorAction SilentlyContinue)
}

# Stock Windows ships an App Execution Alias at
# %LOCALAPPDATA%\Microsoft\WindowsApps\python3.exe which Get-Command resolves
# happily and which opens the Microsoft Store instead of running Python. So a
# tool marked Verify is proved by RUNNING it: existence alone would let this
# script decide Python is present and skip installing it, leaving the AST
# guardrail disabled with nothing said. internal/onboard applies the same rule,
# for the same reason.
function Test-Tool {
    param([hashtable]$Tool)
    foreach ($name in $Tool.Names) {
        if (-not (Test-Command $name)) { continue }
        if (-not $Tool.Verify) { return $true }
        try {
            $out = & $name --version 2>&1
            if ($LASTEXITCODE -eq 0 -and "$out" -match 'Python') { return $true }
        } catch {
            # A stub that refuses to run is not the tool.
        }
    }
    return $false
}

function Install-HostTools {
    if ($NoTools -or $env:ROOKERY_NO_TOOLS -eq '1') { return }

    $missing = @($HostTools | Where-Object { -not (Test-Tool $_) })
    if ($missing.Count -eq 0) {
        Write-Step "Host tools: all present"
        return
    }

    Write-Step "Host tools"
    Write-Host "  Rookery runs without these, but some features quietly degrade:"
    foreach ($t in $missing) {
        Write-Host ("    {0,-10} {1}" -f $t.Names[0], $t.Purpose)
    }
    Write-Host ""

    if (-not (Test-Command 'winget')) {
        Write-Warn "winget not found. Install App Installer from the Microsoft Store, then:"
        foreach ($t in $missing) { Write-Host "    winget install -e --id $($t.Winget)" }
        $script:ToolsPrompted = $true
        return
    }

    $ids = ($missing | ForEach-Object { $_.Winget }) -join ', '

    # A script piped from the network into a shell must not install four
    # packages on its own initiative. Ask — unless there is no console to ask
    # on, in which case print the commands and move on rather than hanging.
    $answer = 'n'
    if ($Yes -or $env:ROOKERY_YES -eq '1') {
        $answer = 'y'
    } elseif ([Environment]::UserInteractive -and -not [Console]::IsInputRedirected) {
        $answer = Read-Host "  Install $ids via winget? [y/N]"
    } else {
        foreach ($t in $missing) { Write-Host "    winget install -e --id $($t.Winget)" }
        $script:ToolsPrompted = $true
        return
    }

    if ($answer -notmatch '^(y|yes)$') {
        Write-Host "  Skipped. Later:"
        foreach ($t in $missing) { Write-Host "    winget install -e --id $($t.Winget)" }
        $script:ToolsPrompted = $true
        return
    }

    foreach ($t in $missing) {
        Write-Host "  installing $($t.Winget)..."
        # --accept-*-agreements keeps winget from blocking on a prompt this
        # script has no way to answer.
        & winget install -e --id $t.Winget --accept-source-agreements --accept-package-agreements --silent
        if ($LASTEXITCODE -ne 0) {
            Write-Warn "winget failed for $($t.Winget) — run it yourself: winget install -e --id $($t.Winget)"
        }
    }
    # winget writes its shims to a directory the CURRENT process resolved PATH
    # from before they existed, so a freshly installed tool is typically not
    # findable until a new shell. Saying so up front prevents a "it says it
    # installed but rookery can't see it" report.
    Write-Warn "newly installed tools appear on PATH in a NEW terminal, not this one."
}

# ── release resolution ───────────────────────────────────────────────────────

if (-not $Version) {
    Write-Step "Resolving the latest release"
    try {
        $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -UseBasicParsing
        $Version = $release.tag_name
    } catch {
        Stop-DownloadFailed "the release list"
    }
}
if (-not $Version) { Stop-DownloadFailed "the release list" }

# Release tags carry a leading v; archive names do not.
$num     = $Version -replace '^v', ''
$archive = "rookery_${num}_windows_${Arch}.zip"
$base    = "https://github.com/$Repo/releases/download/$Version"

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("rookery-install-" + [System.Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp -Force | Out-Null

try {
    Write-Step "Downloading rookery $Version (windows/$Arch)"
    $zip = Join-Path $tmp $archive
    try {
        Invoke-WebRequest -Uri "$base/$archive" -OutFile $zip -UseBasicParsing
    } catch {
        Stop-DownloadFailed $archive
    }

    # Verify against the release's own checksums.txt. This catches a truncated
    # download and a tampered mirror; it is not a substitute for the cosign
    # signature, which is what proves the release itself is ours. Running
    # `rookery version` below is the end-to-end check that the bytes execute.
    $sums = Join-Path $tmp 'checksums.txt'
    $haveSums = $true
    try {
        Invoke-WebRequest -Uri "$base/checksums.txt" -OutFile $sums -UseBasicParsing
    } catch {
        $haveSums = $false
        Write-Warn "checksums.txt not published for $Version — installing unverified"
    }
    if ($haveSums) {
        Write-Step "Verifying checksum"
        $got  = (Get-FileHash -Path $zip -Algorithm SHA256).Hash.ToLowerInvariant()
        $line = Get-Content $sums | Where-Object { $_ -match "\s$([regex]::Escape($archive))$" } | Select-Object -First 1
        if (-not $line) { Stop-WithError "checksums.txt has no entry for $archive" }
        $want = ($line -split '\s+')[0].ToLowerInvariant()
        if ($got -ne $want) {
            Stop-WithError "checksum mismatch for $archive - refusing to install`n  expected $want`n  got      $got"
        }
        Write-Ok "ok"
    }

    Write-Step "Extracting"
    Expand-Archive -Path $zip -DestinationPath $tmp -Force
    $exe = Join-Path $tmp 'rookery.exe'
    if (-not (Test-Path $exe)) { Stop-WithError "archive does not contain rookery.exe" }

    if (-not $BinDir) { $BinDir = Join-Path $env:LOCALAPPDATA 'Programs\Rookery' }
    New-Item -ItemType Directory -Path $BinDir -Force | Out-Null

    Write-Step "Installing to $BinDir"
    $dest = Join-Path $BinDir 'rookery.exe'
    try {
        Move-Item -Path $exe -Destination $dest -Force
    } catch {
        Stop-WithError "cannot write to $BinDir. It may be in use — stop a running rookery and retry, or set -BinDir."
    }

    $installed = & $dest version 2>$null | Select-Object -First 1
    if (-not $installed) { Stop-WithError "installed binary did not run - the download may be for the wrong platform" }
    Write-Ok $installed

    # Persist on the USER PATH, not the machine one: this installer does not
    # require administrator rights and must not start.
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if ($userPath -notlike "*$BinDir*") {
        Write-Step "Adding $BinDir to your PATH"
        $newPath = if ([string]::IsNullOrEmpty($userPath)) { $BinDir } else { "$userPath;$BinDir" }
        [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
        # Also update THIS session, so the next line of advice actually works.
        $env:Path = "$env:Path;$BinDir"
        Write-Warn "PATH updated. Other open terminals will not see it until restarted."
    }

    Install-HostTools

    Write-Host ""
    Write-Step "Done"
    Write-Host ""
    Write-Host "Next, set up your install - owner account, keys, first workspace and coder:"
    Write-Host ""
    Write-Host "    rookery onboard" -ForegroundColor White
    Write-Host ""
    if ($script:ToolsPrompted) {
        Write-Host "onboard will offer the skipped host tools again." -ForegroundColor DarkGray
    }
}
finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
