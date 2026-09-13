#!/usr/bin/env python3
"""Package a native Ubuntu-22.04-baseline ELF as DEB/RPM/portable tar.

Integration: --stage <directory containing dengshell and data> --release 20
Output names are stable; linux-packages.json records hashes and ABI evidence.
Build the ELF first using scripts/build-linux-release.sh. No browser fallback.
"""
from pathlib import Path
import argparse, hashlib, json, os, re, shutil, subprocess, tarfile, tempfile

ROOT = Path(__file__).resolve().parent.parent
CACHE = Path(os.environ.get('XDG_CACHE_HOME', str(Path.home() / '.cache'))) / 'dengshell-linux-release'
CACHE.mkdir(parents=True, exist_ok=True)
os.environ['TMPDIR'] = str(CACHE)
os.environ['DPKG_DEB_THREADS_MAX'] = '2'


def output(command):
    return subprocess.check_output(command, text=True, stderr=subprocess.STDOUT).strip()


def sha(path):
    with path.open('rb') as stream:
        digest = hashlib.sha256()
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(chunk)
        return digest.hexdigest()


def inspect(binary):
    headers = output(['readelf', '-h', str(binary)])
    if 'ELF64' not in headers or 'X86-64' not in headers:
        raise SystemExit('发行程序必须是 Linux x86-64 ELF。')
    symbols = output(['readelf', '--version-info', str(binary)])
    versions = sorted(set(re.findall(r'GLIBC_(\d+(?:\.\d+)+)', symbols)), key=lambda s: tuple(map(int, s.split('.'))))
    if not versions or tuple(map(int, versions[-1].split('.'))) > (2, 35):
        raise SystemExit('GLIBC ABI 超出 2.35 基线：' + ', '.join(versions))
    dynamic = output(['readelf', '-d', str(binary)])
    needed = re.findall(r'Shared library: \[(.*?)\]', dynamic)
    if not {'libgtk-3.so.0', 'libwebkit2gtk-4.1.so.0'}.issubset(needed):
        raise SystemExit('不是预期的 GTK3 + WebKitGTK 4.1 原生桌面程序。')
    if re.search(r'\((?:RUNPATH|RPATH)\)', dynamic):
        raise SystemExit('发行程序不应含构建机 RPATH。')
    info = {'schemaVersion': 1, 'architecture': 'amd64', 'nativeDesktop': True,
            'baseline': 'Ubuntu 22.04 / glibc 2.35 / GTK 3 / WebKitGTK 4.1',
            'requiredGLIBC': versions[-1], 'glibcSymbols': versions, 'neededLibraries': needed,
            'sha256': sha(binary), 'bytes': binary.stat().st_size}
    return info


