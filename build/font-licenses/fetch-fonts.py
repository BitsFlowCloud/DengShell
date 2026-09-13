#!/usr/bin/env python3
"""Fetch pinned OFL fonts, losslessly wrap them in WOFF2 and validate the bundle.

Requires Python 3 with fonttools[woff] and requests. Original sfnt font programs
are retained: no glyph subsetting, axis instancing, renaming or outline changes.
Run from any directory; temporary downloads live in the user's cache.
"""

from concurrent.futures import ThreadPoolExecutor
from hashlib import sha256
from io import BytesIO
import json
import os
from pathlib import Path
import time

from fontTools.ttLib import TTFont
import requests


ROOT = Path(__file__).resolve().parents[2]
DEST = ROOT / 'web/assets/fonts'
LICENSES = ROOT / 'build/font-licenses'
CACHE = Path(os.environ.get('XDG_CACHE_HOME', Path.home() / '.cache')) / 'dengshell-font-sources'
GOOGLE_COMMIT = '809e4d8b8d7e9364a914909bb777679606c178b8'
NOTO_COMMIT = 'f8d157532fbfaeda587e826d4cd5b21a49186f7c'
GOOGLE = f'https://raw.githubusercontent.com/google/fonts/{GOOGLE_COMMIT}/ofl'
NOTO = f'https://raw.githubusercontent.com/notofonts/noto-cjk/{NOTO_COMMIT}'
FONTS = [
    ('jetbrains-mono', 'JetBrains Mono', 'jetbrainsmono', 'JetBrainsMono[wght].ttf'),
    ('fira-code', 'Fira Code', 'firacode', 'FiraCode[wght].ttf'),
    ('source-code-pro', 'Source Code Pro', 'sourcecodepro', 'SourceCodePro[wght].ttf'),
    ('ibm-plex-mono', 'IBM Plex Mono', 'ibmplexmono', 'IBMPlexMono-Regular.ttf'),
    ('roboto-mono', 'Roboto Mono', 'robotomono', 'RobotoMono[wght].ttf'),
    ('inconsolata', 'Inconsolata', 'inconsolata', 'Inconsolata[wdth,wght].ttf'),
    ('fira-mono', 'Fira Mono', 'firamono', 'FiraMono-Regular.ttf'),
    ('space-mono', 'Space Mono', 'spacemono', 'SpaceMono-Regular.ttf'),
    ('cutive-mono', 'Cutive Mono', 'cutivemono', 'CutiveMono-Regular.ttf'),
    ('pt-mono', 'PT Mono', 'ptmono', 'PTM55FT.ttf'),
    ('anonymous-pro', 'Anonymous Pro', 'anonymouspro', 'AnonymousPro-Regular.ttf'),
    ('overpass-mono', 'Overpass Mono', 'overpassmono', 'OverpassMono[wght].ttf'),
    ('red-hat-mono', 'Red Hat Mono', 'redhatmono', 'RedHatMono[wght].ttf'),
    ('dm-mono', 'DM Mono', 'dmmono', 'DMMono-Regular.ttf'),
    ('spline-sans-mono', 'Spline Sans Mono', 'splinesansmono', 'SplineSansMono[wght].ttf'),
    ('geist-mono', 'Geist Mono', 'geistmono', 'GeistMono[wght].ttf'),
    ('azeret-mono', 'Azeret Mono', 'azeretmono', 'AzeretMono[wght].ttf'),
    ('martian-mono', 'Martian Mono', 'martianmono', 'MartianMono[wdth,wght].ttf'),
    ('chivo-mono', 'Chivo Mono', 'chivomono', 'ChivoMono[wght].ttf'),
    ('fragment-mono', 'Fragment Mono', 'fragmentmono', 'FragmentMono-Regular.ttf'),
]
BOLD_FONTS = [
    ('ibm-plex-mono-bold', 'IBM Plex Mono Bold', 'ibmplexmono', 'IBMPlexMono-Bold.ttf'),
    ('fira-mono-bold', 'Fira Mono Bold', 'firamono', 'FiraMono-Bold.ttf'),
    ('space-mono-bold', 'Space Mono Bold', 'spacemono', 'SpaceMono-Bold.ttf'),
    ('anonymous-pro-bold', 'Anonymous Pro Bold', 'anonymouspro', 'AnonymousPro-Bold.ttf'),
]


