#!/usr/bin/env python3
"""Build a per-user Windows installer from a clean portable distribution."""
from pathlib import Path
import argparse, hashlib, json, os, re, shutil, subprocess, tempfile

ROOT = Path(__file__).resolve().parent.parent

def nsis_string(value):
    return str(value).replace('$', '$$').replace('"', '$\\"')

def build(stage, output):
    application_build = int(re.search(r"const ApplicationBuild uint64 = (\d+)", (ROOT / "internal/app/updates.go").read_text()).group(1))
    release = application_build % 1000
    stage, output = stage.resolve(), output.resolve()
    if sorted(p.name for p in stage.iterdir()) != ['DengShell.exe', 'data']:
        raise SystemExit('Windows staging must contain only DengShell.exe and data.')
    if any(p.name not in ['docs', 'licenses'] for p in (stage / 'data').iterdir()):
        raise SystemExit('User data must never enter the installer.')
    tool = shutil.which('makensis')
    if not tool:
        raise SystemExit('Install NSIS (makensis) before packaging Windows.')
    output.parent.mkdir(parents=True, exist_ok=True)
    cache = Path.home() / '.cache/cloudshell-build'
    cache.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='dengshell-nsis-', dir=cache) as temp:
        temp = Path(temp)
        files = sorted(p for p in stage.rglob('*') if p.is_file())
        uninstall = []
        for p in files:
            if p.relative_to(stage).as_posix() == 'DengShell.exe':
                continue  # Explicit checked deletion precedes registry/shortcut removal.
            relative = nsis_string(str(p.relative_to(stage)).replace('/', '\\'))
            uninstall.append(f'Delete "$INSTDIR\\{relative}"')
        for p in sorted((p for p in stage.rglob('*') if p.is_dir()), key=lambda p:len(p.parts), reverse=True):
            relative = nsis_string(str(p.relative_to(stage)).replace('/', '\\'))
            uninstall.append(f'RMDir "$INSTDIR\\{relative}"')
        (temp / 'uninstall-files.nsh').write_text('\n'.join(uninstall)+'\n')
        # makensis uses libc tmpfile(), which ignores TMPDIR on Linux. A private
        # mount keeps its mmap scratch on disk without touching the system /tmp.
        prefix = []
        if shutil.which('bwrap'):
            scratch = temp / 'nsis-tmp'; scratch.mkdir()
            prefix = ['bwrap','--bind','/','/','--bind',str(scratch),'/tmp',
                      '--dev-bind','/dev','/dev','--die-with-parent','--']
            if subprocess.run(prefix+['/bin/true'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode:
                prefix = []
        subprocess.run(prefix + [tool, '-V2', '-INPUTCHARSET', 'UTF8',
                        '-DSTAGE='+str(stage), '-DOUTPUT='+str(output),
                        '-DICON='+str(ROOT/'build/dengshell.ico'),
                        '-DLICENSE_FILE='+str(ROOT/'LICENSE'),
                        '-DUNINSTALL_FILES='+str(temp/'uninstall-files.nsh'),
                        '-DESTIMATED_SIZE='+str(sum(p.stat().st_size for p in files)//1024),
                        '-DRELEASE='+str(release),
                        str(ROOT/'scripts/windows-installer.nsi')], check=True,
                       env={**os.environ,'TMPDIR':str(cache)})
    with output.open('rb') as f:
        digest = hashlib.file_digest(f,'sha256').hexdigest()
    print(json.dumps({'file':str(output),'bytes':output.stat().st_size,'sha256':digest,'scope':'current-user','preservesConfigOnUninstall':True},indent=2))

if __name__ == '__main__':
    parser=argparse.ArgumentParser()
    parser.add_argument('--stage',type=Path,required=True)
    parser.add_argument('--output',type=Path,default=ROOT/'build/windows/DengShell-Setup-x64.exe')
    args=parser.parse_args();build(args.stage,args.output)
