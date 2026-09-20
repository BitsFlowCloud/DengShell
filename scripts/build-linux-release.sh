#!/usr/bin/env bash
# Native x86-64 release, compiled against Ubuntu 22.04 (glibc 2.35).
# Does not use the host C compiler or replace its system libraries.
set -euo pipefail
dengshell_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
dengshell_output="${1:-$dengshell_root/build/linux/native}"
mkdir -p "$dengshell_output"
dengshell_output="$(cd "$dengshell_output" && pwd)"
dengshell_cache="${XDG_CACHE_HOME:-$HOME/.cache}/dengshell-linux-release"
mkdir -p "$dengshell_cache/go-build" "$dengshell_cache/go-mod" "$dengshell_cache/tmp"
export TMPDIR="$dengshell_cache/tmp"
if docker info >/dev/null 2>&1; then dengshell_docker=(docker)
elif sudo -n docker info >/dev/null 2>&1; then dengshell_docker=(sudo -n docker)
else printf '%s\n' '需要可用的 Docker 引擎以构建原生兼容发行包。请启动 Docker 后重试。' >&2; exit 1; fi
dengshell_image="${DENGSHELL_LINUX_BUILDER:-dengshell-linux-builder:jammy-go1.27.1}"
if [[ -z "${DENGSHELL_LINUX_BUILDER:-}" ]]; then
  "${dengshell_docker[@]}" build --platform linux/amd64 -t "$dengshell_image" -f "$dengshell_root/build/linux/Dockerfile" "$dengshell_root/build/linux"
fi
dengshell_cpu_args=()
if [[ -n "${DENGSHELL_BUILD_CPUSET:-}" ]]; then dengshell_cpu_args=(--cpuset-cpus "$DENGSHELL_BUILD_CPUSET"); fi
dengshell_image_id="$("${dengshell_docker[@]}" image inspect --format '{{.Id}}' "$dengshell_image")"
"${dengshell_docker[@]}" run --rm "${dengshell_cpu_args[@]}" --platform linux/amd64 --user "$(id -u):$(id -g)" \
  -v "$dengshell_root:/src:ro" -v "$dengshell_output:/out" -v "$dengshell_cache:/cache" \
  -e HOME=/cache -e GOCACHE=/cache/go-build -e GOMODCACHE=/cache/go-mod -e TMPDIR=/cache/tmp -e GOTMPDIR=/cache/tmp \
  -e DENGSHELL_BUILDER_ID="$dengshell_image_id" -e GOMAXPROCS=4 -e GOOS=linux -e GOARCH=amd64 -e GOAMD64=v1 -e CGO_ENABLED=1 \
  -w /src "$dengshell_image" bash -euo pipefail -c '
    pkg-config --exists gtk+-3.0 webkit2gtk-4.1
    go build -p 4 -buildvcs=false -trimpath -tags desktop,production,webkit2_41 -ldflags "-s -w" -o /out/dengshell.new .
    python3 /src/scripts/package-linux.py --inspect-binary /out/dengshell.new --build-info /out/build-info.json
    mv /out/dengshell.new /out/dengshell
  '
printf '原生 Linux 发行程序：%s/dengshell\n构建环境清单：%s/build-info.json\n' "$dengshell_output" "$dengshell_output"
