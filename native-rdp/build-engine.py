#!/usr/bin/env python3
"""Rebuild the pinned Windows x64 engine in an empty external directory."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import urllib.request
import zipfile

here = Path(__file__).resolve().parent
repo = here.parent
work = Path(sys.argv[1]).resolve() if len(sys.argv) == 2 else None
if work is None or (work.exists() and any(work.iterdir())):
    raise SystemExit('Usage: build-engine.py /absolute/path/to/empty/build-directory')
work.mkdir(parents=True, exist_ok=True)
if hasattr(os, 'sched_getaffinity'):
    cpus = os.sched_getaffinity(0) & {0, 1, 2, 3}
    if cpus:
        os.sched_setaffinity(0, cpus)

def run(*args, cwd=work):
    subprocess.run(args, cwd=cwd, check=True)

for entry in json.loads((here / 'sources.json').read_text()):
    path = work / entry['file']
    urllib.request.urlretrieve(entry['url'], path)
    if hashlib.sha256(path.read_bytes()).hexdigest() != entry['sha256']:
        raise SystemExit('Source checksum mismatch: ' + entry['file'])
    run('tar', '-xf', str(path))

source = work / 'freerdp-3.32.0'
prefix = work / 'mingw64'
build = work / 'engine-build'
run('patch', '-p1', '--batch', '-i', str(here / 'freerdp-3.32.0.patch'), cwd=source)
toolchain = work / 'mingw-toolchain.cmake'
toolchain.write_text('''set(CMAKE_SYSTEM_NAME Windows)
set(CMAKE_SYSTEM_PROCESSOR x86_64)
set(CMAKE_C_COMPILER x86_64-w64-mingw32-gcc)
set(CMAKE_CXX_COMPILER x86_64-w64-mingw32-g++)
set(CMAKE_RC_COMPILER x86_64-w64-mingw32-windres)
set(CMAKE_FIND_ROOT_PATH "''' + str(prefix) + '''" /usr/x86_64-w64-mingw32)
set(CMAKE_FIND_ROOT_PATH_MODE_PROGRAM NEVER)
set(CMAKE_FIND_ROOT_PATH_MODE_LIBRARY ONLY)
set(CMAKE_FIND_ROOT_PATH_MODE_INCLUDE ONLY)
set(CMAKE_FIND_ROOT_PATH_MODE_PACKAGE ONLY)
''')
options = {
    'CMAKE_BUILD_TYPE': 'Release', 'CMAKE_TOOLCHAIN_FILE': str(toolchain),
    'CMAKE_PREFIX_PATH': str(prefix), 'CMAKE_INSTALL_PREFIX': str(work / 'install'),
    'CMAKE_POLICY_VERSION_MINIMUM': '3.5', 'CMAKE_EXE_LINKER_FLAGS': '-static',
    'CMAKE_C_FLAGS': '-D__STDC_NO_THREADS__=1',
    'CMAKE_CXX_FLAGS': '-D__STDC_NO_THREADS__=1',
    'BUILD_SHARED_LIBS': 'OFF', 'BUILD_TESTING': 'OFF',
    'OPENSSL_USE_STATIC_LIBS': 'TRUE', 'USE_UNWIND': 'OFF',
    'WITH_CLIENT_SDL': 'OFF', 'WITH_SERVER': 'OFF', 'WITH_SAMPLE': 'OFF',
    'WITH_WINPR_TOOLS': 'OFF', 'WITH_FFMPEG': 'OFF', 'WITH_SWSCALE': 'OFF',
    'WITH_MEDIA_FOUNDATION': 'OFF', 'WITH_JPEG': 'OFF', 'WITH_AAD': 'OFF',
    'WITH_WINPR_JSON': 'OFF', 'WITH_WIN_CONSOLE': 'ON',
    'WITH_INTERNAL_MD4': 'ON', 'WITH_INTERNAL_RC4': 'ON',
    'WITH_PROGRESS_BAR': 'OFF', 'WITH_SMARTCARD_EMULATE': 'OFF',
    'CHANNEL_URBDRC': 'OFF', 'CHANNEL_SMARTCARD': 'OFF',
    'CHANNEL_RDPECAM': 'OFF', 'CHANNEL_PRINTER': 'OFF', 'CHANNEL_LOCATION': 'OFF',
}
run('cmake', '-S', str(source), '-B', str(build), '-G', 'Ninja',
    *[f'-D{k}={v}' for k, v in options.items()])
run('cmake', '--build', str(build), '--target', 'wfreerdp', '-j4')
exe = build / 'client/Windows/cli/wfreerdp.exe'
target = repo / 'internal/rdpengine/payload/windows-amd64.zip'
temporary = target.with_suffix('.zip.tmp')
with zipfile.ZipFile(temporary, 'w', zipfile.ZIP_DEFLATED, compresslevel=9) as z:
    for name, path in [
        ('DengShellRDP.exe', exe),
        ('FreeRDP-LICENSE.txt', source / 'LICENSE'),
        ('OpenSSL-LICENSE.txt', prefix / 'share/licenses/openssl/LICENSE'),
        ('NOTICE.txt', here / 'NOTICE.txt'),
        ('MinGW-w64-LICENSE.txt', here / 'MinGW-w64-LICENSE.txt'),
        ('GCC-runtime-LICENSE.txt', here / 'GCC-runtime-LICENSE.txt'),
    ]:
        item = zipfile.ZipInfo(name, (2026, 9, 27, 0, 0, 0))
        item.compress_type = zipfile.ZIP_DEFLATED
        z.writestr(item, path.read_bytes())
os.replace(temporary, target)
print('Embedded engine:', target)
print('Engine SHA-256:', hashlib.sha256(exe.read_bytes()).hexdigest())
