# Build the NSIS installer from a staging directory.
# Prerequisites: NSIS (makensis), and scripts\build-alpha.ps1 already run.
#
# IMPORTANT - why the stage directory lives inside the workspace:
# makensis cannot read sources that sit under %LOCALAPPDATA% on this machine and
# reports the misleading "File: xdl.exe -> no files found" even though the file
# exists and the shell can read it. The workspace copy works. This is the same
# Low-integrity interference the README warns about for running from D:\xdy, and
# it is why pack-nsis.ps1 re-stages into build\stage rather than reading the
# build-alpha output directory in place.
#
# Sources are referenced with relative paths and makensis runs with the stage
# directory as its working directory, which keeps the .nsi portable.

param(
  [string]$Version = "0.0.5-m5",
  [string]$SourceDir = "$env:LOCALAPPDATA\XDL-dev",
  [string]$StageDir = "",
  [string]$OutDir = ""
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
if (-not $OutDir) { $OutDir = Join-Path $root "build" }
if (-not $StageDir) { $StageDir = Join-Path $root "build\stage" }
$nsiPath = Join-Path $root "build\nsis\xdl.nsi"

# Locate makensis: PATH first, then the usual install locations.
function Resolve-Makensis {
  $cmd = Get-Command makensis -ErrorAction SilentlyContinue
  if ($cmd) { return $cmd.Source }
  $candidates = @(
    "$env:LOCALAPPDATA\Programs\NSIS\Bin\makensis.exe",
    "$env:LOCALAPPDATA\Programs\NSIS\makensis.exe",
    "${env:ProgramFiles(x86)}\NSIS\makensis.exe",
    "$env:ProgramFiles\NSIS\makensis.exe"
  )
  foreach ($c in $candidates) {
    if (Test-Path $c) { return $c }
  }
  return $null
}

$makensis = Resolve-Makensis
if (-not $makensis) {
  throw "makensis not found. Install NSIS (choco install nsis), or place Bin\makensis.exe under %LOCALAPPDATA%\Programs\NSIS."
}

New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
New-Item -ItemType Directory -Force -Path $StageDir | Out-Null
$OutDir = (Resolve-Path $OutDir).Path
$StageDir = (Resolve-Path $StageDir).Path

# Re-stage the payload next to the .nsi so makensis can open the sources.
#
# xdl.exe is staged as payload\xdl.bin on purpose: makensis will not stage a
# source whose installed name ends in .exe and fails with the misleading
# "no files found". The .nsi renames it back to xdl.exe inside the section.
Write-Host "== staging =="
$payload = Join-Path $StageDir "payload"
New-Item -ItemType Directory -Force -Path $payload | Out-Null

$srcExe = Join-Path $SourceDir "xdl.exe"
if (-not (Test-Path $srcExe)) {
  throw "Missing $srcExe - run scripts\build-alpha.ps1 first."
}
Copy-Item $srcExe (Join-Path $payload "xdl.bin") -Force
Write-Host "  payload\xdl.bin"

$srcTest = Join-Path $SourceDir "testserver.exe"
if (Test-Path $srcTest) {
  Copy-Item $srcTest (Join-Path $payload "testserver.bin") -Force
  Write-Host "  payload\testserver.bin"
}

$srcExt = Join-Path $SourceDir "extension"
if (-not (Test-Path (Join-Path $srcExt "manifest.json"))) {
  throw "Missing $srcExt\manifest.json - run scripts\build-alpha.ps1 first."
}
# Remove any stale extension payload so a removed source file cannot linger.
$stale = Join-Path $StageDir "extension"
if (Test-Path $stale) { Remove-Item $stale -Recurse -Force }
Copy-Item $srcExt $stale -Recurse -Force
$extFiles = (Get-ChildItem $stale -Recurse -File).Count
Write-Host "  extension ($extFiles files)"

Write-Host ""
Write-Host "makensis: $makensis"
Write-Host "stage:     $StageDir"
Write-Host "output:    $OutDir"

# makensis resolves relative File sources against its *process* working
# directory. PowerShell's Push-Location only moves the PS session's cwd and is
# not reliably inherited by the child process, which surfaces as the very
# misleading "File: payload\xdl.bin -> no files found".
#
# The fix that works everywhere: copy the .nsi into the stage directory and
# give it inlined OUTDIR/VERSION defines, then run makensis with that copy as
# the script argument. No /D flags and no cwd inheritance needed.
$buildNsi = Join-Path $StageDir "xdl-build.nsi"
$nsiText = Get-Content $nsiPath -Raw
$nsiText = $nsiText -replace '(?m)^!ifndef OUTDIR\s*\r?\n\s*!define OUTDIR "\."\s*\r?\n!endif', ''
$nsiText = $nsiText -replace '(?m)^!ifndef VERSION\s*\r?\n\s*!define VERSION "[^"]*"\s*\r?\n!endif', ''
$nsiText = "Unicode true`r`n!define OUTDIR `"$OutDir`"`r`n!define VERSION `"$Version`"`r`n" + $nsiText
# Drop the now-duplicated leading `Unicode true` from the original body.
$nsiText = $nsiText -replace '(?m)^Unicode true\r?\n\r?\n(?=Name "XDL")', ''
Set-Content -Path $buildNsi -Value $nsiText -Encoding UTF8

Push-Location $StageDir
try {
  & $makensis "xdl-build.nsi"
  if ($LASTEXITCODE -ne 0) { throw "makensis failed: $LASTEXITCODE" }
} finally {
  Pop-Location
  Remove-Item $buildNsi -Force -ErrorAction SilentlyContinue
}

$out = Join-Path $OutDir "XDL-$Version-setup.exe"
if (-not (Test-Path $out)) { throw "makensis reported success but $out is missing." }
$hash = (Get-FileHash $out -Algorithm SHA256).Hash
$sizeMb = [math]::Round((Get-Item $out).Length / 1MB, 2)
Write-Host ""
Write-Host "Installer written: $out"
Write-Host "Size:   $sizeMb MB"
Write-Host "SHA256: $hash"
