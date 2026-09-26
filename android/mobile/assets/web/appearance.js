'use strict';

let appearance = { uiFontId: 'builtin:ui-ibm-plex-sans-sc', uiTextColors: {}, fontId: 'builtin:jetbrains-mono', fontColors: {}, fontBold: {}, chartStyles: {}, backgroundId: 'builtin:none', backgroundOpacity: .42, backgroundVersion: 2, uiScale: Number(window.CLOUDSHELL?.uiScale) || 1, terminalFontSize: 14, startupAnimation: window.DengShellSplash?.enabled !== false };
const appearanceMapFields = new Set(['fontColors', 'fontBold', 'chartStyles', 'uiTextColors']);
const appearanceOwnedElsewhere = new Set(['layout', 'windowWidth', 'windowHeight', 'windowMaximised', 'terminalBold']);
let appearanceSaved = appearanceSnapshot(appearance);
let managedAssets = [], managedProxies = [], fontCatalog = null, backgroundCatalog = [], assetKind = 'background';
let terminalFontFamily = 'monospace', appearanceReady, appearanceSave = Promise.resolve(), mediaGeneration = 0;
const fontLoads = new Map(), assetData = new Map();
const proportionalTerminalFonts = new Set();
const terminalFallbackFaces = new Map();
let activeFontID = '', activeBackgroundID = '', effectiveScale = 1, appearanceOperation = 0;
const logicalWidth = () => innerWidth / effectiveScale;
const logicalHeight = () => innerHeight / effectiveScale;
const terminalDefaultColor = '#d3e0e8';
const fontColorPresets = [
  { name: '云白', color: terminalDefaultColor }, { name: '薄荷', color: '#a9dfbf' },
  { name: '冰蓝', color: '#9ed8ff' }, { name: '暖金', color: '#f3d49a' },
  { name: '淡紫', color: '#d9b8ff' }, { name: '珊瑚', color: '#f1b7ac' },
  { name: '石墨', color: '#243447' }, { name: '纯白', color: '#ffffff' },
];
const validFontColor = color => typeof color === 'string' && /^#[\da-f]{6}$/i.test(color);
const colorForFont = (id = appearance.fontId) => validFontColor(appearance.fontColors?.[id]) ? appearance.fontColors[id].toLowerCase() : terminalDefaultColor;
const boldForFont = (id = appearance.fontId) => appearance.fontBold?.[id] === true;
function normalizeTerminalFontSize(value) { const number=Number(value); return Number.isFinite(number) ? Math.max(8,Math.min(40,Math.round(number*2)/2)) : 14; }
function terminalTheme() {
  return { background: '#00000000', foreground: colorForFont(), cursor: colorForFont(), selectionBackground: '#7195aa66', black: '#233846', red: '#df9c96', green: '#a4c9b4', yellow: '#d7c29f', blue: '#8fb5d1', magenta: '#baafd0', cyan: '#96c8ce', white: '#d3e0e8', brightBlack: '#7d99ab', brightRed: '#efb5ae', brightGreen: '#b8dbbf', brightYellow: '#e8d7b5', brightBlue: '#b1d0e6', brightMagenta: '#d3c7e4', brightCyan: '#b7dfe2', brightWhite: '#edf5f9' };
}
function applyTerminalAppearanceColors() {
  const color = colorForFont();
  document.documentElement.style.setProperty('--terminal-foreground', color);
  for (const state of sessions.values()) state.term.options.theme = { ...state.term.options.theme, foreground: color, cursor: color, background: '#00000000' };
  const preview = document.getElementById('font-preview-text');
  if (preview) preview.style.color = color;
}

async function staticJSON(path) {
  const response = await fetch(path); if (!response.ok) throw new Error('无法读取内置资源'); return response.json();
}
function initializeAppearanceCatalogs() {
  if (!appearanceReady) appearanceReady = Promise.all([staticJSON('assets/fonts/catalog.json'), staticJSON('assets/backgrounds/catalog.json')]).then(([fonts, backgrounds]) => {
    fontCatalog = fonts; backgroundCatalog = backgrounds.backgrounds; return fonts;
  }).catch(error => { appearanceReady = null; throw error; });
  return appearanceReady;
}
function allFonts() { return [...(fontCatalog?.fonts || []).map(font => ({ ...font, id: `builtin:${font.id}` })), ...managedAssets.filter(asset => asset.kind === 'font').map(asset => ({ ...asset, family: `Deng custom ${asset.id}` }))]; }
function allBackgrounds() { return [{ id: 'builtin:none', name: '纯色 · 专注', kind: 'builtin' }, ...backgroundCatalog.map(background => ({ ...background, id: `builtin:${background.id}`, kind: 'builtin' })), ...managedAssets.filter(asset => asset.kind === 'background')]; }
async function assetURL(asset) {
  if (asset.file) return new URL(asset.file, location.href).href;
  if (!assetData.has(asset.id)) {
    const pending = api(`/api/assets/${asset.id}/data`).then(async data => {
      // Large base64 images exceed CSS custom-property size limits. Keep the
      // image in a Blob and put only its short local URL in --terminal-background.
      if (asset.kind === 'background') return URL.createObjectURL(await (await fetch(data.dataUrl)).blob());
      return data.dataUrl;
    }).catch(error => { if (assetData.get(asset.id) === pending) assetData.delete(asset.id); throw error; });
    assetData.set(asset.id, pending);
  }
  return assetData.get(asset.id);
}
function forgetAssetURL(id) {
  const pending = assetData.get(id); assetData.delete(id);
  // This also releases an image deleted while its data request was pending.
  pending?.then(url => { if (url.startsWith('blob:')) URL.revokeObjectURL(url); }, () => {});
}
async function loadFace(font) {
  if (!fontLoads.has(font.id)) fontLoads.set(font.id, (async () => {
    const url = await assetURL(font), face = new FontFace(font.family, `url(${JSON.stringify(url)})`, { weight: font.weightRange || '400', style: 'normal', display: 'block' });
    await face.load(); document.fonts.add(face);
    if (font.bold?.file) { const boldURL = new URL(font.bold.file, location.href).href; const bold = new FontFace(font.family, `url(${JSON.stringify(boldURL)})`, { weight: '700', style: 'normal', display: 'block' }); await bold.load(); document.fonts.add(bold); }
    return face;
  })().catch(error => { fontLoads.delete(font.id); throw new Error(`字体「${font.name}」无法加载：${error.message}`); }));
  return fontLoads.get(font.id);
}
async function alignedTerminalFontFamily(font) {
  // Uploaded display fonts may have proportional Latin glyphs. Keep their CJK
  // appearance, but use an explicitly named monospace Latin fallback in a grid.
  const canvas = document.createElement('canvas'), context = canvas.getContext('2d');
  context.font = `100px "${font.family}"`; context.fontKerning = 'none';
  const widths = [...'iWm0 .|'].map(char => context.measureText(char).width);
  let fallbackFamily = fontCatalog.fallback.family;
  const family = () => `"${font.family}", "${fallbackFamily}", monospace`;
  if (Math.max(...widths)-Math.min(...widths) < .1) {
    // Narrow faces (e.g. Iosevka) use 0.5 em cells, while Maple's Han glyphs
    // occupy 1.2 em. Scale only the fallback face to fit exactly two cells.
    const ratio = Math.min(1, widths[0] / 60);
    if (ratio < .995) {
      const key = ratio.toFixed(6), alias = `Deng CJK fit ${key}`;
      if (!terminalFallbackFaces.has(key)) terminalFallbackFaces.set(key, (async () => {
        const url = await assetURL(fontCatalog.fallback);
        const face = new FontFace(alias, `url(${JSON.stringify(url)})`, { weight: '400', sizeAdjust: `${ratio * 100}%` });
        if (!('sizeAdjust' in face)) throw new Error('当前浏览器内核不支持窄字体的中文对齐，请更新系统 WebView 后使用此字体');
        await face.load(); document.fonts.add(face); return face;
      })().catch(error => { terminalFallbackFaces.delete(key); throw error; }));
      await terminalFallbackFaces.get(key); fallbackFamily = alias;
    }
    return family();
  }
  proportionalTerminalFonts.add(font.id);
  const mono = allFonts().find(item => item.id === 'builtin:jetbrains-mono'); await loadFace(mono);
  return `"${mono.family}", ${family()}`;
}

