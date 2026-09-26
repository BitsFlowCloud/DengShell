'use strict';

// Each panel owns its controls and query. Every command additionally checks
// ownership, so a queued event from a hidden or moved tab cannot edit a file.
window.DengEditorTools = (doc, changed) => {
  const ui = doc.ui, area = ui.area;
  const available = () => !doc.closed && !doc.loading && !!doc.model && doc.win.active === doc && doc.win.element.open;
  const button = (text, action, title = text) => { const e = node('button', 'upload-button', text); e.type = 'button'; e.title = title; e.onclick = action; return e; };
  const input = (label, cls) => { const e = node('input', cls); e.type = 'text'; e.placeholder = label; e.setAttribute('aria-label', label); e.autocomplete = 'off'; e.spellcheck = false; return e; };
  const bar = node('div', 'text-editor-search'); bar.hidden = true; bar.setAttribute('role', 'search'); bar.setAttribute('aria-label', '仅搜索当前文件');
  const row = node('div', 'text-editor-search-row'), query = input('搜索当前文件', 'text-editor-find');
  const caseLabel = node('label', 'text-editor-match-case'), matchCase = node('input'); matchCase.type = 'checkbox'; caseLabel.append(matchCase, '区分大小写');
  const status = node('span', 'text-editor-search-status'); status.setAttribute('role', 'status');
  const replaceRow = node('div', 'text-editor-search-row text-editor-replace-row'), replacement = input('替换为', 'text-editor-replacement');
  replaceRow.hidden = true;
  const goto = node('div', 'text-editor-goto'); goto.hidden = true;
  const line = input('行号', 'text-editor-line'); line.type = 'number'; line.min = '1'; line.step = '1';
  const lineStatus = node('span', 'text-editor-line-status'); lineStatus.setAttribute('role', 'status');
  const info = () => DengTextSearch.scan(area.value, query.value, matchCase.checked, area.selectionStart, area.selectionEnd);
  function refresh() {
    const enabled = !!doc.model && !doc.loading && !doc.closed;
    for (const e of [...buttons, ...bar.querySelectorAll('input,button'), ...goto.querySelectorAll('input,button')]) e.disabled = !enabled;
    if (!bar.hidden && enabled) { const result = info(); status.textContent = !query.value ? '仅当前文件' : result.count ? `${result.current} / ${result.count}` : '未找到'; }
  }
  function select(range) {
    if (!range || !available()) return;
    area.focus({ preventScroll: true }); area.setSelectionRange(...range);
    const style = getComputedStyle(area), before = area.value.slice(0, range[0]), lineNumber = before.split('\n').length - 1;
    area.scrollTop = Math.max(0, lineNumber * parseFloat(style.lineHeight) - area.clientHeight / 2);
    // Approximate horizontal reveal using measured monospace text, including tabs.
    const prefix = before.slice(before.lastIndexOf('\n') + 1).replace(/\t/g, '    ');
    const canvas = document.createElement('canvas'), context = canvas.getContext('2d'); context.font = style.font;
    area.scrollLeft = Math.max(0, context.measureText(prefix).width - area.clientWidth / 2);
    refresh();
  }
  function find(backwards = false) { if (available() && query.value) { const result = info(); select(backwards ? result.previous : result.next); refresh(); } }
  function replaceOne() {
    if (!available() || !query.value) return;
    if (!info().current) { find(); return; }
    const caret = doc.model.replace(area.selectionStart, area.selectionEnd, replacement.value);
    area.value = doc.model.visible; area.setSelectionRange(caret, caret); changed(); find();
  }
  function replaceAll() {
    if (!available() || !query.value) return;
    const result = DengTextSearch.replaceAll(doc.model.raw, query.value, replacement.value, matchCase.checked);
    if (result.raw !== doc.model.raw) { doc.model.checkpoint(); doc.model.raw = result.raw; area.value = doc.model.visible; area.setSelectionRange(0, 0); changed(); }
    refresh(); status.textContent = `已替换 ${result.count} 处（当前文件）`;
  }
  function go() {
    if (!available()) return;
    const range = DengTextSearch.lineRange(area.value, Number(line.value));
    if (!range) { lineStatus.textContent = `请输入 1–${area.value.split('\n').length} 的整数行号`; line.setAttribute('aria-invalid', 'true'); return; }
    line.removeAttribute('aria-invalid'); lineStatus.textContent = `第 ${line.value} 行`; select(range);
  }
  function close() { bar.hidden = true; goto.hidden = true; if (available()) area.focus({ preventScroll: true }); }
  function open(mode) {
    if (!available()) return;
    const selected = area.value.slice(area.selectionStart, area.selectionEnd);
    bar.hidden = mode === 'line'; goto.hidden = mode !== 'line'; replaceRow.hidden = mode !== 'replace';
    if (mode === 'line') { line.value = String(area.value.slice(0, area.selectionStart).split('\n').length); lineStatus.textContent = `共 ${area.value.split('\n').length} 行 · 仅当前文件`; line.removeAttribute('aria-invalid'); line.focus(); line.select(); }
    else { if (selected && !selected.includes('\n')) query.value = selected; refresh(); query.focus(); query.select(); }
  }
  row.append(query, button('上一个', () => find(true), '上一个匹配（Shift+F3）'), button('下一个', () => find(), '下一个匹配（F3）'), caseLabel, status, button('×', close, '关闭搜索（Esc）'));
  row.lastChild.setAttribute('aria-label', '关闭搜索');
  replaceRow.append(replacement, button('替换', replaceOne), button('全部替换', replaceAll, '仅替换当前文件中的全部匹配'));
  bar.append(row, replaceRow);
  goto.append(line, button('定位', go), lineStatus, button('×', close, '关闭定位（Esc）')); goto.lastChild.setAttribute('aria-label', '关闭定位');
  query.addEventListener('input', refresh); matchCase.addEventListener('change', refresh);
  bar.addEventListener('keydown', event => { if (event.key === 'Enter' && !event.isComposing && event.target.tagName === 'INPUT') { event.preventDefault(); find(event.shiftKey); } });
  line.addEventListener('keydown', event => { if (event.key === 'Enter') { event.preventDefault(); go(); } });
  area.addEventListener('select', refresh);
  const buttons = [button('搜索', () => open('find'), '搜索当前文件（Ctrl+F）'), button('替换', () => open('replace'), '替换当前文件（Ctrl+H）'), button('定位行', () => open('line'), '定位当前文件的行（Ctrl+G）')];
  function keydown(event) {
    if (event.defaultPrevented || event.isComposing || !available()) return false;
    const ctrl = event.ctrlKey || event.metaKey, key = event.key.toLowerCase();
    if (ctrl && !event.altKey && ['f', 'h', 'g'].includes(key)) { event.preventDefault(); event.stopPropagation(); open({ f: 'find', h: 'replace', g: 'line' }[key]); return true; }
    if (event.key === 'F3') { event.preventDefault(); event.stopPropagation(); if (query.value) find(event.shiftKey); else open('find'); return true; }
    if (event.key === 'Escape' && (!bar.hidden || !goto.hidden)) { event.preventDefault(); event.stopPropagation(); close(); return true; }
    return false;
  }
  return { buttons, bar, goto, refresh, keydown };
};
