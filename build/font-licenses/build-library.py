#!/usr/bin/env python3
"""Build the reviewed website font library; never touches releases or user data.

Requires fonttools[woff], native woff2_decompress and 7z for upstream archives.
Inputs, archives and licenses are pinned by SHA-256 in library-sources.json.
Run render-library.mjs afterwards to generate real PNG previews and catalogs.
"""
import argparse
from concurrent.futures import ProcessPoolExecutor
from hashlib import sha256
import importlib.util
import io
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('subset_helpers', Path(__file__).with_name('subset-languages.py'))
helpers = importlib.util.module_from_spec(spec)
spec.loader.exec_module(helpers)

def digest(data):
    return sha256(data).hexdigest()

def input_file(font, cache):
    path = cache / (font['inputSHA256'] + font['inputExtension'])
    if path.exists() and digest(path.read_bytes()) == font['inputSHA256']:
        return path
    url = font['sourceURL']
    rawpath = cache / digest(url.encode())
    if not rawpath.exists() or digest(rawpath.read_bytes()) != font['downloadSHA256']:
        with urllib.request.urlopen(url, timeout=120) as response:
            raw = response.read(300 * 1024 * 1024 + 1)
        if digest(raw) != font['downloadSHA256']:
            raise ValueError(font['id'] + ': upstream checksum mismatch')
        rawpath.write_bytes(raw)
    raw = rawpath.read_bytes()
    member = font.get('archiveMember')
    if member:
        if font.get('archiveType') == '7z':
            raw = subprocess.check_output(['7z', 'e', '-so', str(rawpath), member], stderr=subprocess.DEVNULL)
        else:
            with zipfile.ZipFile(io.BytesIO(raw)) as archive:
                raw = archive.read(member)
    if digest(raw) != font['inputSHA256']:
        raise ValueError(font['id'] + ': font checksum mismatch')
    path.write_bytes(raw)
    return path

def build_one(args):
    font, cache, output, existing = args
    source = input_file(font, cache)
    face = helpers.open_font(source)
    cmap = face.getBestCmap()
    metrics = face['hmtx'].metrics
    upm = face['head'].unitsPerEm
    widths = {metrics[cmap[cp]][0] for cp in range(32, 127) if cp in cmap}
    ascii_complete = all(cp in cmap for cp in range(32, 127))
    # Space can be special in a display face; terminal fonts must keep every
    # printable ASCII character at exactly the same advance.
    if font['kind'] == 'font' and (not ascii_complete or len(widths) != 1):
        raise ValueError(font['id'] + ': not an ASCII monospace font')
    axes = {a.axisTag: [a.minValue, a.defaultValue, a.maxValue] for a in face['fvar'].axes} if 'fvar' in face else {}
    result = {k: font[k] for k in ('id','kind','name','description','style','project','version','licenseName','licenseText')}
    result['description'] = result['description'].replace(' · 默认', ' · 通用界面')
    if font.get('builtinId'):
        result['builtinId'] = font['builtinId']
    if 'wght' in axes:
        result['weightRange'] = f"{axes['wght'][0]:g} {axes['wght'][2]:g}"
    if font['kind'] == 'font':
        result['fallbackNote'] = '英文等宽；缺少的中文由内置 Maple Mono CN 按终端格宽补齐。'
    elif font['id'] in ('ui-huninn','ui-chiron-hei','ui-chiron-sung','ui-chiron-round','ui-wenkai-tc'):
        result['fallbackNote'] = '繁体优先；缺少的简体字由内置 IBM Plex Sans SC 补齐。'
    else:
        result['fallbackNote'] = '支持中英文；未收录字符由内置 IBM Plex Sans SC 和系统字体补齐。'
    if font['id'] == 'ui-zhuque':
        result['fallbackNote'] += ' 此版本为上游技术预览版。'
    if font['id'] == 'ui-smiley':
        result['fallbackNote'] += ' 固定倾斜风格，更适合标题。'
    raw = source.read_bytes()
    modified = source.suffix != '.woff2'
    if modified:
        family = 'Deng Library ' + font['inputSHA256'][:12]
        helpers.rename(face, family)
        face['name'].setName(font['licenseText'], 13, 3, 1, 0x409)
        face.flavor = 'woff2'
        if existing:
            raw = Path(existing).read_bytes()
        else:
            target = io.BytesIO()
            face.save(target)
            raw = target.getvalue()
    name = digest(raw) + '.woff2'
    target = output / 'fonts/files' / name
    target.write_bytes(raw)
    licensepath = output / 'fonts/licenses' / (font['id'] + '.txt')
    licensepath.write_text(font['licenseText'], encoding='utf-8')
    result['file'] = {'path': '/fonts/files/' + name, 'sha256': digest(raw), 'size': len(raw)}
    result['_validation'] = {'inputSHA256':font['inputSHA256'], 'unicodeMappings':len(cmap), 'asciiComplete':ascii_complete, 'asciiWidthsEm':[w/upm for w in sorted(widths)], 'axes':axes, 'convertedToWOFF2':modified}
    face.close()
    print(font['id'], len(raw), flush=True)
    return result

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--website-output', type=Path, required=True)
    parser.add_argument('--cache', type=Path, required=True)
    parser.add_argument('--jobs', type=int, default=2)
    args = parser.parse_args()
    for directory in ('fonts/files','fonts/previews','fonts/licenses'):
        (args.website_output / directory).mkdir(parents=True, exist_ok=True)
    args.cache.mkdir(parents=True, exist_ok=True)
    fonts = json.loads(Path(__file__).with_name('library-sources.json').read_text())['fonts']
    assert len([f for f in fonts if f['kind']=='ui-font']) == 20
    assert len([f for f in fonts if f['kind']=='font']) == 30
    for font in fonts:
        assert digest(font['licenseText'].encode()) == font['licenseSHA256'], font['id'] + ': license checksum mismatch'
    existing = {}
    for path in (args.website_output/'fonts/files').glob('*.woff2'):
        if path.stem != digest(path.read_bytes()):
            continue
        candidate = helpers.open_font(path)
        family = candidate['name'].getDebugName(1)
        if family and family.startswith('Deng Library '):
            existing[family] = str(path)
        candidate.close()
    with ProcessPoolExecutor(max_workers=args.jobs) as pool:
        result = list(pool.map(build_one, [(f,args.cache,args.website_output,existing.get('Deng Library '+f['inputSHA256'][:12])) for f in fonts]))
    (args.website_output/'fonts/render-input.json').write_text(json.dumps({'version':1,'fonts':result},ensure_ascii=False,indent=2)+'\n')

if __name__ == '__main__':
    main()