// xterm 6's DOM renderer measures each glyph with integer offsetWidth / 32.
// That rounding, accumulated across mixed fallback/ANSI spans, shifts table
// columns. Keep its Unicode buffer and terminal semantics, and place rendered
// spans at the exact cell positions instead. This narrow adapter is guarded for
// the pinned xterm 6 DOM renderer; an alternative renderer stays untouched.
function installTerminalFontMetrics(state) {
  const renderer = state.term?._core?._renderService?._renderer?.value;
  const factory = renderer?._rowFactory, cache = renderer?._widthCache;
  if (!factory?.createRow || !cache?._measure || factory._dengGridMetrics) return;
  factory._dengGridMetrics = true; state.host.classList.add('terminal-grid-metrics');
  const scaleProbe = document.createElement('span');
  scaleProbe.className = 'xterm-char-measure-element';
  scaleProbe.style.cssText = 'position:absolute;display:block;width:100px;height:1px;border:0;padding:0;visibility:hidden;pointer-events:none';
  cache._container.append(scaleProbe);
  const measure = cache._measure;
  cache._measure = function (text, variant) {
    const element = this._measureElements?.[variant];
    if (!element) return measure.call(this,text,variant);
    element.textContent = text.repeat(32);
    // WebKit and Chromium expose different CSS-zoom rectangle coordinates.
    // Calibrate in the same parent instead of dividing by the requested zoom.
    const coordinateScale = scaleProbe.getBoundingClientRect().width / 100 || 1;
    return element.getBoundingClientRect().width / coordinateScale / 32;
  };
  const createRow = factory.createRow;
  factory.createRow = function (...args) {
    const spans = createRow.apply(this,args), line = args[0], cellWidth = args[7], pieces = [];
    if (!line?.loadCell || !Number.isFinite(cellWidth) || cellWidth <= 0) return spans;
    let column = 0;
    for (const span of spans) {
      const text = span.textContent, cells = []; let length = 0;
      while (length < text.length && column < line.length) {
        const cell = line.loadCell(column,this._workCell), width = cell.getWidth();
        if (!width) { column++; continue; }
        let chars = cell.isInvisible() ? ' ' : cell.getChars() || ' ';
        if (chars === ' ' && (cell.isUnderline() || cell.isOverline())) chars = '\u00a0';
        cells.push({ text:chars, column, width }); length += chars.length; column += width;
      }
      // Leave unexpected future renderer forms intact rather than guessing at
      // Unicode width or splitting combining/supplementary characters.
      if (length !== text.length || cells.map(cell=>cell.text).join('') !== text) return spans;
      pieces.push({span,cells});
    }
    const output = [], height = renderer.dimensions.css.cell.height;
    for (const {span,cells} of pieces) {
      const hasLines = cells.some(cell => terminalBoxGlyph(cell.text));
      const runs = [];
      if (hasLines) {
        for (const cell of cells) {
          const previous = runs.at(-1), box = terminalBoxGlyph(cell.text);
          if (previous && !!previous.box === !!box) { previous.text += cell.text; previous.width += cell.width; if (box) previous.box.push({glyph:box,width:cell.width}); }
          else runs.push({...cell,box:box?[{glyph:box,width:cell.width}]:null});
        }
      } else if (cells.length) runs.push({text:span.textContent,column:cells[0].column,width:cells.reduce((n,cell)=>n+cell.width,0)});
      for (const run of runs) {
        const element = runs.length === 1 ? span : span.cloneNode(false);
        element.textContent = run.text; element.style.position = 'absolute'; element.style.left = `${run.column*cellWidth}px`; element.style.width = `${run.width*cellWidth}px`;
        element.dataset.terminalColumn = run.column;
        if (run.box) {
          element.classList.add('terminal-box-glyph');
          element.append(drawTerminalBoxGlyph(run.box,run.width*cellWidth,height));
        }
        output.push(element);
      }
    }
    return output;
  };
  cache.clear(); renderer._setDefaultSpacing(); state.term.refresh(0,state.term.rows-1);
}
const terminalBoxGlyphs = new Map([
    ['─','ew'],['│','ns'],['┌','es'],['┐','ws'],['└','en'],['┘','wn'],['├','ens'],['┤','wns'],['┬','ews'],['┴','ewn'],['┼','ewns'],
    ['━','ew',2],['┃','ns',2],['┏','es',2],['┓','ws',2],['┗','en',2],['┛','wn',2],['┣','ens',2],['┫','wns',2],['┳','ews',2],['┻','ewn',2],['╋','ewns',2],
    ['═','ew',3],['║','ns',3],['╔','es',3],['╗','ws',3],['╚','en',3],['╝','wn',3],['╠','ens',3],['╣','wns',3],['╦','ews',3],['╩','ewn',3],['╬','ewns',3],
    ['╭','es',4],['╮','ws',4],['╰','en',4],['╯','wn',4],
  ].map(([text,edges,kind=1])=>[text,{edges,kind}]));
