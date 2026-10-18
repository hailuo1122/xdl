# Build the XDL setup.exe. This installer has its own window and does not use NSIS.
# Prerequisite: scripts\build-alpha.ps1 has produced %LOCALAPPDATA%\XDL-dev\xdl.exe.

param(
  [string]$Version = "0.0.5-m5",
  [string]$SourceDir = "$env:LOCALAPPDATA\XDL-dev",
  [string]$OutDir = ""
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
if (-not $OutDir) { $OutDir = Join-Path $root "build" }
$stage = Join-Path $root "build\stage"
$payloadPath = Join-Path $root "cmd\setup\payload.zip"
$stubPath = Join-Path $root "cmd\setup\payload.stub.zip"

New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
New-Item -ItemType Directory -Force -Path $stage | Out-Null

$srcExe = Join-Path $SourceDir "xdl.exe"
if (-not (Test-Path $srcExe)) {
  throw "Missing $srcExe - run scripts\build-alpha.ps1 first."
}
$srcExt = Join-Path $SourceDir "extension"
if (-not (Test-Path (Join-Path $srcExt "manifest.json"))) {
  throw "Missing $srcExt\manifest.json - run scripts\build-alpha.ps1 first."
}

Write-Host "== staging =="
Copy-Item $srcExe (Join-Path $stage "xdl.exe") -Force
$stale = Join-Path $stage "extension"
if (Test-Path $stale) { Remove-Item $stale -Recurse -Force }
Copy-Item $srcExt $stale -Recurse -Force

Write-Host "== setup ui =="
$uiDir = Join-Path $root "cmd\setup\ui"
Push-Location $uiDir
try {
  if (-not (Test-Path "node_modules")) {
    pnpm install
    if ($LASTEXITCODE -ne 0) { throw "pnpm install (setup ui) failed: $LASTEXITCODE" }
  }
  pnpm run build
  if ($LASTEXITCODE -ne 0) { throw "setup ui build failed: $LASTEXITCODE" }
} finally {
  Pop-Location
}
if (-not (Test-Path (Join-Path $uiDir "dist\index.html"))) {
  throw "missing cmd\setup\ui\dist\index.html after ui build"
}

Write-Host "== uninstall.exe =="
Push-Location $root
try {
  go build -tags uninstaller -trimpath -ldflags "-H windowsgui -s -w -X main.Version=$Version" -o (Join-Path $stage "uninstall.exe") ./cmd/setup
  if ($LASTEXITCODE -ne 0) { throw "uninstall.exe build failed: $LASTEXITCODE" }
} finally {
  Pop-Location
}

Write-Host "== payload =="
Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem
$zipPath = Join-Path $stage "payload.zip"
if (Test-Path $zipPath) { Remove-Item $zipPath -Force }
$zip = [System.IO.Compression.ZipFile]::Open($zipPath, [System.IO.Compression.ZipArchiveMode]::Create)
try {
  $opt = [System.IO.Compression.CompressionLevel]::Optimal
  [void][System.IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip, (Join-Path $stage "xdl.exe"), "xdl.exe", $opt)
  [void][System.IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip, (Join-Path $stage "uninstall.exe"), "uninstall.exe", $opt)
  Get-ChildItem (Join-Path $stage "extension") -Recurse -File | ForEach-Object {
    $rel = $_.FullName.Substring((Join-Path $stage "extension").Length).TrimStart('\')
    $entry = "extension/" + ($rel -replace '\\', '/')
    [void][System.IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip, $_.FullName, $entry, $opt)
  }
} finally {
  $zip.Dispose()
}

# Keep a tiny stub in the repo; only swap in the real archive for this build.
if (-not (Test-Path $stubPath)) {
  if ((Test-Path $payloadPath) -and ((Get-Item $payloadPath).Length -lt 4096)) {
    Copy-Item $payloadPath $stubPath -Force
  } else {
    # Last-resort stub so a failed build cannot leave a huge zip in cmd/setup.
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $tmpStub = [System.IO.Compression.ZipFile]::Open($stubPath, 'Create')
    try {
      $entry = $tmpStub.CreateEntry("README.txt")
      $sw = New-Object System.IO.StreamWriter($entry.Open())
      $sw.Write("stub payload")
      $sw.Dispose()
    } finally { $tmpStub.Dispose() }
  }
}
Copy-Item $zipPath $payloadPath -Force

Write-Host "== setup.exe =="
$out = Join-Path $OutDir "XDL-$Version-setup.exe"
Push-Location $root
try {
  go build -trimpath -ldflags "-H windowsgui -s -w -X main.Version=$Version" -o $out ./cmd/setup
  if ($LASTEXITCODE -ne 0) { throw "setup.exe build failed: $LASTEXITCODE" }
} finally {
  Pop-Location
  if (Test-Path $stubPath) {
    Copy-Item $stubPath $payloadPath -Force
    Remove-Item $stubPath -Force -ErrorAction SilentlyContinue
  }
}

if (-not (Test-Path $out)) { throw "setup.exe was not written." }
$hash = (Get-FileHash $out -Algorithm SHA256).Hash
$sizeMb = [math]::Round((Get-Item $out).Length / 1MB, 2)
Write-Host ""
Write-Host "Installer written: $out"
Write-Host "Size:   $sizeMb MB"
Write-Host "SHA256: $hash"
