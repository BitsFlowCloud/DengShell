#!/usr/bin/env python3
"""Bundle the proportional application UI face; Chinese uses the existing CJK face."""
from pathlib import Path
from hashlib import sha256
from io import BytesIO
import json, requests
from fontTools.ttLib import TTFont
root=Path(__file__).resolve().parents[2]
base='https://raw.githubusercontent.com/google/fonts/809e4d8b8d7e9364a914909bb777679606c178b8/ofl/sourcesans3/'
source=base+'SourceSans3[wght].ttf'
r=requests.get(source,timeout=60);r.raise_for_status();raw=r.content
r=requests.get(base+'OFL.txt',timeout=30);r.raise_for_status();license=r.content
assert b'SIL OPEN FONT LICENSE' in license.upper()
font=TTFont(BytesIO(raw),recalcTimestamp=False);assert set(range(32,127))<=font.getBestCmap().keys()
font.flavor='woff2';target=root/'web/assets/fonts/ui-source-sans-3.woff2';font.save(target)
for directory in [root/'web/assets/fonts/licenses',root/'build/font-licenses']:(directory/'ui-source-sans-3.txt').write_bytes(license)
meta={'name':'Source Sans 3','family':'Deng UI','file':'assets/fonts/ui-source-sans-3.woff2','weightRange':'200 900','license':'assets/fonts/licenses/ui-source-sans-3.txt','licenseName':'SIL Open Font License 1.1','source':source,'sourceSha256':sha256(raw).hexdigest(),'sha256':sha256(target.read_bytes()).hexdigest(),'bytes':target.stat().st_size,'fallback':'Deng CJK'}
(root/'web/assets/fonts/ui-font.json').write_text(json.dumps(meta,indent=2)+'\n')
print('Proportional UI font:',meta['bytes'],'bytes')
