#!/usr/bin/env python3
"""Verify the committed minimal bundle without downloading or changing files."""
import json
from hashlib import sha256
from pathlib import Path

root = Path(__file__).resolve().parents[2] / 'web'
used = set()
for directory, count in [('fonts',5),('ui-fonts',1)]:
    catalog_path = root/'assets'/directory/'catalog.json'
    catalog = json.loads(catalog_path.read_text())
    assert len(catalog['fonts']) == count, directory
    entries = list(catalog['fonts'])
    if catalog.get('fallback'): entries.append(catalog['fallback'])
    for f in list(entries):
        if f.get('bold',{}).get('file'): entries.append(f['bold'])
    expected = {catalog_path}
    for font in entries:
        path=root/font['file']; raw=path.read_bytes()
        assert len(raw)==font['bytes'] and sha256(raw).hexdigest()==font['sha256'], path
        assert raw[:4]==b'wOF2', path
        license_path=root/font['license']
        assert 'OPEN FONT LICENSE' in license_path.read_text().upper(), license_path
        expected.update([path,license_path]);used.add(path)
    actual={p for p in catalog_path.parent.rglob('*') if p.is_file()}
    assert actual == expected, f'{directory}: unreferenced or missing files: {actual ^ expected}'
print(f'PASS: 1 UI + 5 Shell families, 7 font files (including a separate bold face), {sum(p.stat().st_size for p in used):,} font bytes; hashes and licenses verified.')
