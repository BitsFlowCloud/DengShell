#!/usr/bin/env python3
"""Subset bundled fonts for English and simplified/traditional Chinese.

Run after the original font-generation scripts. Only built-in web assets are
processed; custom fonts and user configuration are never read. Requires
fonttools[woff]. Font IDs, axes, metrics, hinting and OFL notices are preserved.
"""
from concurrent.futures import ProcessPoolExecutor
from hashlib import sha256
import argparse
import json
from pathlib import Path
import re
import os
import shutil
import subprocess
import tempfile
from fontTools import subset, unicodedata
from fontTools.ttLib import TTFont
from fontTools.pens.recordingPen import DecomposingRecordingPen

ROOT = Path(__file__).resolve().parents[2]
LANGUAGES = ['en', 'zh-Hans', 'zh-Hant']
SCRIPTS = {'Latn', 'Hani', 'Bopo', 'Zyyy', 'Zinh'}
# Greek letters are also standard mathematical/engineering symbols.
TECHNICAL = set(map(ord, 'ΑΒΓΔΕΖΗΘΙΚΛΜΝΞΟΠΡΣΤΥΦΧΨΩαβγδεζηθικλμνξοπρστυφχψωϑϕϖϵϰϱς'))
# Browser comparisons found changed Latin ligatures in Fira/Overpass and
# technical symbols in Sarasa. Preserve these original faces until that
# interaction is resolved, instead of shipping known rendering changes.
PRESERVE_ORIGINAL = {'fira-code.woff2', 'overpass-mono.woff2', 'sarasa.woff2', 'ibm-plex-sans-sc.woff2'}

def open_font(path):
    # The Python WOFF2 glyf reconstruction can copy large remaining streams
    # once per glyph. Prefer the native decoder for large Chinese font faces.
    decoder = shutil.which('woff2_decompress')
    if decoder and Path(path).suffix == '.woff2':
        data = Path(path).read_bytes()
        cache = Path(os.environ.get('XDG_CACHE_HOME', Path.home()/'.cache'))/'dengshell-font-build'
        cache.mkdir(parents=True,exist_ok=True)
        decoded = cache/(sha256(data).hexdigest()+'.ttf')
        if not decoded.exists():
            with tempfile.TemporaryDirectory(dir=cache) as temp:
                source = Path(temp)/'font.woff2';source.write_bytes(data)
                subprocess.run([decoder,str(source)],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE)
                (Path(temp)/'font.ttf').replace(decoded)
        return TTFont(decoded,recalcTimestamp=False)
    return TTFont(path,recalcTimestamp=False)

def retained(cp):
    return unicodedata.script(chr(cp)) in SCRIPTS or cp in TECHNICAL or unicodedata.category(chr(cp)) in {'Sm', 'Co'}

def rename(font, family):
    names = font['name']
    style = names.getDebugName(17) or names.getDebugName(2) or 'Regular'
    prefix = re.sub('[^A-Za-z0-9]', '', family)
    postscript = prefix + '-' + re.sub('[^A-Za-z0-9]', '', style)
    values = {1: family, 3: postscript+'-zh-en-1', 4: family+' '+style, 6: postscript, 16: family, 18: family+' '+style, 21: family, 25: prefix}
    names.names = [n for n in names.names if n.nameID not in values]
    for ident, value in values.items():
        names.setName(value, ident, 3, 1, 0x409)
        try:
            value.encode('mac_roman')
        except UnicodeEncodeError:
            pass  # Keep non-Latin styles in the Unicode name records only.
        else:
            names.setName(value, ident, 1, 0, 0)
    if 'fvar' in font:
        for index, instance in enumerate(font['fvar'].instances):
            ident = instance.postscriptNameID
            if ident != 0xffff:
                names.names = [n for n in names.names if n.nameID != ident]
                names.setName(prefix+'-Instance'+str(index), ident, 3, 1, 0x409)
    if 'CFF ' in font:
        cff = font['CFF '].cff
        cff.fontNames = [postscript]
        cff.topDictIndex[0].FamilyName = family
        cff.topDictIndex[0].FullName = family+' '+style

