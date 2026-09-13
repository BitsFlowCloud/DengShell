#!/usr/bin/env python3
"""Stage a complete release from frozen binaries; never include user data.

Outputs are written to --output and the prepared --website directory. Deploy
only after validating this complete set. This script never overwrites share,
private configuration, or the desktop source checkout.
"""
from pathlib import Path
import argparse, hashlib, json, re, shutil, subprocess, tarfile, zipfile
ROOT = Path(__file__).resolve().parent.parent
BUILD = int(re.search(r'const ApplicationBuild uint64 = (\d+)', (ROOT/'internal/app/updates.go').read_text()).group(1))
RELEASE = BUILD % 1000
LABEL = f'{str(BUILD)[:8]}-r{RELEASE}'

def digest(p):
    with p.open('rb') as f: return hashlib.file_digest(f, 'sha256').hexdigest()

def copy(src, dst):
    dst.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(src, dst)

def zip_tree(folder, target, prefix=Path()):
    with zipfile.ZipFile(target, 'w', zipfile.ZIP_DEFLATED, compresslevel=9) as z:
        for p in sorted(folder.rglob('*')):
            if p.is_file(): z.write(p, prefix/p.relative_to(folder))

def distribution(folder, binary, windows):
    folder.mkdir(parents=True, exist_ok=False)
    copy(binary, folder/('DengShell.exe' if windows else 'dengshell'))
    for sub, pattern, dest in [('build/font-licenses','*.txt','fonts'), ('web/vendor','*LICENSE',''), ('build/go-licenses','*','go'), ('build/installer-licenses','*','installer')]:
        for p in (ROOT/sub).glob(pattern):
            if p.is_file(): copy(p, folder/'data/licenses'/dest/p.name)
    for name in ['LICENSE-GRAPHICS.txt','ATTRIBUTION.txt']: copy(ROOT/'web/assets/group-emoji'/name,folder/'data/licenses'/('Twemoji-'+name))
    for p in (ROOT/'web/assets/system-logos').glob('*.txt'): copy(p,folder/'data/licenses/system-logos'/p.name)
    copy(ROOT/'LICENSE',folder/'data/licenses/DengShell-MIT.txt')
    for name in ['CONNECTION-CONFIG.md','config.example.json','COMMON-APPS.md','ONLINE-UPDATE-DESIGN.md','UPDATER-CONTRACT.md',f'RELEASE-v0.01-r{RELEASE}.md']:
        copy(ROOT/'build'/name,folder/'data/docs'/name)
    copy(ROOT/'README.md',folder/'data/docs/README.md')
    copy(ROOT/('build/windows/README.txt' if windows else 'build/linux/README.txt'),folder/'data/docs/使用说明.txt')
    return folder

