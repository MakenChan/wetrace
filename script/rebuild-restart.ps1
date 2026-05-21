# rebuild-restart.ps1
# Rebuild wetrace.exe, then restart.
#
# Usage:
#   powershell -ExecutionPolicy Bypass -File script\rebuild-restart.ps1
#   powershell -ExecutionPolicy Bypass -File script\rebuild-restart.ps1 -SkipUI
#
# Params:
#   -SkipUI  Skip 'npm run build', reuse existing ui/dist. Use when only Go
#            code changed.
#
# Optional .env keys:
#   GCC_DIR  : directory containing gcc (e.g. C:\msys64\ucrt64\bin) for cgo
#   GO_DIR   : Go install bin dir (containing go.exe), e.g. C:\Program Files\Go\bin
#   Both optional; if unset, the current user's default PATH is used.
#   When set, they are prepended to $env:PATH (this session only).

[CmdletBinding()]
param(
    [switch]$SkipUI
)

$ErrorActionPreference = 'Stop'

. (Join-Path $PSScriptRoot '_env.ps1')

Stop-Wetrace
Invoke-Build -SkipUI:$SkipUI
Start-Wetrace
