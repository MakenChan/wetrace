# restart.ps1
# Restart wetrace without rebuilding.
#
# Usage:
#   powershell -ExecutionPolicy Bypass -File script\restart.ps1
#
# Notes:
#   - Optional GCC_DIR / GO_DIR from .env are prepended to PATH (this session only).
#   - Restart itself does not require GCC/Go, but we keep the same env logic for
#     easier troubleshooting.

$ErrorActionPreference = 'Stop'

. (Join-Path $PSScriptRoot '_env.ps1')

# PATH injection only; handy when inspecting failures via where.exe go / gcc.
[void](Initialize-BuildEnv)

Stop-Wetrace
Start-Wetrace