def package(args):
    out, work, site = args.output.resolve(), args.work_dir.resolve(), args.website.resolve()
    for p in [out,work,site/'downloads']: p.mkdir(parents=True,exist_ok=True)
    winbinary, linuxbinary = args.windows_binary.resolve(), args.linux_binary.resolve()
    reference = (args.windows_reference or args.windows_binary).resolve()
    binaries = [reference.read_bytes(),linuxbinary.read_bytes()]
    assets = {}
    for folder in [ROOT/'web',ROOT/'internal/app/shell_integration']:
        for p in sorted(folder.rglob('*')):
            if not p.is_file() or any(n.startswith(('.','_')) for n in p.relative_to(folder).parts):continue
            assert all(p.read_bytes() in b for b in binaries), f'Embedded asset differs: {p}'
            assets[str(p.relative_to(ROOT))]=digest(p)
    win = distribution(work/'DengShell-windows-x64',winbinary,True)
    linux = distribution(work/'DengShell-linux-x64',linuxbinary,False)
    zip_tree(win,out/'DengShell-windows-x64.zip')
    subprocess.run(['python3',str(ROOT/'scripts/package-windows.py'),'--stage',str(win),'--output',str(out/'DengShell-Setup-x64.exe')],check=True)
    subprocess.run(['python3',str(ROOT/'scripts/package-linux.py'),'--stage',str(linux),'--version','0.1.0','--release',str(RELEASE),'--output',str(out)],check=True)
    copy(winbinary,out/'DengShell.exe')
    copy(out/'up.deb',out/'DengShell-linux-x64.deb')
    copy(out/'up.deb',out/'DengShell-ubuntu-x64.deb')
    copy(ROOT/'build/windows/README.txt',out/'DengShell-Windows-说明.txt')
    source = work/'source';source.mkdir()
    sourcefiles=[]
    for pattern in ['*.go','go.mod','go.sum','package.json','package-lock.json','*.sh','*.ps1','*.syso','README.md','LICENSE','AGENTS.md']:
        sourcefiles += list(ROOT.glob(pattern))
    for folder in ['web','internal','scripts']:
        sourcefiles += [p for p in (ROOT/folder).rglob('*') if p.is_file() and '__pycache__' not in p.parts]
    for name in ['dengshell.png','dengshell.svg','dengshell.ico','CONNECTION-CONFIG.md','config.example.json','COMMON-APPS.md',f'RELEASE-v0.01-r{RELEASE}.md','ONLINE-UPDATE-DESIGN.md','UPDATER-CONTRACT.md','FONT-VALIDATION.md','BACKGROUND-PROMPTS-v0.01.json','windows/app.manifest','windows/README.txt','linux/README.txt','linux/Dockerfile','linux/.dockerignore','linux/native/build-info.json','linux/COMPATIBILITY.json']:
        sourcefiles.append(ROOT/'build'/name)
    for folder in ['go-licenses','font-licenses','installer-licenses']:
        sourcefiles += [p for p in (ROOT/'build'/folder).glob('*') if p.is_file()]
    for p in sorted(set(sourcefiles)):copy(p,source/p.relative_to(ROOT))
    zip_tree(source,out/'DengShell-source.zip',Path('DengShell'))
    notes='密钥管理器保存口令、多服务器命令历史、独立曲线样式、文件属主名称与大小/时间排序；Windows 安装/绿色版及 Linux 桌面安装包。'
    for binary,artifact,platform,name in [(winbinary,winbinary,'windows-amd64','up.exe'),(linuxbinary,out/'up.deb','linux-amd64','up.deb')]:
        copy(artifact,site/name)
        info={'schemaVersion':2,'build':BUILD,'product':'DengShell','platform':platform,'version':'v0.01','notes':notes,'sha256':digest(artifact),'size':artifact.stat().st_size,'executableSHA256':digest(binary)}
        (site/(name+'.json')).write_text(json.dumps(info,ensure_ascii=False,indent=2)+'\n')
    for name in ['DengShell-windows-x64.zip','DengShell-Setup-x64.exe','DengShell-linux-x64.rpm','DengShell-linux-x64.tar.gz','DengShell-source.zip']:
        copy(out/name,site/'downloads'/name)
    files={p.name:{'bytes':p.stat().st_size,'sha256':digest(p)} for p in sorted(out.iterdir()) if p.suffix in ['.exe','.zip','.gz','.deb','.rpm']}
    manifest={'version':'v0.01','build':LABEL,'applicationBuild':BUILD,'artifacts':files,'embeddedAssets':assets,'executableHashes':{'windows-amd64':digest(winbinary),'linux-amd64':digest(linuxbinary)}}
    (out/'release.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2)+'\n')
    (out/'SHA256SUMS.txt').write_text(''.join(f"{v['sha256']}  {n}\n" for n,v in files.items()))
    (site/'downloads/SHA256SUMS.txt').write_text(''.join(f'{digest(p)}  {p.relative_to(site)}\n' for p in sorted(site.rglob('*')) if p.is_file() and p.suffix in ['.zip','.deb','.exe','.gz','.rpm']))
    print(json.dumps({'output':str(out),'website':str(site),'build':LABEL,'artifacts':files},ensure_ascii=False,indent=2))

if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--windows-binary',type=Path,required=True)
    parser.add_argument('--windows-reference',type=Path)
    parser.add_argument('--linux-binary',type=Path,default=ROOT/'build/linux/native/dengshell')
    parser.add_argument('--work-dir',type=Path,required=True)
    parser.add_argument('--website',type=Path,required=True)
    parser.add_argument('--output',type=Path,required=True)
    package(parser.parse_args())
