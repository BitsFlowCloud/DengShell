#!/usr/bin/env python3
"""Create tiny live-preview subsets from the reviewed website font files.
Requires fontTools + brotli. Full fonts and licenses remain unchanged.
"""
import argparse, hashlib, json
from concurrent.futures import ProcessPoolExecutor
from pathlib import Path
from fontTools import subset
from fontTools.ttLib import TTFont
ROOT = Path(__file__).resolve().parent.parent
TEXT = 'DengShell SSH root@server:~$ echo "Hello" 0123456789 /home/user 简体中文 繁體中文 连接服务器 文件管理 网络流量 ABC abc ┌─────┐│'
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('website', type=Path)
a = p.parse_args()
catalog = json.loads((ROOT/'internal/app/font_library_catalog.json').read_text())
site_path = a.website/'fonts/catalog.json'
site = json.loads(site_path.read_text())
def make_preview(row):
    source = a.website/row['file']['path'].lstrip('/')
    assert hashlib.sha256(source.read_bytes()).hexdigest() == row['file']['sha256']
    existing = list((a.website/'fonts/previews').glob(row['id']+'-*.woff2'))
    if existing:
        target = existing[0]; raw = target.read_bytes()
    else:
        print('Subsetting',row['id'],flush=True)
        font = TTFont(source)
        options = subset.Options(); options.flavor = 'woff2'; options.recalc_timestamp = False
        # The fixed sample needs glyphs and weight axes, not contextual scripts.
        options.layout_features = []; options.layout_closure = False
        options.drop_tables += ['GSUB', 'GPOS', 'GDEF', 'BASE', 'JSTF', 'DSIG']
        sub = subset.Subsetter(options=options); sub.populate(text=TEXT); sub.subset(font)
        temporary = a.website/'fonts/previews'/('preview-'+row['id']+'.woff2')
        font.save(temporary); raw = temporary.read_bytes()
        target = temporary.with_name(row['id']+'-'+hashlib.sha256(raw).hexdigest()[:16]+'.woff2'); temporary.replace(target)
    assert len(raw) < 256*1024
    return {'path': '/'+str(target.relative_to(a.website)), 'sha256': hashlib.sha256(raw).hexdigest(), 'size': len(raw)}

if __name__ == '__main__':
    with ProcessPoolExecutor(max_workers=2) as pool:
        previews = list(pool.map(make_preview, catalog['fonts']))
    total = 0
    for row, preview in zip(catalog['fonts'], previews):
        row['previewFont'] = preview
        next(r for r in site['fonts'] if r['id']==row['id'])['previewFont'] = preview
        total += preview['size']
    (ROOT/'internal/app/font_library_catalog.json').write_text(json.dumps(catalog,ensure_ascii=False,indent=2)+'\n')
    site_path.write_text(json.dumps(site,ensure_ascii=False,indent=2)+'\n')
    print(json.dumps({'fonts':len(catalog['fonts']), 'previewBytes':total}))