def variations(font, keep):
    return sorted((selector, cp, glyph is None) for table in font['cmap'].tables if table.format == 14 for selector, mappings in table.uvsDict.items() for cp, glyph in mappings if cp in keep)

def work(args):
    src, dst, family, sample, cached = args
    raw = Path(src).read_bytes()
    font = open_font(src)
    preserve = Path(src).name in PRESERVE_ORIGINAL
    cmap = font.getBestCmap()
    keep = {cp for cp in cmap if preserve or retained(cp)}
    widths = {cp: font['hmtx'][cmap[cp]] for cp in keep}
    axes = [(a.axisTag,a.minValue,a.defaultValue,a.maxValue) for a in font['fvar'].axes] if 'fvar' in font else []
    uvs = variations(font, keep)
    notice = {n.toUnicode() for n in font['name'].names if n.nameID in (0,13,14)}
    # Compare decomposed outlines, not glyph names/indices changed by subsetting.
    chosen = sorted(set(sample)&keep | set(sorted(keep)[::max(1,len(keep)//100)]))
    glyphs = font.getGlyphSet(); shapes = {}
    missing = DecomposingRecordingPen(glyphs);glyphs[font.getGlyphOrder()[0]].draw(missing)
    missing_width = font['hmtx'][font.getGlyphOrder()[0]]
    for cp in chosen:
        pen = DecomposingRecordingPen(glyphs); glyphs[cmap[cp]].draw(pen); shapes[cp] = pen.value
    reuse = False
    if cached and cached.get('subsetInputSha256') == sha256(raw).hexdigest() and Path(dst).is_file() and sha256(Path(dst).read_bytes()).hexdigest() == cached.get('sha256'):
        candidate = open_font(dst)
        reuse = set(candidate.getBestCmap()) == keep and variations(candidate, keep) == uvs and (Path(dst).read_bytes() == raw if preserve else candidate['name'].getDebugName(1) == family)
        if reuse:
            cg = candidate.getGlyphSet();pen = DecomposingRecordingPen(cg);cg[candidate.getGlyphOrder()[0]].draw(pen)
            reuse = pen.value == missing.value and candidate['hmtx'][candidate.getGlyphOrder()[0]] == missing_width
        candidate.close()
    if not reuse:
        Path(dst).parent.mkdir(parents=True,exist_ok=True)
        if preserve:
            Path(dst).write_bytes(raw)
        else:
            options = subset.Options(); options.name_IDs = ['*']; options.name_languages = ['*']; options.layout_features = ['*']; options.notdef_outline = True
            selectors = {selector for selector, _, _ in uvs}
            sub = subset.Subsetter(options=options); sub.populate(unicodes=keep | selectors); sub.subset(font)
            rename(font, family); font.flavor = 'woff2'; font.save(dst)
    font.close()
    result = open_font(dst); after = result.getBestCmap()
    assert set(after) == keep, src+': Unicode coverage changed'
    assert all(result['hmtx'][after[cp]] == widths[cp] for cp in keep), src+': metrics changed'
    assert axes == ([(a.axisTag,a.minValue,a.defaultValue,a.maxValue) for a in result['fvar'].axes] if 'fvar' in result else []), src+': axes changed'
    assert variations(result, keep) == uvs, src+': variation selectors changed'
    assert {n.toUnicode() for n in result['name'].names if n.nameID in (0,13,14)} == notice, src+': license notice changed'
    glyphs = result.getGlyphSet()
    pen = DecomposingRecordingPen(glyphs);glyphs[result.getGlyphOrder()[0]].draw(pen)
    assert pen.value == missing.value and result['hmtx'][result.getGlyphOrder()[0]] == missing_width, src+': missing-glyph indicator changed'
    for cp, expected in shapes.items():
        pen = DecomposingRecordingPen(glyphs); glyphs[after[cp]].draw(pen)
        assert pen.value == expected, f'{src}: outline changed U+{cp:04X}'
    output = Path(dst).read_bytes()
    row = {'file':str(Path(src).name),'beforeBytes':len(raw),'bytes':len(output),'sha256':sha256(output).hexdigest(),'subsetInputSha256':sha256(raw).hexdigest(),'unicodeMappings':len(after),'removedMappings':len(cmap)-len(keep),'allRetainedMetricsPreserved':True,'allHanMappingsPreserved':True,'variationSelectorsPreserved':True,'variableAxesPreserved':True,'licenseNoticesPreserved':True,'missingGlyphIndicatorPreserved':True,'sampledOutlinesCompared':len(shapes),'subsetLanguages':LANGUAGES,'derivativeFamily':family}
    row['preservedOriginalForCompatibility'] = preserve
    if preserve:
        row['derivativeFamily'] = result['name'].getDebugName(1)
    print(f'{row["file"]}: {len(raw):,} -> {len(output):,} bytes; removed {row["removedMappings"]} non-target mappings',flush=True)
    return row

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source-root',type=Path,default=ROOT)
    parser.add_argument('--output-root',type=Path,default=ROOT)
    parser.add_argument('--jobs',type=int,default=2)
    parser.add_argument('--report',type=Path,required=True)
    parser.add_argument('--cache-report',type=Path,help='reuse matching cached files, then repeat all validation')
    args = parser.parse_args(); source=args.source_root; output=args.output_root
    paths=['web/assets/fonts/catalog.json','web/assets/ui-fonts/catalog.json']
    catalogs={p:json.loads((source/p).read_text()) for p in paths}
    terminal=catalogs[paths[0]]; ui=catalogs[paths[1]]; entries=[*terminal['fonts'],*ui['fonts']]
    entries += [dict(f['bold'],family=f['family']+' Bold',_target=f['bold']) for f in terminal['fonts'] if f.get('bold')]
    chars=set(map(ord,'简体中文繁體中文 English 臺灣香港澳門龍龘𠮷，。；：「」『』─│┌┐└┘╬✓×±μΩ')) | set(range(32,127))
    for p in (source/'web').glob('*'):
        if p.suffix in ['.js','.html']:
            chars |= {ord(c) for c in p.read_text() if unicodedata.script(c)=='Hani'}
    cache_report = args.cache_report or args.report
    previous = json.loads(cache_report.read_text()) if cache_report.exists() else {}
    verified = {row['file']:row for row in previous.get('fonts',[]) }
    tasks=[(str(source/'web'/f['file']),str(output/'web'/f['file']),f['family'],sorted(chars),verified.get(Path(f['file']).name)) for f in entries]
    with ProcessPoolExecutor(max_workers=args.jobs) as pool: rows=list(pool.map(work,tasks))
    for entry,row in zip(entries,rows):
        target=entry.get('_target',entry)
        for k in ['bytes','sha256','unicodeMappings','subsetInputSha256','subsetLanguages','derivativeFamily','preservedOriginalForCompatibility']:target[k]=row[k]
    terminal['fallback'].update(next(f for f in terminal['fonts'] if f['file']==terminal['fallback']['file']))
    terminal['fallback']['id']='builtin:maple-mono-cn'
    for font in ui['fonts']:
        if 'coverage' in ui:
            slug=font['id'].removeprefix('builtin:ui-');ui['coverage'].setdefault(slug,{})['unicodeMappings']=font['unicodeMappings']
    for rel,catalog in catalogs.items():
        catalog['subsetLanguages']=LANGUAGES
        dest=output/rel;dest.parent.mkdir(parents=True,exist_ok=True);dest.write_text(json.dumps(catalog,ensure_ascii=False,indent=2)+'\n')
    report={'passed':True,'languageScripts':sorted(SCRIPTS),'technicalGreekSymbolsRetained':True,'fonts':rows,'beforeBytes':sum(r['beforeBytes'] for r in rows),'afterBytes':sum(r['bytes'] for r in rows)}
    args.report.parent.mkdir(parents=True,exist_ok=True);args.report.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
    print(f'Total: {report["beforeBytes"]:,} -> {report["afterBytes"]:,} bytes',flush=True)

if __name__=='__main__':main()
