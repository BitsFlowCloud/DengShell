#!/usr/bin/env python3
"""Package an existing clean installed tree using Arch's native makepkg."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parent.parent


def package(args):
    if not re.fullmatch(r'\d+(?:\.\d+)*', args.version) or not re.fullmatch(r'\d+', args.release):
        raise SystemExit('version and release must be numeric')
    tree = args.root.resolve()
    if sorted(p.name for p in tree.iterdir()) != ['opt', 'usr']:
        raise SystemExit('Arch input must be a clean installed tree with only opt/ and usr/.')
    data = tree / 'opt/dengshell/data'
    if not data.is_dir() or any(p.name not in ['docs', 'licenses', 'support'] for p in data.iterdir()):
        raise SystemExit('Arch input contains non-distribution data.')
    docker = ['docker']
    if subprocess.run(docker + ['info'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
        docker = ['sudo', '-n', 'docker']
    image = os.environ.get('DENGSHELL_ARCH_BUILDER', 'dengshell-arch-builder:r28')
    if not os.environ.get('DENGSHELL_ARCH_BUILDER'):
        subprocess.run(docker + ['build', '-t', image, '-f', str(ROOT / 'build/linux/Arch.Dockerfile'), str(ROOT / 'build/linux')], check=True)
    cache = Path.home() / '.cache/dengshell-linux-release'
    cache.mkdir(parents=True, exist_ok=True)
    args.output.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='arch-package-', dir=cache) as temporary:
        work = Path(temporary)
        with tarfile.open(work / 'payload.tar', 'w') as archive:
            def normalized(member):
                member.uid = member.gid = 0
                member.uname = member.gname = 'root'
                return member
            archive.add(tree, arcname='payload', filter=normalized)
        with (work / 'payload.tar').open('rb') as stream:
            digest = hashlib.file_digest(stream, 'sha256').hexdigest()
        (work / 'PKGBUILD').write_text(f'''pkgname=dengshell
pkgver={args.version}
pkgrel={args.release}
pkgdesc='DengShell SSH terminal and SFTP desktop workspace'
arch=('x86_64')
url='https://github.com/BitsFlowCloud/DengShell'
license=('MIT' 'OFL-1.1')
depends=('glibc>=2.35' 'gtk3' 'webkit2gtk-4.1' 'bash' 'iputils' 'pacman' 'libarchive' 'polkit')
optdepends=('libayatana-appindicator: system tray integration')
options=('!strip' '!debug')
source=('payload.tar')
sha256sums=('{digest}')
package() {{
    cp -a "$srcdir/payload/." "$pkgdir/"
    install -Dm644 "$pkgdir/usr/share/doc/dengshell/copyright" "$pkgdir/usr/share/licenses/dengshell/LICENSE"
}}
''')
        subprocess.run(docker + ['run', '--rm', '--user', f'{os.getuid()}:{os.getgid()}', '-v', f'{work}:/work', '-e', 'HOME=/work', '-e', 'TMPDIR=/work', image, 'makepkg', '--nodeps', '--noconfirm', '--force'], check=True)
        packages = list(work.glob('dengshell-*.pkg.tar.zst'))
        if len(packages) != 1:
            raise SystemExit('Expected exactly one pacman package')
        dest = args.output / f'DengShell-linux-x64.pkg.tar.zst'
        shutil.copy2(packages[0], dest)
        # Keep the build recipe alongside metadata for reproducibility.
        shutil.copy2(work / 'PKGBUILD', args.output / 'PKGBUILD')
        with dest.open('rb') as stream:
            digest = hashlib.file_digest(stream, 'sha256').hexdigest()
        print(json.dumps({'file': str(dest), 'bytes': dest.stat().st_size, 'sha256': digest}, indent=2))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, required=True)
    parser.add_argument('--version', default='0.1.0')
    parser.add_argument('--release', required=True)
    parser.add_argument('--output', type=Path, required=True)
    package(parser.parse_args())
