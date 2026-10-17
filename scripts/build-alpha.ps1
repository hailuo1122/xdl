param(
    [string]$OutDir = (Join-Path $env:LOCALAPPDATA "XDL-dev"),
    [switch]$SkipTests,
    [switch]$SkipExtension,
    [switch]$GenerateSyso
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    Write-Host "== frontend =="
    Push-Location frontend
    pnpm install --frozen-lockfile
    pnpm build
    Pop-Location

    if (-not $SkipExtension) {
        Write-Host "== extension =="
        Push-Location extension
        pnpm install --frozen-lockfile
        pnpm exec tsc --noEmit
        pnpm build
        Pop-Location
    }

    if ($GenerateSyso) {
        Write-Host "== windows resources =="
        if (-not (Get-Command wails3 -ErrorAction SilentlyContinue)) {
            throw "wails3 not found; install it or omit -GenerateSyso"
        }
        wails3 generate icons -input build/appicon.png -windowsfilename build/windows/icon.ico
        wails3 generate syso -arch amd64 -icon build/windows/icon.ico -info build/windows/info.json -manifest build/windows/wails.exe.manifest -out cmd/xdl/xdl_windows_amd64.syso
    }

    if (-not $SkipTests) {
        Write-Host "== go test / vet =="
        go test ./...
        if ($LASTEXITCODE -ne 0) { throw "go test failed" }
        go vet ./...
        if ($LASTEXITCODE -ne 0) { throw "go vet failed" }
    }

    Write-Host "== go build =="
    New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
    # -H windowsgui: no console window when double-clicking or launched by the extension.
    go build -ldflags="-H windowsgui" -o (Join-Path $OutDir "xdl.exe") ./cmd/xdl
    if ($LASTEXITCODE -ne 0) { throw "xdl build failed" }
    go build -o (Join-Path $OutDir "testserver.exe") ./testserver
    if ($LASTEXITCODE -ne 0) { throw "testserver build failed" }

    if (-not $SkipExtension) {
        $extDst = Join-Path $OutDir "extension"
        if (Test-Path $extDst) { Remove-Item -Recurse -Force $extDst }
        Copy-Item -Recurse (Join-Path $root "extension/.output/chrome-mv3") $extDst
        Write-Host "extension copied to $extDst"
    }

    Write-Host ""
    Write-Host "Built into $OutDir"
    Write-Host "Install / refresh the native host:"
    Write-Host "  & `"$(Join-Path $OutDir 'xdl.exe')`" --install-native-host"
    Write-Host "Then start:"
    Write-Host "  & `"$(Join-Path $OutDir 'xdl.exe')`""
    Write-Host "The app deploys the extension to %APPDATA%\XDL\extension on startup."
} finally {
    Pop-Location
}
