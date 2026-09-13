#!/usr/bin/env bash
set -euo pipefail
cloudshell_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$cloudshell_dir"

dengshell_problem() {
  local message="$1"
  printf '%s\n' "$message" >&2
  if [[ -n "${DISPLAY:-}" || -n "${WAYLAND_DISPLAY:-}" ]]; then
    if command -v zenity >/dev/null 2>&1; then
      if zenity --error --title='DengShell 启动检查' --no-markup --width=680 --text="$message" 2>/dev/null; then return; fi
    fi
    if command -v kdialog >/dev/null 2>&1; then
      if kdialog --title 'DengShell 启动检查' --error "$message" 2>/dev/null; then return; fi
    fi
    if command -v xmessage >/dev/null 2>&1; then
      xmessage -center -title 'DengShell 启动检查' "$message" 2>/dev/null || true
    fi
  fi
}

dengshell_ldd_details() {
  local line count=0
  while IFS= read -r line; do
    [[ "$line" == *'not found'* ]] || continue
    if (( count == 20 )); then
      printf '%s\n' '……其余缺失项请在终端运行 ldd ./dengshell 查看。'
      break
    fi
    printf '%s\n' "$line"
    count=$((count + 1))
  done <<< "$1"
}

dengshell_install_guide() {
  local key value distro='' family='' version='' gtk='' webkit='' libc='' manager='' prefix=''
  local dengshell_os_release=/etc/os-release
  if [[ -r "$dengshell_os_release" ]]; then
    while IFS='=' read -r key value; do
      value="${value#\"}"; value="${value%\"}"
      value="${value#\'}"; value="${value%\'}"
      case "$key" in
        ID) distro="$value" ;;
        ID_LIKE) family="$value" ;;
        VERSION_ID) version="$value" ;;
      esac
    done < "$dengshell_os_release"
  fi
  case " $distro $family " in
    *' ubuntu '*|*' debian '*)
      gtk=libgtk-3-0
      if command -v apt-cache >/dev/null 2>&1 && apt-cache show libgtk-3-0t64 >/dev/null 2>&1; then
        gtk=libgtk-3-0t64
      elif [[ "$version" =~ ^([0-9]+) ]]; then
        if [[ "$distro" == ubuntu ]] && (( 10#${BASH_REMATCH[1]} >= 24 )); then gtk=libgtk-3-0t64; fi
        if [[ "$distro" == debian ]] && (( 10#${BASH_REMATCH[1]} >= 13 )); then gtk=libgtk-3-0t64; fi
      fi
      webkit=libwebkit2gtk-4.1-0; libc=libc-bin; manager='sudo apt install'; prefix=$'sudo apt update\n'
      ;;
    *' fedora '*|*' rhel '*|*' centos '*)
      gtk=gtk3; webkit=webkit2gtk4.1; libc=glibc-common; manager='sudo dnf install'
      ;;
    *' arch '*)
      gtk=gtk3; webkit=webkit2gtk-4.1; libc=glibc; manager='sudo pacman -S --needed'
      ;;
    *' opensuse '*|*' suse '*|*' opensuse-tumbleweed '*|*' opensuse-leap '*)
      gtk=libgtk-3-0; webkit=libwebkit2gtk-4_1-0; libc=glibc; manager='sudo zypper install'
      ;;
  esac
  if [[ "$distro" == alpine ]]; then
    printf '%s' $'此发行包使用 glibc，不支持 Alpine 的 musl 运行库。请使用基于 glibc 的 Linux 桌面发行版。不要在 Alpine 中安装不匹配的兼容 libc 来替代完整桌面环境。'
    return
  fi
  if [[ " $distro $family " == *' rhel '* || "$distro" == centos ]]; then
    if [[ "$version" =~ ^([0-9]+) ]] && (( 10#${BASH_REMATCH[1]} < 10 )); then
      printf '%s' $'当前 RHEL / Rocky / AlmaLinux / CentOS 8、9 默认仓库缺少本包所需的 WebKitGTK 4.1。请使用兼容的较新桌面发行版或在目标环境重新适配源码构建。不要混用其他发行版的基础库。'
      return
    fi
  fi
  if [[ -z "$manager" ]]; then
    printf '%s' $'请通过当前发行版的软件包管理器安装缺失组件：GTK 3、WebKitGTK 4.1、ping（iputils），以及提供 ldd 的 libc 工具包。\n\n若软件源没有 WebKitGTK 4.1，请使用适配当前发行版的 DengShell 发行包，或在本机依照项目构建说明编译。WebKitGTK 4.0 / 6.0 不能直接替代 4.1。'
    return
  fi
  local packages=()
  if [[ "$dengshell_need_gui" == true ]]; then packages+=("$gtk" "$webkit"); fi
  if [[ "$dengshell_need_ldd" == true ]]; then packages+=("$libc"); fi
  if [[ "$dengshell_need_ping" == true ]]; then
    if [[ "$manager" == 'sudo apt install' ]]; then packages+=(iputils-ping); else packages+=(iputils); fi
  fi
  printf '当前发行版：%s %s\n可在终端执行：\n%s%s %s\n\n若软件源没有这些包，请使用匹配发行版的程序包或在本机重新构建；不要用其他版本的 WebKitGTK 代替 4.1。' "${distro:-未知}" "$version" "$prefix" "$manager" "${packages[*]}"
}

# An extracted native release runs without a Go installation. Source checkouts
# retain the existing convenience of building when the application is absent.
if [[ ! -f ./dengshell ]]; then
  if [[ -x ./build.sh ]]; then
    ./build.sh
  else
    dengshell_problem $'找不到 DengShell 程序文件。\n\n请重新解压完整的 Linux x64 发行包，将 start.sh 与 dengshell 放在同一目录。已编译的发行包不需要安装 Go。'
    exit 1
  fi
elif [[ ! -x ./dengshell ]]; then
  if ! chmod u+x ./dengshell; then
    dengshell_problem $'无法设置 DengShell 的执行权限。\n\n请把完整发行包复制到可写目录，执行 chmod +x dengshell start.sh 后重新启动。'
    exit 1
  fi
fi

dengshell_config=''
dengshell_args=("$@")
for ((dengshell_i=0; dengshell_i<${#dengshell_args[@]}; dengshell_i++)); do
  dengshell_arg="${dengshell_args[dengshell_i]}"
  case "$dengshell_arg" in
    --) break ;;
    -config|--config)
      if (( dengshell_i + 1 < ${#dengshell_args[@]} )); then
        dengshell_i=$((dengshell_i + 1))
        dengshell_config="${dengshell_args[dengshell_i]}"
      fi
      ;;
    -config=*|--config=*) dengshell_config="${dengshell_arg#*=}" ;;
    -addr|--addr)
      # Match Go flag parsing: string-valued flags consume the following token.
      if (( dengshell_i + 1 < ${#dengshell_args[@]} )); then dengshell_i=$((dengshell_i + 1)); fi
      ;;
    -h|--help|-help) exec ./dengshell "$@" ;;
    -*) ;;
    *) break ;;
  esac
done
if [[ -z "$dengshell_config" ]]; then
  if [[ -f "$cloudshell_dir/data/support/installed-layout" ]]; then
    dengshell_config="${XDG_CONFIG_HOME:-$HOME/.config}/dengshell"
    # Put the default before user arguments, including a Go -- terminator.
    # An explicit --config is detected above and always remains authoritative.
    set -- --config "$dengshell_config" "$@"
  else
    dengshell_config="$cloudshell_dir/data"
  fi
fi
case "$(uname -m)" in
  x86_64|amd64) dengshell_arch=amd64 ;;
  aarch64|arm64) dengshell_arch=arm64 ;;
  i386|i486|i586|i686) dengshell_arch=386 ;;
  armv6l|armv7l) dengshell_arch=arm ;;
  *) dengshell_arch="$(uname -m)" ;;
