#!/usr/bin/env bash
set -euo pipefail
cloudshell_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$cloudshell_dir"
cloudshell_tmp="${XDG_CACHE_HOME:-$HOME/.cache}/cloudshell-build"
mkdir -p "$cloudshell_tmp"
export TMPDIR="$cloudshell_tmp" GOTMPDIR="$cloudshell_tmp"
if [[ "${1:-}" == '--macos' ]] || { [[ $# == 0 ]] && [[ "$(uname -s)" == Darwin ]]; }; then
  [[ "${1:-}" != '--macos' ]] || shift
  exec python3 scripts/build-macos.py "$@"
elif [[ "${1:-}" == '--windows' ]]; then
  mkdir -p build/windows
  GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -buildvcs=false -trimpath -tags desktop,production -ldflags '-s -w -H windowsgui' -o build/windows/DengShell.exe .
elif [[ "${1:-}" == '--browser' ]]; then
  go build -buildvcs=false -trimpath -ldflags '-s -w' -o dengshell-server .
else
  pkg-config --exists gtk+-3.0 webkit2gtk-4.1
  go build -buildvcs=false -trimpath -tags desktop,production,webkit2_41 -ldflags '-s -w' -o dengshell .
fi
