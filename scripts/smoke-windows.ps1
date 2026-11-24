<#
.SYNOPSIS
    Windows smoke test: serve must come back after being killed.

.DESCRIPTION
    Run by the `Windows smoke test` job in .github/workflows/pr.yml.

    The bug this exists for: on Windows the install lock was the mere existence
    of rookery.pid, removed only by a clean shutdown that `serve` never performs.
    So a server killed in any way — a reboot, a closed console window, Ctrl+C,
    `schtasks /End` — left the file behind and every later start refused. The
    autostart task worked once, after install, and never again.

    A test that starts the server ONCE passes on that broken code, which is why
    the step that matters here is the restart after Stop-Process -Force: that is
    TerminateProcess, so no deferred code runs, exactly like a reboot.

    Also asserted: a second server against the same data directory is still
    refused while the first is alive, with a message naming the cause.

.PARAMETER Exe
    Path to the rookery.exe under test.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Exe
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$Exe  = (Resolve-Path $Exe).Path
$Port = 18099
$Root = Join-Path ([System.IO.Path]::GetTempPath()) ("rookery-smoke-" + [System.Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $Root -Force | Out-Null

# The same environment for every process below. ROOKERY_DATA_DIR keeps the run
# out of the runner's own profile; loopback-only so no firewall prompt is involved.
$env:ROOKERY_DATA_DIR = Join-Path $Root 'data'
$env:ROOKERY_HOST     = '127.0.0.1'
$env:ROOKERY_PORT     = "$Port"

$script:Started = @()

function Fail {
    param([string]$Message)
    Write-Host "FAIL: $Message" -ForegroundColor Red
    foreach ($log in Get-ChildItem -Path $Root -Filter '*.log' -ErrorAction SilentlyContinue) {
        Write-Host "----- $($log.Name) -----"
        Get-Content $log.FullName -ErrorAction SilentlyContinue | Write-Host
    }
    throw $Message
}

function Start-Serve {
    param([string]$Name)
    $p = Start-Process -FilePath $Exe -ArgumentList 'serve' -WorkingDirectory $Root -PassThru -NoNewWindow `
        -RedirectStandardOutput (Join-Path $Root "$Name.out.log") `
        -RedirectStandardError  (Join-Path $Root "$Name.err.log")
    # Open the process handle NOW. Without it, a process that exits before the
    # first HasExited/WaitForExit call can report a null ExitCode — and the
    # second serve below exits in well under a second by design.
    $null = $p.Handle
    $script:Started += $p
    return $p
}

function Wait-Healthy {
    param([System.Diagnostics.Process]$Proc, [int]$Seconds = 60)
    $deadline = (Get-Date).AddSeconds($Seconds)
    while ((Get-Date) -lt $deadline) {
        if ($Proc.HasExited) { Fail "serve exited with code $($Proc.ExitCode) before answering /healthz" }
        try {
            $r = Invoke-WebRequest -Uri "http://127.0.0.1:$Port/healthz" -UseBasicParsing -TimeoutSec 4
            if ($r.StatusCode -eq 200) { return }
        } catch {
            # Not listening yet.
        }
        Start-Sleep -Milliseconds 500
    }
    Fail "/healthz did not answer 200 within $Seconds s"
}

try {
    Write-Host "==> owner bootstrap"
    & $Exe owner bootstrap -u smoke -p 'smoke-pw-12345'
    if ($LASTEXITCODE -ne 0) { Fail "owner bootstrap exited $LASTEXITCODE" }

    Write-Host "==> first start"
    $first = Start-Serve 'first'
    Wait-Healthy $first
    Write-Host "    healthy (pid $($first.Id))"

    Write-Host "==> a second server must be refused while the first is alive"
    $second = Start-Serve 'second'
    if (-not $second.WaitForExit(30000)) { Fail "second serve did not exit — two servers are running on one data directory" }
    $second.WaitForExit()
    if ($second.ExitCode -eq 0) { Fail "second serve exited 0; it must refuse" }
    $err = Get-Content (Join-Path $Root 'second.err.log') -Raw
    if ($err -notmatch 'already running') { Fail "second serve's error does not name the cause: $err" }
    Write-Host "    refused: $($err.Trim())"

    Write-Host "==> hard kill (TerminateProcess, as a reboot or closed window does)"
    Stop-Process -Id $first.Id -Force
    $first.WaitForExit()
    $lock = Join-Path $env:ROOKERY_DATA_DIR 'rookery.pid'
    if (Test-Path $lock) { Write-Host "    lock file left behind, as expected: $lock" }

    Write-Host "==> restart after the kill — the regression check"
    $third = Start-Serve 'third'
    Wait-Healthy $third
    Write-Host "    healthy again (pid $($third.Id))"

    Write-Host "PASS" -ForegroundColor Green
}
finally {
    foreach ($p in $script:Started) {
        if (-not $p.HasExited) { Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue }
    }
    Remove-Item -Recurse -Force $Root -ErrorAction SilentlyContinue
}