def fetch(url):
    cached = CACHE / sha256(url.encode()).hexdigest()
    if cached.exists():
        return cached.read_bytes()
    for attempt in range(3):
        try:
            response = requests.get(url, timeout=120)
            response.raise_for_status()
            content = response.content
            cached.write_bytes(content)
            return content
        except requests.RequestException:
            if attempt == 2:
                raise
            time.sleep(attempt + 1)


def bundle(spec):
    font_id, name, directory, filename = spec
    source_url = f'{GOOGLE}/{directory}/{filename}'
    license_url = f'{GOOGLE}/{directory}/OFL.txt'
    if font_id == 'noto-sans-mono-cjk-sc':
        source_url = f'{NOTO}/Sans/Mono/NotoSansMonoCJKsc-Regular.otf'
        license_url = f'{NOTO}/Sans/LICENSE'
    raw = fetch(source_url)
    license_text = fetch(license_url)
    if b'SIL OPEN FONT LICENSE' not in license_text.upper():
        raise ValueError(f'Expected OFL license for {font_id}')
    (LICENSES / f'{font_id}.txt').write_bytes(license_text)
    (DEST / 'licenses' / f'{font_id}.txt').write_bytes(license_text)
    font = TTFont(BytesIO(raw), recalcTimestamp=False)
    output_file = f'{font_id}.woff2'
    font.flavor = 'woff2'
    font.save(DEST / output_file)
    item = {
        'id': font_id,
        'name': name,
        'family': 'Deng CJK' if font_id == 'noto-sans-mono-cjk-sc' else f'Deng {name}',
        'file': f'assets/fonts/{output_file}',
        'kind': 'builtin',
        'license': f'assets/fonts/licenses/{font_id}.txt',
        'licenseName': 'SIL Open Font License 1.1',
        'source': source_url,
        'sourceSha256': sha256(raw).hexdigest(),
        'sha256': sha256((DEST / output_file).read_bytes()).hexdigest(),
        'bytes': (DEST / output_file).stat().st_size,
    }
    if 'fvar' in font:
        item['axes'] = {a.axisTag: {'min': a.minValue, 'default': a.defaultValue, 'max': a.maxValue} for a in font['fvar'].axes}
    if font_id != 'noto-sans-mono-cjk-sc':
        item['fallback'] = 'Deng CJK'
    print(f'{name}: {item["bytes"]:,} bytes', flush=True)
    font.close()
    return item


