# _env.ps1
# Shared helpers: parse .env at repo root, optionally prepend GCC_DIR / GO_DIR to $env:PATH.
# Dot-sourced by restart.ps1 / rebuild-restart.ps1. Not meant to be executed directly.

# --- Repo root (parent of the script dir) ---
$script:RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path

function Get-DotEnv {
    <#
    Read $RepoRoot\.env and return a hashtable.
    Supports:
      - KEY=VALUE
      - Comment lines (starting with #) and blank lines
      - Surrounding single/double quotes around VALUE are stripped
    #>
    param(
        [string]$Path = (Join-Path $script:RepoRoot '.env')
    )
    $map = @{}
    if (-not (Test-Path -LiteralPath $Path)) { return $map }
    foreach ($line in Get-Content -LiteralPath $Path -Encoding UTF8) {
        $t = $line.Trim()
        if (-not $t -or $t.StartsWith('#')) { continue }
        $eq = $t.IndexOf('=')
        if ($eq -lt 1) { continue }
        $k = $t.Substring(0, $eq).Trim()
        $v = $t.Substring($eq + 1).Trim()
        if (($v.StartsWith('"') -and $v.EndsWith('"')) -or
            ($v.StartsWith("'") -and $v.EndsWith("'"))) {
            $v = $v.Substring(1, $v.Length - 2)
        }
        $map[$k] = $v
    }
    return $map
}

function Initialize-BuildEnv {
    <#
    Prepend GCC_DIR / GO_DIR from .env to PATH; set CGO_ENABLED=1.
    Returns a callable path to go.exe (falls back to 'go' if not resolved).
    #>
    $envMap = Get-DotEnv
    $prepend = @()

    $gccDir = $envMap['GCC_DIR']
    if ($gccDir) {
        if (Test-Path -LiteralPath $gccDir) {
            $prepend += $gccDir
            Write-Host "[env] GCC_DIR -> $gccDir" -ForegroundColor DarkGray
        } else {
            Write-Warning "[env] GCC_DIR in .env does not exist: $gccDir (ignored)"
        }
    }

    $goDir = $envMap['GO_DIR']
    if ($goDir) {
        if (Test-Path -LiteralPath $goDir) {
            $prepend += $goDir
            Write-Host "[env] GO_DIR  -> $goDir" -ForegroundColor DarkGray
        } else {
            Write-Warning "[env] GO_DIR in .env does not exist: $goDir (ignored)"
        }
    }

    if ($prepend.Count -gt 0) {
        $env:PATH = ($prepend -join ';') + ';' + $env:PATH
    }

    $env:CGO_ENABLED = '1'

    # Resolve go.exe
    $goExe = $null
    if ($goDir) {
        $candidate = Join-Path $goDir 'go.exe'
        if (Test-Path -LiteralPath $candidate) { $goExe = $candidate }
    }
    if (-not $goExe) {
        $cmd = Get-Command go.exe -ErrorAction SilentlyContinue
        if ($cmd) { $goExe = $cmd.Source } else { $goExe = 'go' }
    }
    return $goExe
}

function Stop-Wetrace {
    <#
    Stop all wetrace.exe processes and wait for exit.
    #>
    $procs = Get-CimInstance Win32_Process -Filter "name='wetrace.exe'" -ErrorAction SilentlyContinue
    if (-not $procs) {
        Write-Host "[stop] no wetrace.exe is running" -ForegroundColor DarkGray
        return
    }
    foreach ($p in $procs) {
        Write-Host "[stop] killing PID=$($p.ProcessId) CmdLine=$($p.CommandLine)" -ForegroundColor Yellow
        try {
            Stop-Process -Id $p.ProcessId -Force -ErrorAction Stop
        } catch {
            Write-Warning "[stop] Stop-Process failed: $_"
        }
    }
    # wait up to 5s
    for ($i = 0; $i -lt 50; $i++) {
        Start-Sleep -Milliseconds 100
        $alive = Get-CimInstance Win32_Process -Filter "name='wetrace.exe'" -ErrorAction SilentlyContinue
        if (-not $alive) { return }
    }
    Write-Warning "[stop] wetrace.exe did not exit within 5s, port may still be held"
}

function Start-Wetrace {
    <#
    Start wetrace.exe in background with stdout/stderr redirected to logs.
    #>
    $exe = Join-Path $script:RepoRoot 'wetrace.exe'
    $log = Join-Path $script:RepoRoot 'wetrace.log'
    if (-not (Test-Path -LiteralPath $exe)) {
        throw "[start] executable not found: $exe (build first)"
    }
    Write-Host "[start] $exe (log: $log)" -ForegroundColor Cyan
    $p = Start-Process -FilePath $exe `
        -WorkingDirectory $script:RepoRoot `
        -RedirectStandardOutput $log `
        -RedirectStandardError  (Join-Path $script:RepoRoot 'wetrace.err.log') `
        -WindowStyle Hidden `
        -PassThru
    Write-Host "[start] PID=$($p.Id)" -ForegroundColor Green
    Start-Sleep -Milliseconds 800
    if ($p.HasExited) {
        Write-Warning "[start] process exited, check wetrace.log / wetrace.err.log"
    }
}

function Invoke-UIBuild {
    <#
    Run 'npm run build' in ui/ to refresh the frontend bundle under ui/dist.
    Go uses //go:embed ui/dist, so frontend changes require this step first.
    #>
    $uiDir = Join-Path $script:RepoRoot 'ui'
    if (-not (Test-Path -LiteralPath $uiDir)) {
        Write-Host "[ui] skip: ui/ directory not found" -ForegroundColor DarkGray
        return
    }
    $npmCmd = Get-Command npm.cmd -ErrorAction SilentlyContinue
    if (-not $npmCmd) { $npmCmd = Get-Command npm -ErrorAction SilentlyContinue }
    if (-not $npmCmd) {
        Write-Warning "[ui] npm not found, skipping UI build (go embed will reuse old ui/dist)"
        return
    }
    Write-Host "[ui] npm run build (cwd=$uiDir)" -ForegroundColor Cyan
    Push-Location $uiDir
    try {
        & $npmCmd.Source run build
        if ($LASTEXITCODE -ne 0) {
            throw "[ui] npm run build failed, exit=$LASTEXITCODE"
        }
        Write-Host "[ui] OK -> ui/dist" -ForegroundColor Green
    } finally {
        Pop-Location
    }
}

function Invoke-Build {
    <#
    Build wetrace.exe at repo root using go.exe from .env (or PATH).

    Params:
      -SkipUI   Skip 'npm run build', reuse existing ui/dist.
                Use when only Go code changed.

    Injects build metadata via -ldflags:
      pkg/version.BuildTime = current local ISO 8601 time
    #>
    param(
        [switch]$SkipUI
    )

    $goExe = Initialize-BuildEnv

    if (-not $SkipUI) {
        Invoke-UIBuild
    } else {
        Write-Host "[ui] skipped (-SkipUI)" -ForegroundColor DarkGray
    }

    # Build time: ISO 8601 with local timezone, e.g. 2026-05-09T09:55:12+08:00
    $buildTime = Get-Date -Format "yyyy-MM-ddTHH:mm:sszzz"
    $ldflags   = "-X 'github.com/afumu/wetrace/pkg/version.BuildTime=$buildTime'"

    Write-Host "[build] go: $goExe" -ForegroundColor Cyan
    Write-Host "[build] CGO_ENABLED=$env:CGO_ENABLED" -ForegroundColor DarkGray
    Write-Host "[build] BuildTime=$buildTime" -ForegroundColor DarkGray
    Push-Location $script:RepoRoot
    try {
        & $goExe build -ldflags $ldflags -o wetrace.exe .
        if ($LASTEXITCODE -ne 0) {
            throw "[build] go build failed, exit=$LASTEXITCODE"
        }
        Write-Host "[build] OK -> $(Join-Path $script:RepoRoot 'wetrace.exe')" -ForegroundColor Green
    } finally {
        Pop-Location
    }
}