def docker_command():
    for command in [['docker'], ['sudo', '-n', 'docker']]:
        if subprocess.run(command + ['info'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
            return command
    raise SystemExit('需要 Docker 运行已构建的 Linux 打包环境。')


def copy(src, dst):
    dst.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(src, dst)


def installed_tree(portable, target):
    shutil.copytree(portable, target / 'opt/dengshell')
    (target / 'opt/dengshell/data/support/installed-layout').write_text('installed\n')
    entry = target / 'usr/share/applications/dengshell.desktop'
    entry.parent.mkdir(parents=True)
    entry.write_text('[Desktop Entry]\nType=Application\nName=DengShell\nComment=SSH 终端与服务器工作台\nExec=/opt/dengshell/data/support/start.sh\nIcon=dengshell\nTerminal=false\nCategories=Network;RemoteAccess;\nStartupWMClass=dengshell\n')
    copy(ROOT / 'build/dengshell.png', target / 'usr/share/icons/hicolor/256x256/apps/dengshell.png')
    copy(ROOT / 'LICENSE', target / 'usr/share/doc/dengshell/copyright')
    command = target / 'usr/bin/dengshell'
    command.parent.mkdir(parents=True)
    command.write_text('#!/bin/sh\nexec /opt/dengshell/data/support/start.sh "$@"\n')
    command.chmod(0o755)


def package(args):
    stage, out = args.stage.resolve(), args.output.resolve()
    if sorted(p.name for p in stage.iterdir()) != ['data', 'dengshell']:
        raise SystemExit('便携目录顶层只能包含 dengshell 和 data。')
    if any(p.name not in ['docs', 'licenses', 'support'] for p in (stage / 'data').iterdir()):
        raise SystemExit('打包输入含非发行资源；请排除用户配置、密钥与导入素材。')
    info = inspect(stage / 'dengshell')
    build_info_path = ROOT / 'build/linux/native/build-info.json'
    if not build_info_path.exists():
        raise SystemExit('缺少基线构建记录，请先运行 scripts/build-linux-release.sh。')
    build_info = json.loads(build_info_path.read_text())
    if build_info.get('sha256') != info['sha256']:
        raise SystemExit('程序与兼容基线构建记录不一致；禁止包装本机重新编译的替代程序。')
    out.mkdir(parents=True, exist_ok=True)
    docker = docker_command()
    image = os.environ.get('DENGSHELL_LINUX_BUILDER', 'dengshell-linux-builder:jammy-go1.27.1')
    with tempfile.TemporaryDirectory(prefix='package-', dir=CACHE) as temp:
        work = Path(temp)
        portable = work / 'DengShell-linux-x64'
        shutil.copytree(stage, portable)
        support = portable / 'data/support'
        support.mkdir(parents=True, exist_ok=True)
        launcher = (ROOT / 'start.sh').read_text().replace('$(dirname -- "${BASH_SOURCE[0]}")', '$(dirname -- "${BASH_SOURCE[0]}")/../..', 1)
        (support / 'start.sh').write_text(launcher)
        (support / 'start.sh').chmod(0o755)
        copy(ROOT / 'scripts/install-linux.sh', support / 'install.sh')
        (support / 'install.sh').chmod(0o755)
        copy(ROOT / 'build/dengshell.png', support / 'dengshell.png')
        copy(ROOT / 'build/linux/README.txt', portable / 'data/docs/Linux-安装说明.txt')
        copy(build_info_path, portable / 'data/docs/linux-build-info.json')
        if (ROOT / 'build/linux/COMPATIBILITY.json').exists():
            copy(ROOT / 'build/linux/COMPATIBILITY.json', portable / 'data/docs/Linux-兼容验证.json')
        if (ROOT / 'build/linux/WEBKIT-FONT-VALIDATION.json').exists() and json.loads((ROOT / 'build/linux/WEBKIT-FONT-VALIDATION.json').read_text()).get('testedExecutableSHA256') == info['sha256']:
            copy(ROOT / 'build/linux/WEBKIT-FONT-VALIDATION.json', portable / 'data/docs/WebKit-字体验证.json')
        (support / 'installed-layout').unlink(missing_ok=True)
        tar = out / 'DengShell-linux-x64.tar.gz'
        with tarfile.open(tar, 'w:gz', compresslevel=6) as archive:
            def normalized(member):
                member.uid = member.gid = 0
                member.uname = member.gname = 'root'
                return member
            archive.add(portable, arcname=portable.name, filter=normalized)
        debroot = work / 'debroot'
        installed_tree(portable, debroot)
        control = debroot / 'DEBIAN'
        control.mkdir()
        installed_size = (sum(p.stat().st_size for p in debroot.rglob('*') if p.is_file()) + 1023) // 1024
        (control / 'control').write_text(f'''Package: dengshell
Version: {args.version}-{args.release}
Section: net
Priority: optional
Architecture: amd64
Maintainer: DengShell contributors
Installed-Size: {installed_size}
Depends: libc6 (>= 2.35), libgtk-3-0 (>= 3.24) | libgtk-3-0t64 (>= 3.24), libwebkit2gtk-4.1-0 (>= 2.36), iputils-ping, bash
Recommends: libayatana-appindicator3-1
Homepage: https://ds.free-vps.org
Description: DengShell native SSH desktop workspace
 Native GTK3/WebKitGTK 4.1 SSH, SFTP and Linux monitoring workspace.
''')
        deb = out / 'up.deb'
        subprocess.run(['dpkg-deb', '--root-owner-group', '-Zxz', '--build', str(debroot), str(deb)], check=True)
        rpmroot = work / 'rpmroot'
        installed_tree(portable, rpmroot)
        rpmdir = work / 'rpmbuild'
        for name in ['BUILD', 'RPMS', 'SOURCES', 'SPECS', 'SRPMS', 'BUILDROOT']:
            (rpmdir / name).mkdir(parents=True)
        required = '\n'.join('Requires: ' + name + '()(64bit)' for name in info['neededLibraries'])
        spec = rpmdir / 'SPECS/dengshell.spec'
        spec.write_text(f'''Name: dengshell
Version: {args.version}
Release: {args.release}
Summary: Native SSH desktop workspace
License: MIT
URL: https://ds.free-vps.org
BuildArch: x86_64
AutoReqProv: no
Requires: glibc >= 2.35
Requires: /bin/bash
Requires: iputils
{required}
%global _build_id_links none
%global __os_install_post %{{nil}}
%description
Native GTK3/WebKitGTK 4.1 SSH and SFTP desktop workspace.
%prep
%build
%install
mkdir -p %{{buildroot}}
cp -a /work/rpmroot/. %{{buildroot}}/
%files
/opt/dengshell
/usr/bin/dengshell
/usr/share/applications/dengshell.desktop
/usr/share/icons/hicolor/256x256/apps/dengshell.png
/usr/share/doc/dengshell
''')
        subprocess.run(docker + ['run', '--rm', '--platform', 'linux/amd64', '--user', f'{os.getuid()}:{os.getgid()}',
                                   '-v', f'{work}:/work', '-e', 'HOME=/work', '-e', 'TMPDIR=/work', image,
                                   'rpmbuild', '--define', '_topdir /work/rpmbuild', '--define', '_tmppath /work',
                                   '-bb', '/work/rpmbuild/SPECS/dengshell.spec'], check=True)
        rpm = out / 'DengShell-linux-x64.rpm'
        copy(next((rpmdir / 'RPMS').rglob('*.rpm')), rpm)
        artifacts = {p.name: {'bytes': p.stat().st_size, 'sha256': sha(p)} for p in [tar, deb, rpm]}
        manifest = {'schemaVersion': 1, 'version': args.version, 'release': args.release,
                    'architecture': 'amd64', 'executable': info, 'artifacts': artifacts,
                    'formats': {'up.deb': 'Debian 12+, Ubuntu 22.04+ and compatible derivatives',
                                'DengShell-linux-x64.rpm': 'Fedora/openSUSE with glibc >= 2.35 and WebKitGTK 4.1',
                                'DengShell-linux-x64.tar.gz': 'glibc x86-64 desktops with GTK3 and WebKitGTK 4.1'},
                    'unsupported': ['ARM/32-bit', 'musl/Alpine', 'Ubuntu 20.04/Debian 11', 'RHEL/Rocky/AlmaLinux 8/9 default repositories']}
        (out / 'linux-packages.json').write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n')
        (out / 'SHA256SUMS.txt').write_text(''.join(f"{value['sha256']}  {name}\n" for name, value in artifacts.items()))
        print(json.dumps(manifest, ensure_ascii=False, indent=2))


parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--inspect-binary', type=Path)
parser.add_argument('--build-info', type=Path)
parser.add_argument('--stage', type=Path)
parser.add_argument('--version', default='0.1.0')
parser.add_argument('--release', default=str(int(re.search(r'const ApplicationBuild uint64 = (\d+)', (ROOT/'internal/app/updates.go').read_text()).group(1)) % 1000))
parser.add_argument('--output', type=Path, default=ROOT / 'build/linux')
args = parser.parse_args()
if not re.fullmatch(r'[0-9]+(?:\.[0-9]+)*', args.version) or not re.fullmatch(r'[0-9]+', args.release):
    parser.error('version/release must be numeric package identifiers')
if args.inspect_binary:
    result = inspect(args.inspect_binary)
    if args.build_info:
        result['builderImageID'] = os.environ.get('DENGSHELL_BUILDER_ID', '')
        result['goVersion'] = output(['go', 'version'])
        result['compiler'] = output(['gcc', '-dumpfullversion'])
        result['pkgConfig'] = {name: output(['pkg-config', '--modversion', name]) for name in ['gtk+-3.0', 'webkit2gtk-4.1']}
        result['buildPackages'] = output(['dpkg-query', '-W', '-f=${Package}\t${Version}\n']).splitlines()
        args.build_info.write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps({key: value for key, value in result.items() if key != 'buildPackages'}, ensure_ascii=False, indent=2))
elif args.stage:
    package(args)
else:
    parser.error('provide --inspect-binary or --stage')
