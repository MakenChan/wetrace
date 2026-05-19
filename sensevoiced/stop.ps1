param(
  [int]$Port = 5210
)

$ErrorActionPreference = "SilentlyContinue"
$conn = Get-NetTCPConnection -LocalPort $Port -ErrorAction SilentlyContinue
if (-not $conn) {
  Write-Host ("No process listening on port {0}." -f $Port)
  exit 0
}
foreach ($c in $conn) {
  $pidVal = $c.OwningProcess
  try {
    Stop-Process -Id $pidVal -Force -ErrorAction Stop
    Write-Host ("Stopped process PID={0} (port {1})" -f $pidVal, $Port)
  } catch {
    Write-Host ("Failed to stop PID={0}: {1}" -f $pidVal, $_)
  }
}