def validate(catalog):
    ascii_chars = set(range(0x20, 0x7f))
    chinese_text = '中文简体繁體服务器连接密钥代理字体管理上传下载文件目录设置背景测试你好世界中华人民共和国臺灣香港澳門龍龍汉漢龘𠮷'
    punctuation = '，。！？：；（）【】《》、'
    box_chars = '┌─┬┐│├┼┤└┴┘═║╔╗╚╝╠╣╦╩╬'
    gb2312 = set()
    for high in range(0xb0, 0xf8):
        for low in range(0xa1, 0xff):
            try:
                gb2312.add(ord(bytes([high, low]).decode('gb2312')))
            except UnicodeDecodeError:
                pass
    fallback = TTFont(DEST / Path(catalog['fallback']['file']).name)
    fallback_map = fallback.getBestCmap()
    cjk_sample = set(map(ord, chinese_text + punctuation))
    assert not cjk_sample - fallback_map.keys(), 'Missing CJK sample glyph'
    assert len(gb2312) == 6763
    assert not gb2312 - fallback_map.keys(), 'Missing GB2312 Chinese character'
    big5 = set()
    for high in range(0xa4, 0xfa):
        for low in list(range(0x40, 0x7f)) + list(range(0xa1, 0xff)):
            try:
                cp = ord(bytes([high, low]).decode('big5'))
                if 0x3400 <= cp <= 0x9fff: big5.add(cp)
            except UnicodeDecodeError:
                pass
    assert len(big5) == 13051
    assert not big5 - fallback_map.keys(), 'Missing Big5 traditional Chinese ideograph'
    fallback_upm = fallback['head'].unitsPerEm
    chinese_widths = {fallback['hmtx'][fallback_map[cp]][0] / fallback_upm for cp in gb2312}
    assert chinese_widths == {1.0}, f'Unexpected CJK ideograph advances: {chinese_widths}'
    results = []
    for item in catalog['fonts']:
        font = TTFont(DEST / Path(item['file']).name)
        cmap = font.getBestCmap()
        assert not ascii_chars - cmap.keys(), f'{item["name"]}: missing ASCII'
        advances = {font['hmtx'][cmap[cp]][0] for cp in ascii_chars}
        assert len(advances) == 1, f'{item["name"]}: non-monospaced ASCII: {advances}'
        upm = font['head'].unitsPerEm
        width_em = next(iter(advances)) / upm
        assert width_em >= .5, f'{item["name"]}: CJK glyph wider than two Latin cells'
        missing_boxes = set(map(ord, box_chars)) - cmap.keys() - fallback_map.keys()
        if missing_boxes:
            raise ValueError(f'{item["name"]}: missing box drawing {"".join(map(chr, missing_boxes))}')
        item['cellWidthEm'] = width_em
        item['asciiGlyphCount'] = len(ascii_chars)
        if item.get('bold'):
            bold = TTFont(DEST / Path(item['bold']['file']).name)
            bold_map = bold.getBestCmap()
            assert not ascii_chars - bold_map.keys(), f'{item["name"]} Bold: missing ASCII'
            bold_advances = {bold['hmtx'][bold_map[cp]][0] / bold['head'].unitsPerEm for cp in ascii_chars}
            assert bold_advances == {width_em}, f'{item["name"]} Bold: different cell widths'
            bold.close()
        results.append((item['name'], len(cmap), width_em, item['bytes']))
        font.close()
    total_bytes = sum(p.stat().st_size for p in DEST.rglob('*') if p.is_file() and p.name != 'catalog.json')
    report = [
        '# DengShell bundled font validation', '',
        'The 20 choices are 20 distinct Latin monospace families, each paired with the same bundled Noto Sans Mono CJK SC font. They are not represented as 20 independently complete Chinese font designs.', '',
        '## Provenance and packaging', '',
        '- Proportional application UI: Source Sans 3 variable (169,416 bytes), also bundled under OFL; generated by `fetch-ui-font.py` and paired with the same offline CJK fallback.',
        f'- Google Fonts commit: `{GOOGLE_COMMIT}`.',
        f'- Noto CJK commit: `{NOTO_COMMIT}`.',
        '- All fonts are SIL OFL 1.1. Unmodified upstream license text is both embedded in `web/assets/fonts/licenses/` and copied under `build/font-licenses/`.',
        '- Source programs are only losslessly WOFF2-compressed. No outlines, naming, character coverage, variable axes, or metrics were modified.',
        '- Exact pinned source URLs and source/output SHA-256 hashes are recorded in the catalog.',
        '- All assets are loaded from the application; no Google Fonts CSS or runtime CDN/network request is required.',
        f'- Bundled font programs + licenses: {total_bytes:,} bytes ({total_bytes / 1024 / 1024:.2f} MiB), excluding the small JSON manifest.', '',
        '## Automated checks', '',
        '- Decoded every WOFF2 using fontTools; every Latin font contains all 95 printable ASCII characters with a single constant advance width.',
        '- Shared CJK fallback contains all 6,763 GB2312 Chinese ideographs, the listed simplified/traditional/supplementary-plane test text, and Chinese punctuation.',
        f'- Chinese test: `{chinese_text}`.',
        f'- Chinese punctuation test: `{punctuation}`.',
        '- All GB2312 ideographs have an advance of exactly 1 em in the CJK fallback.',
        f'- Every selected Latin + CJK fallback pair covers the box-drawing test: `{box_chars}`.',
        '- Every Latin cell is at least 0.5 em; the 1 em CJK ideograph fits inside the terminal’s two-cell allocation.', '',
        '| Latin family | Unicode mappings | ASCII cell / em | WOFF2 bytes |',
        '| --- | ---: | ---: | ---: |',
        *[f'| {name} | {count:,} | {width:.6f} | {size:,} |' for name, count, width, size in results], '',
        f'Shared CJK fallback: {len(fallback_map):,} Unicode mappings, {catalog["fallback"]["bytes"]:,} bytes.', '',
        '## Practical limits / integration requirements', '',
        '- Load and await both the chosen face and the CJK fallback before refitting xterm; otherwise early fallback measurements can use a system font.',
        '- Use the selected family followed by `"Deng CJK", monospace`; fonts use their default/regular weight and default width axis. The xterm grid assigns Chinese wide characters two cells.',
        '- Traditional Chinese: all 13,051 Big5 Han ideographs decoded from A4–F9 rows are present in the shared fallback (zero missing).',
        '- These checks establish glyph coverage and advances, not a guarantee for every Unicode character or every terminal program. Emoji, newly encoded rare ideographs, and unusual combining sequences can still depend on other fallback support.',
        '- Variable font axes are retained. Register `FontFace` with catalog `weightRange` before selecting weight 400 or 700; register separate `bold` faces with weight 700. Do not leave a variable font registered only as normal/400, because its upstream default axis can be 200 or 300.',
        '- Twelve families contain an actual variable 700 weight; four more have separately bundled upstream Bold files. Cutive Mono, PT Mono, DM Mono, Fragment Mono and the common CJK fallback use browser synthetic bold. `boldStyle` makes this distinction explicit; synthetic bold is not described as an upstream Bold font file.',
        '- Font smoothing and hinting can differ between WebView2 and WebKitGTK; visually verify at the actual display scale.', '',
        '## Reproduce', '',
        'Install `fonttools[woff]` and `requests` into a Python virtual environment, then run `python build/font-licenses/fetch-fonts.py`. Cached upstream downloads are stored below the user cache directory, and pinned revisions ensure a repeatable source selection.', '',
    ]
    (ROOT / 'build/FONT-VALIDATION.md').write_text('\n'.join(report), encoding='utf-8')
    fallback.close()


