param(
  [string]$BindHost = "127.0.0.1",
  [int]$Port = 5210,
  [switch]$Preload,
  [switch]$Background
)

$ErrorActionPreference = "Stop"
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $here

$py = Join-Path $here "python\python.exe"
if (-not (Test-Path $py)) {
  Write-Error "Embedded Python not found: $py. Please follow README to init first."
  exit 1
}

$argList = @(
  "server.py",
  "--host", $BindHost,
  "--port", $Port
)
if ($Preload) { $argList += "--preload" }

if ($Background) {
  $log = Join-Path $here "server.log"
  $err = Join-Path $here "server.err.log"
  if (Test-Path $log) { Remove-Item $log -Force }
  if (Test-Path $err) { Remove-Item $err -Force }
  Start-Process -FilePath $py -ArgumentList $argList `
    -RedirectStandardOutput $log -RedirectStandardError $err `
    -WindowStyle Hidden
  Write-Host ("sensevoiced started in background: http://{0}:{1}  logs: server.log / server.err.log" -f $BindHost, $Port) -ForegroundColor Cyan
} else {
  Write-Host ("Starting sensevoiced: http://{0}:{1}  (Ctrl+C to exit)" -f $BindHost, $Port) -ForegroundColor Cyan
  & $py @argList
}
