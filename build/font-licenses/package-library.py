#!/usr/bin/env python3
"""Verify and prepare a static font website; each font is stored only once."""
import argparse
from hashlib import sha256
import json
from pathlib import Path
import shutil

root=Path(__file__).resolve().parents[2]
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--website-output',type=Path,required=True)
args=parser.parse_args();base=args.website_output
catalog=json.loads((base/'fonts/catalog.json').read_text())
assert catalog==json.loads((root/'internal/app/font_library_catalog.json').read_text()),'website and client catalog differ'
for font in catalog['fonts']:
    for item in [font['file'],font['preview']]:
        data=(base/item['path'].lstrip('/')).read_bytes()
        assert len(data)==item['size'] and sha256(data).hexdigest()==item['sha256'],font['id']
    assert (base/'fonts/licenses'/(font['id']+'.txt')).read_text()==font['licenseText'],font['id']
for p in (root/'build/font-library-site').iterdir():
    if p.is_file():shutil.copy2(p,base/'fonts'/p.name)
print(f"Prepared {len(catalog['fonts'])} fonts, verified PNGs and licenses. Browser creates licensed ZIPs after confirmation.")