def main():
    for directory in (DEST / 'licenses', LICENSES, CACHE):
        directory.mkdir(parents=True, exist_ok=True)
    with ThreadPoolExecutor(max_workers=4) as pool:
        fonts = list(pool.map(bundle, FONTS))
        bold_fonts = list(pool.map(bundle, BOLD_FONTS))
    bold_by_id = {item['id'].removesuffix('-bold'): item for item in bold_fonts}
    for item in fonts:
        weight = item.get('axes', {}).get('wght')
        if weight:
            item['weightRange'] = f'{weight["min"]:g} {weight["max"]:g}'
            item['boldStyle'] = 'variable'
        elif item['id'] in bold_by_id:
            bold = bold_by_id[item['id']]
            item['bold'] = {key: value for key, value in bold.items() if key in ('file', 'source', 'sourceSha256', 'sha256', 'bytes', 'license')}
            item['bold']['weight'] = '700'
            item['boldStyle'] = 'separate'
        else:
            item['boldStyle'] = 'synthetic'
    fallback = bundle(('noto-sans-mono-cjk-sc', 'Noto Sans Mono CJK SC', '', ''))
    fallback['boldStyle'] = 'synthetic'
    catalog = {'version': 1, 'fallback': fallback, 'fonts': fonts}
    validate(catalog)
    (DEST / 'catalog.json').write_text(json.dumps(catalog, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    print('Validation passed; catalog and report written.', flush=True)


if __name__ == '__main__':
    main()
