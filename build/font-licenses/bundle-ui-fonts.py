#!/usr/bin/env python3
"""Build the five UI fonts from pinned upstream downloads in SOURCE_DIRECTORY.

Needs fonttools[woff] and 7z. Full faces retain every Unicode mapping; only the
separate picker previews are subset. Conversion derivatives use new names.
"""
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import urllib.request
import zipfile
from fontTools.ttLib import TTFont
from fontTools import subset

root = Path(__file__).resolve().parents[2]
source = Path(sys.argv[1])
source.mkdir(parents=True, exist_ok=True)
dest = root / 'web/assets/ui-fonts'
dest.mkdir(parents=True, exist_ok=True)
urls = json.loads((root / 'build/font-licenses/ui-font-sources.json').read_text())
previous = json.loads((dest / 'catalog.json').read_text()) if (dest / 'catalog.json').exists() else {'fonts': []}
expected = {font['source']: font['archiveSha256'] for font in previous['fonts']}
for filename, url in urls.items():
    target = source / filename
    if not target.exists():
        with urllib.request.urlopen(url, timeout=120) as response:
            target.write_bytes(response.read())
    if url in expected:
        assert hashlib.sha256(target.read_bytes()).hexdigest() == expected[url], filename + ': upstream digest changed'
subprocess.run(['7z', 'e', '-y', '-o' + str(source), str(source / 'sarasa.7z'), 'SarasaUiSC-Regular.ttf'], check=True, stdout=subprocess.DEVNULL)
with zipfile.ZipFile(source / 'maple.zip') as archive:
    (source / 'MapleMono-CN-Regular.ttf').write_bytes(archive.read('MapleMono-CN-Regular.ttf'))

specs = [
    ('noto-sans', 'Noto 黑体', 'Deng UI Sans', 'Noto Sans CJK SC', 'noto-sans.otf', 'noto-sans.otf', '清晰均衡 · 默认', 'f8d157532fbfaeda587e826d4cd5b21a49186f7c'),
    ('noto-serif', 'Noto 宋体', 'Deng UI Serif', 'Noto Serif CJK SC', 'noto-serif.otf', 'noto-serif.otf', '衬线分明 · 书面阅读', 'f8d157532fbfaeda587e826d4cd5b21a49186f7c'),
    ('sarasa', '更纱 UI', 'Deng UI Sarasa', 'Sarasa UI SC', 'SarasaUiSC-Regular.ttf', 'sarasa.7z', '紧凑利落 · 信息密集', 'v1.0.41'),
    ('wenkai', '霞鹜文楷屏幕版', 'Deng UI WenKai', 'LXGW WenKai Screen', 'wenkai.ttf', 'wenkai.ttf', '楷书笔形 · 屏幕优化', 'v1.522'),
    ('maple', 'Maple Mono CN', 'Deng UI Maple', 'Maple Mono CN', 'MapleMono-CN-Regular.ttf', 'maple.zip', '圆润等宽 · 中英混排', 'v7.9'),
]
preview = 'DengShell ABC 012345 简体中文，清晰易读。繁體中文，閱讀舒適。'
required = set(map(ord, preview)) | set(range(32, 127))
ui_chars = set()
for file in (root / 'web').glob('*'):
    if file.suffix in ('.js', '.html'):
        ui_chars |= {ord(c) for c in file.read_text() if '\u3400' <= c <= '\u9fff'}

def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()

def rename(font, family):
    names = {1: family, 2: 'Regular', 3: family.replace(' ', '') + '-Regular-1', 4: family + ' Regular', 6: family.replace(' ', '') + '-Regular', 16: family, 17: 'Regular'}
    table = font['name']
    table.names = [record for record in table.names if record.nameID not in names]
    for name_id, value in names.items():
        table.setName(value, name_id, 3, 1, 0x409)
        table.setName(value, name_id, 1, 0, 0)
    if 'CFF ' in font:
        cff = font['CFF '].cff
        cff.fontNames = [names[6]]
        cff.topDictIndex[0].FamilyName = family
        cff.topDictIndex[0].FullName = names[4]

fonts, coverage = [], {}
for slug, name, family, upstream, filename, archive, description, version in specs:
    path = source / filename
    font = TTFont(path, recalcTimestamp=False)
    cmap = set(font.getBestCmap())
    assert required <= cmap, (slug, ''.join(map(chr, sorted(required - cmap))))
    copyright = sorted({n.toUnicode() for n in font['name'].names if n.nameID == 0})
    license_text = (source / (slug + '-license.txt')).read_text()
    assert 'SIL OPEN FONT LICENSE' in license_text.upper()
    license_name = slug + '-OFL.txt'
    (dest / license_name).write_text('\n'.join(copyright) + '\n\n' + license_text)
    rename(font, family)
    font.flavor = 'woff2'
    target = dest / (slug + '.woff2')
    font.save(target)
    assert set(TTFont(target).getBestCmap()) == cmap
    options = subset.Options()
    options.name_IDs = ['*']
    options.name_languages = ['*']
    sub = subset.Subsetter(options=options)
    sub.populate(text=preview)
    sub.subset(font)
    rename(font, family + ' Preview')
    thumb = dest / (slug + '-preview.woff2')
    font.save(thumb)
    coverage[slug] = {'unicodeMappings': len(cmap), 'missingApplicationCharacters': ''.join(map(chr, sorted(ui_chars - cmap))), 'asciiAndPreviewComplete': True}
    fonts.append(dict(id='builtin:ui-' + slug, name=name, family=family, upstream=upstream, description=description,
                      version=version, file='assets/ui-fonts/' + target.name, preview='assets/ui-fonts/' + thumb.name,
                      license='assets/ui-fonts/' + license_name, licenseName='SIL Open Font License 1.1',
                      source=urls[archive], sourceFile=filename, sourceSha256=digest(path), archiveSha256=digest(source / archive),
                      sha256=digest(target), bytes=target.stat().st_size, unicodeMappings=len(cmap)))
    print(slug, target.stat().st_size, coverage[slug], flush=True)

(dest / 'catalog.json').write_text(json.dumps({'fonts': fonts, 'previewText': preview, 'coverage': coverage}, ensure_ascii=False, indent=2) + '\n')
(root / 'build/font-licenses/ui-font-sources.json').write_text(json.dumps(urls, indent=2) + '\n')
