# Builds AI Command Center into dist/:
#   dist/ai-cmd-center.exe         server binary (from server/)
#   dist/static/management.html    dashboard (from panel/)
#   dist/config.example.yaml       config template (from deploy/)
#
# Requires `go` and `bun` on PATH. Run from any directory:
#   powershell -ExecutionPolicy Bypass -File scripts\build.ps1
$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
$dist = Join-Path $root 'dist'
$static = Join-Path $dist 'static'

foreach ($tool in 'go', 'bun') {
    if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) {
        throw "'$tool' was not found on PATH. Install it (see docs/setup.md) and retry."
    }
}

New-Item -ItemType Directory -Force -Path $static | Out-Null

Write-Host '==> Building server'
Push-Location (Join-Path $root 'server')
try {
    & go build -o (Join-Path $dist 'ai-cmd-center.exe') ./cmd/server
    if ($LASTEXITCODE -ne 0) { throw "go build failed ($LASTEXITCODE)" }
} finally {
    Pop-Location
}

Write-Host '==> Building panel'
Push-Location (Join-Path $root 'panel')
try {
    & bun install --frozen-lockfile
    if ($LASTEXITCODE -ne 0) { throw "bun install failed ($LASTEXITCODE)" }
    & bun run build
    if ($LASTEXITCODE -ne 0) { throw "bun run build failed ($LASTEXITCODE)" }
} finally {
    Pop-Location
}

Copy-Item -Force (Join-Path $root 'panel/dist/index.html') (Join-Path $static 'management.html')
Copy-Item -Force (Join-Path $root 'deploy/config.example.yaml') (Join-Path $dist 'config.example.yaml')

Write-Host "==> Done. Output in $dist"
Get-ChildItem -Recurse $dist | Select-Object FullName, Length | Format-Table -AutoSize
