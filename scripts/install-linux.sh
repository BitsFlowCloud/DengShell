#!/usr/bin/env bash
# Shipped as data/support/install.sh. Install only program/support resources,
# never copy a portable user's keys, imported assets or encrypted configuration.
set -euo pipefail
dengshell_source="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
case "${1:-}" in
  '')
    dengshell_target="${XDG_DATA_HOME:-$HOME/.local/share}/dengshell/application"
    dengshell_entries="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
    dengshell_icons="${XDG_DATA_HOME:-$HOME/.local/share}/icons/hicolor/256x256/apps"
    ;;
  --system)
    if [[ "$EUID" -ne 0 ]]; then printf '%s\n' '系统安装需 root：sudo bash data/support/install.sh --system' >&2; exit 1; fi
    dengshell_target=/opt/dengshell; dengshell_entries=/usr/share/applications; dengshell_icons=/usr/share/icons/hicolor/256x256/apps
    ;;
  *) printf '%s\n' '用法：bash data/support/install.sh [--system]' >&2; exit 1 ;;
esac
if [[ ! -f "$dengshell_source/dengshell" ]]; then printf '%s\n' '请从完整 Linux 发行包内运行此安装器。' >&2; exit 1; fi
if [[ "$dengshell_source" != "$dengshell_target" ]]; then
  mkdir -p "$dengshell_target/data"
  cp "$dengshell_source/dengshell" "$dengshell_target/dengshell.new"
  chmod 755 "$dengshell_target/dengshell.new"
  mv "$dengshell_target/dengshell.new" "$dengshell_target/dengshell"
  for dengshell_resource in docs licenses support; do
    if [[ -d "$dengshell_source/data/$dengshell_resource" ]]; then
      mkdir -p "$dengshell_target/data/$dengshell_resource"
      cp -a "$dengshell_source/data/$dengshell_resource/." "$dengshell_target/data/$dengshell_resource/"
    fi
  done
fi
mkdir -p "$dengshell_target/data/support" "$dengshell_entries" "$dengshell_icons"
printf 'installed\n' > "$dengshell_target/data/support/installed-layout"
chmod 755 "$dengshell_target/data/support/start.sh"
cp "$dengshell_target/data/support/dengshell.png" "$dengshell_icons/dengshell.png"
# Desktop Exec values use double-quoted arguments and escape reserved characters.
dengshell_exec="${dengshell_target//\\/\\\\}"; dengshell_exec="${dengshell_exec//\"/\\\"}"
dengshell_exec="${dengshell_exec//\$/\\\$}"; dengshell_exec="${dengshell_exec//\`/\\\`}"
dengshell_exec="${dengshell_exec//%/%%}"
cat > "$dengshell_entries/dengshell.desktop" <<DESKTOP
[Desktop Entry]
Type=Application
Name=DengShell
Comment=SSH 终端与服务器工作台
Exec="$dengshell_exec/data/support/start.sh"
Icon=dengshell
Terminal=false
Categories=Network;RemoteAccess;
StartupWMClass=dengshell
DESKTOP
command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database "$dengshell_entries" || true
printf '安装完成：%s\n请从应用菜单启动 DengShell。首次启动会检查 GTK3 / WebKitGTK 4.1 / ping 并提示缺失依赖。\n配置保存在每位用户的 ${XDG_CONFIG_HOME:-$HOME/.config}/dengshell，升级安装不会删除该目录。\n' "$dengshell_target"