function terminalBoxGlyph(text) { return terminalBoxGlyphs.get(text); }
function drawTerminalBoxGlyph(glyphs,width,height) {
  const ns='http://www.w3.org/2000/svg',svg=document.createElementNS(ns,'svg'),path=document.createElementNS(ns,'path');
  svg.setAttribute('viewBox',`0 0 ${width} ${height}`); svg.setAttribute('preserveAspectRatio','none'); svg.setAttribute('aria-hidden','true');
  const cellWidth=width/glyphs.reduce((sum,item)=>sum+item.width,0);let start=0;const paths=new Map();
  // Adjacent box glyphs share one SVG/path: separate SVG rasterization creates
  // faint seams at fractional column boundaries, even when coordinates match.
  for(const {glyph,width:columns} of glyphs){
    const w=columns*cellWidth,cx=start+w/2,cy=height/2,points={e:[start+w,cy],w:[start,cy],n:[cx,0],s:[cx,height]};let d='';
    if(glyph.kind===4){const a=points[glyph.edges[0]],b=points[glyph.edges[1]];d=`M${a}Q${cx} ${cy} ${b}`;}
    else if(glyph.kind===3){
      const offset=Math.min(1.6,w/5);
      const oppositeCorner=glyph.edges==='ws'||glyph.edges==='en';
      for(const side of [-1,1])for(const edge of glyph.edges){const p=points[edge],vertical=edge==='n'||edge==='s',sx=side*offset,sy=(oppositeCorner?-side:side)*offset;d+=vertical?`M${cx+sx} ${cy+sy}L${p[0]+sx} ${p[1]}`:`M${cx+sx} ${cy+sy}L${p[0]} ${p[1]+sy}`;}
    }else for(const edge of glyph.edges)d+=`M${cx} ${cy}L${points[edge]}`;
    const thickness=glyph.kind===2?'1.8':'1';paths.set(thickness,(paths.get(thickness)||'')+d);start+=w;
  }
  for(const [thickness,d]of paths){const stroke=path.cloneNode(false);stroke.setAttribute('d',d);stroke.setAttribute('stroke-width',thickness);svg.append(stroke);}return svg;
}
async function applyAppearance() {
  const uiApplying = window.DengUIAppearance?.apply(appearance);
  window.DengChartStyles?.apply(appearance.chartStyles);
  const generation = ++mediaGeneration, next = { ...appearance };
  applyUIScale();
  document.documentElement.style.setProperty('--background-opacity', String(next.backgroundOpacity));
  applyTerminalAppearanceColors();
  await Promise.all([initializeAppearanceCatalogs(), uiApplying]);
  if (generation !== mediaGeneration) return;
  const font = allFonts().find(item => item.id === next.fontId) || allFonts()[0];
  if (activeFontID !== font.id) {
    await Promise.all([loadFace(fontCatalog.fallback), loadFace(font)]);
    if (generation !== mediaGeneration) return;
    const family = await alignedTerminalFontFamily(font);
    if (generation !== mediaGeneration) return;
    terminalFontFamily = family;
    activeFontID = font.id;
    document.documentElement.style.setProperty('--terminal-family', terminalFontFamily);
    for (const state of sessions.values()) state.term.options.fontFamily = terminalFontFamily;
  }
  terminalFont = normalizeTerminalFontSize(next.terminalFontSize);
  for (const state of sessions.values()) { state.term.options.fontSize = terminalFont; state.term.options.fontWeight = boldForFont(next.fontId) ? '700' : '400'; }
  const background = allBackgrounds().find(item => item.id === next.backgroundId) || allBackgrounds()[0];
  if (activeBackgroundID !== background.id) {
    const url = background.id === 'builtin:none' ? '' : await assetURL(background);
    if (generation !== mediaGeneration) return;
    document.documentElement.style.setProperty('--terminal-background', url ? `url(${JSON.stringify(url)})` : 'none'); activeBackgroundID = background.id; $('.terminal-panel').classList.toggle('has-background', !!url);
  }
  if (generation !== mediaGeneration) return;
  renderAppearanceControls(); clampAppearancePalette(); requestAnimationFrame(fitActive);
}
async function acceptAppearanceConfig(config) {
  managedAssets = config.assets || []; managedProxies = config.proxies || [];
  if (config.appearance) {
    await appearanceSave;
    const pending = appearanceDifference(appearanceSnapshot(appearance), appearanceSaved);
    const settings = config.appearance;
    const fontBold = Object.hasOwn(settings, 'fontBold') ? settings.fontBold || {} : settings.terminalBold ? { [settings.fontId || appearance.fontId]: true } : {};
    appearance = { ...appearance, ...settings, fontColors: { ...(settings.fontColors || {}) }, fontBold: { ...fontBold } };
    delete appearance.terminalBold;
    appearanceSaved = appearanceSnapshot(appearance);
    appearance = mergeAppearanceChanges(appearance, pending);
    window.DengShellSplash?.accept(appearance.startupAnimation);
  }
  if ((appearance.backgroundVersion || 0) < 2) { if (Math.abs(appearance.backgroundOpacity - .18) < .000001) appearance.backgroundOpacity = .42; appearance.backgroundVersion = 2; }
  await window.DengUIAppearance?.acceptConfig(config);
  try { await applyAppearance(); } catch (error) {
    toast(`外观资源暂时无法加载，已保留选择：${error.message}`);
    const selected = appearance;
    appearance = { ...appearance, fontId: 'builtin:jetbrains-mono', backgroundId: 'builtin:none' };
    await applyAppearance().catch(() => {});
    appearance = selected;
  }
  const remaining = new Set(managedAssets.map(asset => asset.id));
  for (const [id, promise] of fontLoads) if (/^[a-f0-9]{48}$/.test(id) && !remaining.has(id)) { fontLoads.delete(id); promise.then(face => document.fonts.delete(face)).catch(() => {}); }
  for (const id of assetData.keys()) if (!remaining.has(id)) forgetAssetURL(id);
  renderProxyChoices(); renderProxies();
  if ($('#appearance-dialog').open) renderAssets();
}
function appearanceSnapshot(value) {
  return structuredClone(Object.fromEntries(Object.entries(value).filter(([key, value]) => !appearanceOwnedElsewhere.has(key) && value !== undefined)));
}
function appearanceDifference(next, previous) {
  const patch = {};
  for (const [key, value] of Object.entries(next)) {
    if (appearanceMapFields.has(key)) {
      const changed = {}, before = previous[key] || {}, after = value || {};
      for (const id of new Set([...Object.keys(before), ...Object.keys(after)])) {
        if (JSON.stringify(before[id]) !== JSON.stringify(after[id])) changed[id] = after[id] ?? null;
      }
      if (Object.keys(changed).length) patch[key] = changed;
    } else if (JSON.stringify(value) !== JSON.stringify(previous[key])) patch[key] = value;
  }
  return patch;
}
function mergeAppearanceChanges(base, patch) {
  const result = { ...base };
  for (const [key, value] of Object.entries(patch)) {
    if (appearanceMapFields.has(key) || key === 'layout') {
      result[key] = { ...(base[key] || {}) };
      for (const [id, entry] of Object.entries(value || {})) {
        if (entry === null) delete result[key][id]; else result[key][id] = structuredClone(entry);
      }
    } else result[key] = value;
  }
  return result;
}
function persistAppearance(patch = {}, alreadyApplied = false) {
  // Compare against this frontend's last successful save, never against another
  // window's newer response. Maps are reduced to keys the user actually changed.
  if (!alreadyApplied) appearance = { ...appearance, ...patch };
  const snapshot = appearanceSnapshot(appearance), layoutPatch = patch.layout && structuredClone(patch.layout);
  const saving = appearanceSave.then(async () => {
    const changes = appearanceDifference(snapshot, appearanceSaved);
    if (layoutPatch && Object.keys(layoutPatch).length) changes.layout = layoutPatch;
    if (!Object.keys(changes).length) return structuredClone(appearance);
    const saved = await post('/api/appearance/patch', changes);
    appearanceSaved = snapshot;
    return saved;
  });
  appearanceSave = saving.catch(() => {});
  return saving;
}
async function chooseAppearance(patch) {
  window.DengFontLibrary?.selectionChanged(patch);
  const previous = { ...appearance }, operation = ++appearanceOperation;
  appearance = { ...appearance, ...patch };
  try { await applyAppearance(); if (operation !== appearanceOperation) return; await persistAppearance(patch, true); if ($('#appearance-dialog').open) renderAssets(); }
  catch (error) { if (operation !== appearanceOperation) return; appearance = previous; await applyAppearance().catch(() => {}); throw error; }
}
function applyUIScale() {
  // Scale the full logical viewport, including dialogs, without an overflowing page.
  const requested = Math.max(.5, Math.min(2, Number(appearance.uiScale) || 1));
  // The desktop minimum workspace width shrinks a phone UI to about 60%, making
  // touch targets and text unusably small. Android has its own responsive layout.
  effectiveScale = window.DENGSHELL_ANDROID ? 1 : Math.min(requested, Math.max(.5, Math.floor(Math.min(innerWidth / 640, innerHeight / 420) * 100) / 100));
  const width = logicalWidth(), height = logicalHeight(), root = document.documentElement;
  document.body.style.zoom = String(effectiveScale);
  document.body.style.width = `${width}px`; document.body.style.height = `${height}px`;
  root.style.setProperty('--view-width', `${width}px`); root.style.setProperty('--view-height', `${height}px`);
  if (width >= 760) { root.dataset.monitorOpen = 'false'; $('#monitor-backdrop').hidden = true; $('#toggle-monitor').setAttribute('aria-expanded', 'false'); }
  root.dataset.compact = width < 1000 ? 'true' : 'false'; root.dataset.narrow = width < 760 ? 'true' : 'false'; root.dataset.short = height < 650 ? 'true' : 'false';
  root.style.setProperty('--sidebar-default', `${width < 1000 ? 230 : width < 1350 ? 274 : 304}px`);
  root.style.setProperty('--files-default', `${Math.min(300, Math.max(height < 550 ? 115 : 150, height * .25))}px`);
  $('#ui-scale').value = String(requested);
  $('#scale-effective').textContent = effectiveScale < requested ? `当前窗口以 ${Math.round(effectiveScale * 100)}% 显示，放大窗口后恢复` : '';
  $('#scale-effective').hidden = effectiveScale >= requested;
  applyWorkspacePlacement();
  clampLayouts();
  if (!$('#settings-menu').hidden) positionSettings();
  clampAppearancePalette();
  requestAnimationFrame(fitActive);
}
function renderAppearanceControls() {
  $('#background-opacity').value = Math.round(appearance.backgroundOpacity * 100);
  $('#background-opacity-value').value = `${Math.round(appearance.backgroundOpacity * 100)}%`;
  $('#terminal-size').value = appearance.terminalFontSize;
  $('#font-preview-text').style.fontFamily = terminalFontFamily;
  $('#font-preview-text').style.fontSize = `${appearance.terminalFontSize}px`;
  $('#font-preview-text').style.fontWeight = boldForFont() ? '700' : '400';
  $('#font-preview-name').textContent = allFonts().find(font => font.id === activeFontID)?.name || '';
  for (const card of $$('#asset-list [data-font-id]')) reflectFontCardControls(card);
}
function setSettingsMenu(open) {
  $('#settings-menu').hidden = !open; $('#settings-button').setAttribute('aria-expanded', String(open));
  if (open) { positionSettings(); ($('#startup-animation').disabled ? $('#manage-keys') : $('#startup-animation')).focus(); }
}
function positionSettings() {
  const menu = $('#settings-menu'), rect = $('#settings-button').getBoundingClientRect();
  menu.style.left = `${Math.max(8, Math.min(rect.left / effectiveScale, logicalWidth() - menu.offsetWidth - 8))}px`;
  menu.style.top = `${rect.bottom / effectiveScale + 6}px`;
}
async function openAppearance(kind) {
  setSettingsMenu(false); assetKind = kind;
  await initializeAppearanceCatalogs();
  const isFont = kind === 'font';
  if (isFont) document.getElementById('ui-appearance-dialog')?.close();
  $('#appearance-title').textContent = isFont ? '字体设置 · Shell 字体' : '背景管理器';
  $('#appearance-description').textContent = isFont ? '每款字体单独设置加粗和颜色，点击字体即可应用。' : '背景立即生效，可拖动此面板观察终端。';
  $('#import-asset span').textContent = isFont ? '导入字体' : '导入背景';
  $('#asset-picker').accept = isFont ? '.ttf,.otf,.woff,.woff2' : '.png,.jpg,.jpeg,.webp';
  $('#background-strength').hidden = isFont; $('#font-preview').hidden = !isFont;
  $('#asset-list').classList.toggle('font-list', isFont);
  $('#appearance-dialog').dataset.kind = kind;
  $('#shell-font-navigation').hidden = !isFont; $('#shell-font-online-entry').hidden = !isFont;
  renderAppearanceControls(); renderAssets();
  if (!$('#appearance-dialog').open) $('#appearance-dialog').show();
  positionAppearancePalette();
}
function renderAssets() {
  const isFont = assetKind === 'font', assets = isFont ? allFonts() : allBackgrounds(), list = $('#asset-list');
  $('#asset-count').textContent = isFont ? '已安装字体' : `${assets.length} 种背景 · 支持 PNG / JPG / WebP`;
  if (list.dataset.kind !== assetKind) { list.replaceChildren(); list.dataset.kind = assetKind; }
  const existing = new Map([...list.children].map(card => [card.dataset.assetId, card]));
  const wanted = new Set(assets.map(asset => asset.id));
  for (const [id, card] of existing) if (!wanted.has(id)) card.remove();
  assets.forEach((asset, index) => {
    const card = existing.get(asset.id) || createAssetCard(asset, isFont);
    card._asset = asset;
    const selected = (isFont ? appearance.fontId : appearance.backgroundId) === asset.id;
    card.classList.toggle('selected', selected);
    const choose = card.querySelector('.asset-choice'); choose.setAttribute('aria-pressed', String(selected)); choose.title = `使用 ${asset.name}`;
    card.querySelector('.asset-caption strong').textContent = asset.name;
    card.querySelector('.asset-caption span').textContent = isFont && proportionalTerminalFonts.has(asset.id) ? `${selected ? '使用中 · ' : ''}英文采用等宽回退` : selected ? '正在使用 ✓' : asset.id.startsWith('builtin:') ? '内置' : asset.libraryId ? '已下载 · 可离线使用' : '自定义';
    if (isFont) reflectFontCardControls(card);
    // Existing cards retain their inputs, disclosure state, keyboard focus and scroll position.
    if (list.children[index] !== card) list.insertBefore(card, list.children[index] || null);
  });
}
function createAssetCard(asset, isFont) {
  const card = node('article', 'asset-card'); card.dataset.assetId = asset.id; card._asset = asset;
  const choose = node('button', 'asset-choice'); choose.type = 'button';
  const thumb = node('div', isFont ? 'font-sample' : 'background-thumb');
  if (isFont) {
    thumb.textContent = 'Aa 012 中文'; card.dataset.fontId = asset.id;
    thumb.style.fontFamily = `"${asset.family}", "Deng CJK", monospace`;
    loadFace(asset).catch(error => { if (thumb.isConnected) thumb.textContent = '预览不可用'; console.warn(error.message); });
  } else if (asset.id !== 'builtin:none') {
    assetURL(asset).then(url => { if (thumb.isConnected) thumb.style.backgroundImage = `url(${JSON.stringify(url)})`; }).catch(() => { if (thumb.isConnected) thumb.textContent = '无法读取'; });
  } else thumb.append(icon('terminal'));
  const caption = node('div', 'asset-caption'); caption.append(node('strong'), node('span'));
  choose.append(...(isFont ? [caption] : [thumb, caption])); choose.onclick = safe(() => chooseAppearance(isFont ? { fontId: asset.id } : { backgroundId: asset.id }));
  card.append(choose);
  if (isFont) {
    createFontCardControls(card, asset);
    const sampleChoice = node('button', 'font-sample-choice'); sampleChoice.type = 'button';
    sampleChoice.setAttribute('aria-label', `使用 ${asset.name}`); sampleChoice.onclick = choose.onclick;
    sampleChoice.append(thumb); card.querySelector('.font-card-toolbar').prepend(sampleChoice);
  }
  if (!asset.id.startsWith('builtin:')) {
    const actions = node('div', 'asset-actions'), rename = node('button', '', '改名'), del = node('button', 'danger-button', '删除'); rename.type = del.type = 'button';
    rename.onclick = safe(async () => { const name = await ask({ title: '修改名称', input: true, value: card._asset.name }); if (!name?.trim()) return; await flushFontStyleSave(); await post(`/api/assets/${asset.id}`, { name }); await loadProfiles(); });
    del.onclick = safe(async () => { if (!await ask({ title: `删除「${card._asset.name}」？`, description: '删除导入的本机副本，原始文件不受影响。', confirm: '删除' })) return; await flushFontStyleSave(); await remove(`/api/assets/${asset.id}`); forgetAssetURL(asset.id); await loadProfiles(); });
    actions.append(rename, del); card.append(actions);
  }
  return card;
}

