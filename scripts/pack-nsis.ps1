# Build NSIS installer from the staged alpha payload.
# Prerequisites: NSIS (makensis on PATH), scripts\build-alpha.ps1 already run.

param(
  [string]$Version = "0.0.5-m5",
  [string]$StageDir = "$env:LOCALAPPDATA\XDL-dev",
  [string]$OutDir = "$env:LOCALAPPDATA\XDL-dev"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$nsi = Join-Path $root "build\nsis\xdl.nsi"
$exe = Join-Path $StageDir "xdl.exe"

if (-not (Test-Path $exe)) {
  throw "Missing $exe — run scripts\build-alpha.ps1 first."
}
if (-not (Get-Command makensis -ErrorAction SilentlyContinue)) {
  throw "makensis not found. Install NSIS (winget install NSIS.NSIS)."
}

New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
& makensis "/DVERSION=$Version" "/DSTAGE_DIR=$StageDir" "/DOUTDIR=$OutDir" $nsi
if ($LASTEXITCODE -ne 0) { throw "makensis failed: $LASTEXITCODE" }
Write-Host "Installer written under $OutDir"
