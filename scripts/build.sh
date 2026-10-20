#!/usr/bin/env bash
# Builds AI Command Center into dist/:
#   dist/ai-cmd-center[.exe]       server binary (from server/)
#   dist/static/management.html    dashboard (from panel/)
#   dist/config.example.yaml       config template (from deploy/)
#
# Requires `go` and `bun` on PATH. Works on macOS, Linux and Git Bash on Windows:
#   ./scripts/build.sh
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
dist="$root/dist"

for tool in go bun; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "error: '$tool' was not found on PATH. Install it (see docs/setup.md) and retry." >&2
    exit 1
  fi
done

exe=""
case "$(go env GOOS)" in
  windows) exe=".exe" ;;
esac

mkdir -p "$dist/static"

echo "==> Building server"
(cd "$root/server" && go build -o "$dist/ai-cmd-center$exe" ./cmd/server)

echo "==> Building panel"
(cd "$root/panel" && bun install --frozen-lockfile && bun run build)

cp -f "$root/panel/dist/index.html" "$dist/static/management.html"
cp -f "$root/deploy/config.example.yaml" "$dist/config.example.yaml"

echo "==> Done. Output in $dist"
ls -lR "$dist"