esac
dengshell_marker="$dengshell_config/runtime-success-v1-linux-$dengshell_arch"
if [[ -f "$dengshell_marker" ]] && cmp -s -- "$dengshell_marker" <(printf 'DengShell runtime ready v1\n'); then
  exec ./dengshell "$@"
fi

dengshell_issues=''
dengshell_need_gui=false
dengshell_need_ldd=false
dengshell_need_ping=false
# The desktop ELF still links GTK when --browser is passed, so its loader
# dependencies must be checked in either mode.
if ! command -v ldd >/dev/null 2>&1; then
  dengshell_issues+=$'缺少 ldd，无法检查程序所需的共享库。\n'
  dengshell_need_ldd=true
else
  dengshell_ldd_status=0
  dengshell_ldd="$(LC_ALL=C ldd ./dengshell 2>&1)" || dengshell_ldd_status=$?
  dengshell_ldd_detail="$(dengshell_ldd_details "$dengshell_ldd")"
  if [[ "$dengshell_ldd" == *'version '* && "$dengshell_ldd" == *'not found'* && ( "$dengshell_ldd" == *GLIBC_* || "$dengshell_ldd" == *GLIBCXX_* || "$dengshell_ldd" == *CXXABI_* ) ]]; then
    dengshell_problem $'此 DengShell 发行包与当前系统的基础运行库版本不兼容。\n\n请使用适用于当前发行版的程序包，或在当前系统重新构建。安装 GTK / WebKit 不能解决此版本差异，请勿手工替换系统 libc。\n\n检查详情：\n'"$dengshell_ldd_detail"
    exit 1
  elif [[ "$dengshell_ldd" == *'not found'* ]]; then
    dengshell_issues+=$'缺少 GTK / WebKit 或其他共享库：\n'"$dengshell_ldd_detail"$'\n'
    dengshell_need_gui=true
  elif (( dengshell_ldd_status != 0 )) && [[ "$dengshell_ldd" != *'not a dynamic executable'* && "$dengshell_ldd" != *'statically linked'* ]]; then
    dengshell_problem $'无法验证 DengShell 的本机运行库。\n\n请确认下载的是当前系统及 CPU 架构对应的 Linux 发行包，并重新解压后启动。\n\n检查详情：\n'"$dengshell_ldd"
    exit 1
  fi
fi
if ! command -v ping >/dev/null 2>&1; then
  dengshell_issues+=$'缺少 ping，无法进行 ICMP 网络检测。\n'
  dengshell_need_ping=true
fi
# MTR is optional at startup. The route-diagnostics dialog detects its absence
# and offers the appropriate installation flow when that feature is requested.
if [[ -n "$dengshell_issues" ]]; then
  dengshell_problem $'DengShell 首次启动检查未通过，应用尚未启动。\n\n'"$dengshell_issues"$'\n'"$(dengshell_install_guide)"$'\n\n安装后重新运行 start.sh。已编译的发行包不需要安装 Go。'
  exit 1
fi

# The successful native frontend callback is the only writer of this marker.
# A passing preflight or launching in browser mode must not record success.
exec ./dengshell "$@"