let fontStyleSaveTimer, appearancePalettePosition = null, appearanceDrag = null;
function createFontCardControls(card, asset) {
  const bar = node('div', 'font-card-toolbar'), boldLabel = node('label', 'font-card-bold'), bold = node('input'); bold.type = 'checkbox'; bold.dataset.fontBold = asset.id;
  boldLabel.append(bold, document.createTextNode('加粗'));
  const colorButton = node('button', 'font-card-color-button'), dot = node('span', 'font-card-color-dot'); colorButton.type = 'button'; colorButton.dataset.fontColor = asset.id;
  colorButton.append(dot, document.createTextNode('颜色')); colorButton.setAttribute('aria-expanded', 'false');
  const editor = node('section', 'font-card-color-editor'); editor.id = `font-style-${asset.id.replace(/[^a-z0-9_-]/gi, '-')}`; editor.hidden = true;
  colorButton.setAttribute('aria-controls', 'font-color-dialog'); colorButton.setAttribute('aria-haspopup', 'dialog');
  const heading = node('div', 'font-color-heading'), reset = node('button', 'text-button', '恢复默认'); reset.type = 'button';
  heading.append(node('span', '', '文字颜色'), reset);
  const presets = node('div', 'font-color-presets');
  for (const preset of fontColorPresets) {
    const button = node('button', 'font-color-swatch'); button.type = 'button'; button.dataset.color = preset.color;
    button.title = `${preset.name} ${preset.color.toUpperCase()}`; button.setAttribute('aria-label', button.title);
    button.style.setProperty('--swatch-color', preset.color); button.onclick = () => previewFontColor(preset.color, asset.id); presets.append(button);
  }
  const custom = node('div', 'font-color-custom');
  const pickerLabel = node('label', 'font-color-picker-label', '色盘'), picker = node('input'); picker.type = 'color'; picker.dataset.fontColorPicker = asset.id;
  pickerLabel.append(picker);
  const hexLabel = node('label', 'font-color-hex-label', 'HEX'), hex = node('input'); hex.type = 'text'; hex.maxLength = 7; hex.spellcheck = false; hex.dataset.fontColorHex = asset.id;
  hexLabel.append(hex); custom.append(pickerLabel, hexLabel);
  const rgb = node('div', 'font-color-rgb'), channels = {};
  for (const [channel, name] of [['r', '红'], ['g', '绿'], ['b', '蓝']]) {
    const label = node('label', '', channel.toUpperCase()), input = node('input'); input.type = 'number'; input.min = 0; input.max = 255; input.step = 1; input.dataset.fontChannel = channel; channels[channel] = input;
    input.setAttribute('aria-label', `${asset.name} ${name}色通道`); label.append(input); rgb.append(label);
    input.oninput = () => {
      const values = Object.values(channels).map(field => field.value);
      if (values.length !== 3 || values.some(value => !/^\d{1,3}$/.test(value) || Number(value) > 255)) return;
      previewFontColor('#' + values.map(value => Number(value).toString(16).padStart(2, '0')).join(''), asset.id);
    };
    input.onblur = () => reflectFontCardControls(card, true);
  }
  const output = node('output', 'visually-hidden'); output.setAttribute('aria-live', 'polite');
  editor.append(heading, presets, custom, rgb, output); bar.append(boldLabel, colorButton); card.append(bar);
  card._fontControls = { bold, colorButton, dot, editor, picker, hex, channels, presets, output };
  bold.onchange = () => previewFontBold(bold.checked, asset.id);
  colorButton.onclick = () => openFontColorDialog(card);
  picker.oninput = event => previewFontColor(event.target.value, asset.id);
  hex.oninput = () => { const text = hex.value.trim(); previewFontColor(text.startsWith('#') ? text : '#' + text, asset.id); };
  hex.onblur = () => reflectFontCardControls(card, true);
  reset.onclick = () => previewFontColor(terminalDefaultColor, asset.id);
}
function reflectFontCardControls(card, force = false) {
  const controls = card._fontControls; if (!controls) return;
  const id = card.dataset.fontId, color = colorForFont(id), bold = boldForFont(id), name = card._asset.name;
  const preview = card.querySelector('.font-sample');
  preview.style.color = color; preview.style.fontWeight = bold ? '700' : '400';
  const channels = [1, 3, 5].map(offset => parseInt(color.slice(offset, offset + 2), 16) / 255);
  preview.dataset.tone = channels[0] * .2126 + channels[1] * .7152 + channels[2] * .0722 < .45 ? 'light' : 'dark';
  controls.bold.checked = bold; controls.bold.setAttribute('aria-label', `${name} 加粗`);
  controls.colorButton.title = `修改 ${name} 的文字颜色`; controls.dot.style.backgroundColor = color;
  controls.picker.setAttribute('aria-label', `${name} RGB 色盘`); controls.hex.setAttribute('aria-label', `${name} HEX 文字颜色`);
  if (force || document.activeElement !== controls.picker) controls.picker.value = color;
  if (force || document.activeElement !== controls.hex) controls.hex.value = color.toUpperCase();
  const values = [1, 3, 5].map(offset => parseInt(color.slice(offset, offset + 2), 16));
  Object.values(controls.channels).forEach((input, index) => { input.setAttribute('aria-label', `${name} RGB ${input.dataset.fontChannel.toUpperCase()}`); if (force || document.activeElement !== input) input.value = values[index]; });
  const description = `${name} RGB ${values.join(', ')}`;
  if (controls.output.textContent !== description) controls.output.textContent = description;
  for (const button of controls.presets.children) button.setAttribute('aria-pressed', String(button.dataset.color === color));
  reflectFontColorDialog(card);
}
function saveFontStylesSoon() {
  clearTimeout(fontStyleSaveTimer);
  fontStyleSaveTimer = setTimeout(() => { fontStyleSaveTimer = null; persistAppearance({}).catch(error => toast(`字体样式未能保存：${error.message}`)); }, 180);
}
function flushFontStyleSave() {
  if (fontStyleSaveTimer) { clearTimeout(fontStyleSaveTimer); fontStyleSaveTimer = null; return persistAppearance({}); }
  return appearanceSave;
}
function reflectEditedFont(id) {
  for (const card of $$('#asset-list [data-font-id]')) if (card.dataset.fontId === id) reflectFontCardControls(card);
  if (id !== appearance.fontId) return;
  applyTerminalAppearanceColors();
  for (const state of sessions.values()) state.term.options.fontWeight = boldForFont() ? '700' : '400';
  $('#font-preview-text').style.fontWeight = boldForFont() ? '700' : '400'; requestAnimationFrame(fitActive);
}
function previewFontColor(color, id = appearance.fontId) {
  if (!validFontColor(color)) return false;
  appearance = { ...appearance, fontColors: { ...appearance.fontColors, [id]: color.toLowerCase() } };
  reflectEditedFont(id); saveFontStylesSoon(); return true;
}
function previewFontBold(bold, id = appearance.fontId) {
  appearance = { ...appearance, fontBold: { ...appearance.fontBold, [id]: !!bold } };
  reflectEditedFont(id); saveFontStylesSoon();
}
function clampAppearancePalette() {
  const dialog = document.querySelector('#ui-appearance-dialog[open]') || document.getElementById('appearance-dialog');
  if (!dialog?.open || !appearancePalettePosition) return;
  appearancePalettePosition.x = Math.max(8, Math.min(appearancePalettePosition.x, logicalWidth() - dialog.offsetWidth - 8));
  appearancePalettePosition.y = Math.max(8, Math.min(appearancePalettePosition.y, logicalHeight() - dialog.offsetHeight - 8));
  dialog.style.left = `${appearancePalettePosition.x}px`; dialog.style.top = `${appearancePalettePosition.y}px`;
}
function positionAppearancePalette() {
  if (!appearancePalettePosition) appearancePalettePosition = { x: logicalWidth() - $('#appearance-dialog').offsetWidth - 18, y: 54 };
  clampAppearancePalette();
}
function initializeAppearancePalette(dialog = $('#appearance-dialog'), handle = $('#appearance-drag-handle')) {
  dialog.setAttribute('aria-modal', 'false');
  const saved = readSaved('dengshell.appearance-palette', null);
  if (saved && Number.isFinite(saved.x) && Number.isFinite(saved.y)) appearancePalettePosition = { x: saved.x, y: saved.y };
  handle.addEventListener('pointerdown', event => {
    if (event.button !== 0 || event.target.closest('button, input, select')) return;
    const rect = dialog.getBoundingClientRect();
    appearanceDrag = { id: event.pointerId, x: event.clientX / effectiveScale, y: event.clientY / effectiveScale, left: rect.left / effectiveScale, top: rect.top / effectiveScale };
    handle.setPointerCapture(event.pointerId); dialog.classList.add('is-dragging'); event.preventDefault();
  });
  handle.addEventListener('pointermove', event => {
    if (appearanceDrag?.id !== event.pointerId) return;
    appearancePalettePosition = { x: appearanceDrag.left + event.clientX / effectiveScale - appearanceDrag.x, y: appearanceDrag.top + event.clientY / effectiveScale - appearanceDrag.y };
    clampAppearancePalette();
  });
  const finishDrag = event => {
    if (appearanceDrag?.id !== event.pointerId) return;
    appearanceDrag = null; dialog.classList.remove('is-dragging');
    if (handle.hasPointerCapture(event.pointerId)) handle.releasePointerCapture(event.pointerId);
    if (appearancePalettePosition) save('dengshell.appearance-palette', appearancePalettePosition);
  };
  handle.addEventListener('pointerup', finishDrag); handle.addEventListener('pointercancel', finishDrag);
  handle.addEventListener('keydown', event => {
    if (event.target !== handle || !['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', 'Home'].includes(event.key)) return;
    event.preventDefault(); positionAppearancePalette();
    if (event.key === 'Home') appearancePalettePosition = { x: logicalWidth() - dialog.offsetWidth - 18, y: 54 };
    else { const amount = event.shiftKey ? 40 : 10; if (event.key === 'ArrowLeft') appearancePalettePosition.x -= amount; if (event.key === 'ArrowRight') appearancePalettePosition.x += amount; if (event.key === 'ArrowUp') appearancePalettePosition.y -= amount; if (event.key === 'ArrowDown') appearancePalettePosition.y += amount; }
    clampAppearancePalette(); save('dengshell.appearance-palette', appearancePalettePosition);
  });
  dialog.addEventListener('close', () => {
    flushFontStyleSave().catch(error => toast(error.message));
  });
  document.addEventListener('keydown', event => {
    if (event.key === 'Escape' && dialog.open && !$$('dialog[open]').some(other => other !== dialog)) { dialog.close(); event.preventDefault(); }
  });

}
async function importAsset() {
  const kind = assetKind, button = $('#import-asset');
  if (!native()?.ChooseAsset) { $('#asset-picker').click(); return; }
  button.disabled = true;
  try { await flushFontStyleSave(); const asset = await native().ChooseAsset(kind); if (!asset) return; await loadProfiles(); await chooseAppearance(kind === 'font' ? { fontId: asset.id } : { backgroundId: asset.id }); toast('已导入并应用'); }
  finally { button.disabled = false; }
}
function renderProxyChoices() {
  const select = $('#profile-proxy'), value = select.value;
  const custom = node('option', '', '直连 / 自定义代理'); custom.value = '';
  select.replaceChildren(custom, ...managedProxies.map(item => { const option = node('option', '', `${item.name} · ${item.proxy.type.toUpperCase()}`); option.value = item.id; return option; }));
  select.value = managedProxies.some(item => item.id === value) ? value : '';
}
function renderProxies() {
  $('#proxies-empty').hidden = managedProxies.length > 0;
  $('#proxy-list').replaceChildren(...managedProxies.map(item => {
    const row = node('article', 'key-card'), detail = node('div', 'key-detail'), actions = node('div', 'key-actions');
    const bound = profiles.filter(profile => profile.proxyId === item.id).length;
    detail.append(node('strong', '', item.name), node('small', '', `${item.proxy.type.toUpperCase()} · ${item.proxy.host}:${item.proxy.port}`), node('small', '', `${bound} 台服务器${item.proxy.hasPassword ? ' · 已保存认证' : ''}`));
    const edit = node('button', '', '编辑'), del = node('button', 'danger-button', '删除');
    edit.onclick = () => editProxy(item); del.onclick = safe(async () => { if (!await ask({ title: `删除代理「${item.name}」？`, confirm: '删除' })) return; await remove(`/api/proxies/${item.id}`); await loadProfiles(); });
    actions.append(edit, del); row.append(icon('network'), detail, actions); return row;
  }));
}
function editProxy(item = null) {
  const form = $('#proxy-editor-form'); form.reset(); form.elements.id.value = item?.id || ''; form.elements.name.value = item?.name || '';
  if (item) for (const name of ['type', 'host', 'port', 'user']) form.elements[name].value = item.proxy[name] || '';
  form.elements.password.placeholder = item?.proxy.hasPassword ? '已保存，留空保留' : '代理密码（可选）';
  $('#proxy-editor-title').textContent = item ? '编辑代理' : '新建代理'; $('#proxy-editor-dialog').showModal(); form.elements.name.focus();
}
function showProxies() { setSettingsMenu(false); renderProxies(); $('#proxies-dialog').showModal(); }
function reflectWindowState(state) {
  $('#window-controls').hidden = !state?.frameless;
  document.documentElement.classList.toggle('frameless', !!state?.frameless);
  $('#window-maximise').title = state?.maximised ? '还原窗口' : '最大化'; $('#window-maximise').setAttribute('aria-label', $('#window-maximise').title);
  $('#window-maximise use').setAttribute('href', state?.maximised ? '#i-restore' : '#i-maximise');
}
async function initializeWindowControls() { if (native()?.WindowState) reflectWindowState(await native().WindowState()); }
function initializeAppearance() {
  window.DengUIAppearance?.initialize();
  initializeAppearancePalette();
  $('#terminal-size').replaceChildren(...Array.from({ length: 65 }, (_, i) => { const value=8+i/2,option = node('option', '', `${value} px`); option.value = value; return option; }));
  $('#toggle-monitor').onclick = () => { const open = document.documentElement.dataset.monitorOpen !== 'true'; document.documentElement.dataset.monitorOpen = String(open); $('#monitor-backdrop').hidden = !open; $('#toggle-monitor').setAttribute('aria-expanded', String(open)); };
  $('#monitor-backdrop').onclick = () => { document.documentElement.dataset.monitorOpen = 'false'; $('#monitor-backdrop').hidden = true; $('#toggle-monitor').setAttribute('aria-expanded', 'false'); };
  $('#settings-button').onclick = () => setSettingsMenu($('#settings-menu').hidden);
  document.addEventListener('click', event => { if (!event.target.closest('#settings-menu, #settings-button')) setSettingsMenu(false); });
  $('#settings-menu').addEventListener('click', event => { if (event.target.closest('button')) setSettingsMenu(false); });
  $('#settings-menu').addEventListener('keydown', event => {
    if (!['ArrowUp', 'ArrowDown', 'Home', 'End'].includes(event.key) || event.target.tagName === 'SELECT') return;
    const buttons = [...$('#settings-menu').querySelectorAll('input[type=checkbox],button')].filter(button => !button.disabled && button.getClientRects().length), index = buttons.indexOf(document.activeElement);
    event.preventDefault(); buttons[event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : -1) + buttons.length) % buttons.length].focus();
  });
  document.addEventListener('keydown', event => { if (event.key === 'Escape' && !$('#monitor-backdrop').hidden) $('#monitor-backdrop').click(); if (event.key === 'Escape' && !$('#settings-menu').hidden) { setSettingsMenu(false); $('#settings-button').focus(); } });
  $('#manage-backgrounds').onclick = safe(() => openAppearance('background'));
  $('#manage-shell-fonts').onclick = safe(() => openAppearance('font'));
  $('#switch-ui-fonts').onclick = safe(() => window.DengUIAppearance.open());
  $('#online-shell-fonts').onclick = safe(() => window.DengFontLibrary.open('font'));
  $('#manage-proxies').onclick = showProxies; $('#manage-profile-proxies').onclick = showProxies;
  $('#close-appearance').onclick = () => $('#appearance-dialog').close(); $('#import-asset').onclick = safe(importAsset);
  $('#asset-picker').onchange = safe(async event => {
    const file = event.target.files[0], kind = assetKind; event.target.value = ''; if (!file) return;
    if (file.size > (kind === 'font' ? 64 : 32) * 1024 * 1024) throw new Error('文件太大，请选择更小的文件');
    const button = $('#import-asset'); button.disabled = true;
    try {
      const dataURL = await new Promise((resolve, reject) => { const reader = new FileReader(); reader.onload = () => resolve(reader.result); reader.onerror = () => reject(new Error('无法读取文件')); reader.readAsDataURL(file); });
      await flushFontStyleSave();
      const asset = await post('/api/assets', { kind, name: file.name.replace(/\.[^.]+$/, ''), filename: file.name, data: dataURL.split(',')[1] });
      await loadProfiles(); await chooseAppearance(kind === 'font' ? { fontId: asset.id } : { backgroundId: asset.id }); toast('已导入并应用');
    } finally { button.disabled = false; }
  });
  $('#background-opacity').oninput = event => { appearance.backgroundOpacity = Number(event.target.value) / 100; document.documentElement.style.setProperty('--background-opacity', appearance.backgroundOpacity); renderAppearanceControls(); };
  $('#background-opacity').onchange = safe(() => persistAppearance({}));
  $('#terminal-size').onchange = safe(event => chooseAppearance({ terminalFontSize: Number(event.target.value) }));
  $('#ui-scale').onchange = safe(event => chooseAppearance({ uiScale: Number(event.target.value) }));
  $('#profile-proxy').onchange = updateProxyFields;
  $('#close-proxies').onclick = () => $('#proxies-dialog').close(); $('#new-proxy').onclick = () => editProxy();
  $('#close-proxy-editor').onclick = () => $('#proxy-editor-dialog').close();
  $('#proxy-editor-dialog').addEventListener('close', () => $('#proxy-editor-form').reset());
  $('#proxy-editor-form').onsubmit = safe(async event => {
    event.preventDefault(); const form = event.currentTarget, fields = Object.fromEntries(new FormData(form)), button = $('#save-proxy'); if (button.disabled) return; button.disabled = true;
    try { const saved = await post('/api/proxies', { id: fields.id, name: fields.name, proxy: { type: fields.type, host: fields.host, port: Number(fields.port), user: fields.user, password: fields.password, clearPassword: form.elements.clearPassword.checked } }); await loadProfiles(); $('#proxy-editor-dialog').close(); if ($('#connection-dialog').open) { $('#profile-proxy').value = saved.id; updateProxyFields(); } toast('代理已保存，下次连接时生效'); }
    finally { button.disabled = false; }
  });
  for (const button of $$('[data-window-action]')) button.onclick = safe(async () => reflectWindowState(await native().WindowAction(button.dataset.windowAction)));
  $('.titlebar').ondblclick = safe(async event => { if (native()?.WindowAction && !$('#window-controls').hidden && getComputedStyle(event.target).getPropertyValue('--wails-draggable').trim() === 'drag') reflectWindowState(await native().WindowAction('toggle-maximise')); });
  window.addEventListener('resize', () => { applyUIScale(); if (native()?.WindowState) native().WindowState().then(reflectWindowState).catch(() => {}); });
}
