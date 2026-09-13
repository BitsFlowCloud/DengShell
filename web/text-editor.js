'use strict';

window.DengTextEditors = (() => {
  const documents = new Map(), windows = [];
  let serial = 0, lastWindow, dock;
  const encodings = [['auto','自动识别'],['utf-8','UTF-8'],['utf-8-bom','UTF-8 BOM'],['utf-16le-bom','UTF-16 LE BOM'],['utf-16be-bom','UTF-16 BE BOM'],['utf-16le','UTF-16 LE'],['utf-16be','UTF-16 BE'],['gb18030','GB18030'],['gbk','GBK'],['big5','Big5']];
  const keyFor = (state, path) => JSON.stringify([state.id, path]);
  const connected = doc => !doc.closed && doc.state.connected && !doc.state.closed && !doc.state.detaching && !doc.state.restoring && !doc.state.ownershipUncertain && sessions.get(doc.state.id) === doc.state;
  const copyText = text => native()?.WriteClipboard ? native().WriteClipboard(text) : navigator.clipboard.writeText(text);
  const button = (label, cls, action) => { const b = node('button', cls, label); b.type = 'button'; b.onclick = safe(action); return b; };
  const isDirty = () => [...documents.values()].some(d => d.dirty);
  function reflect() {
    for (const doc of documents.values()) update(doc);
    if (dock) { dock.hidden = !documents.size; dock.textContent = `文本编辑器 (${documents.size})${isDirty() ? ' *' : ''}`; }
  }
  function raise(win) {
    const index = windows.indexOf(win); if (index < 0) return;
    windows.splice(index, 1); windows.push(win); windows.forEach((w, i) => { w.element.style.zIndex = String(50 + i); });
    lastWindow = win;
  }
  function constrain(win) {
    const e = win.element, scale = typeof effectiveScale === 'number' ? effectiveScale : 1;
    if (!e.open) return;
    const r = e.getBoundingClientRect();
    e.style.left = Math.max(4, Math.min(r.left / scale, (innerWidth - r.width - 4) / scale)) + 'px';
    e.style.top = Math.max(4, Math.min(r.top / scale, (innerHeight - r.height - 4) / scale)) + 'px';
  }
  function dragWindow(win, handle) {
    handle.addEventListener('pointerdown', event => {
      if (event.button || event.target.closest('button,select,input')) return;
      const r = win.element.getBoundingClientRect(), scale = typeof effectiveScale === 'number' ? effectiveScale : 1;
      const x = event.clientX, y = event.clientY;
      handle.setPointerCapture(event.pointerId);
      const move = e => { win.element.style.left = (r.left + e.clientX - x) / scale + 'px'; win.element.style.top = (r.top + e.clientY - y) / scale + 'px'; constrain(win); };
      const end = () => { handle.removeEventListener('pointermove', move); handle.removeEventListener('pointerup', end); handle.removeEventListener('pointercancel', end); };
      handle.addEventListener('pointermove', move); handle.addEventListener('pointerup', end); handle.addEventListener('pointercancel', end);
    });
  }
  function show(win, doc = win.active) {
    win.active = doc; if (!win.element.open) win.element.show();
    raise(win); renderTabs(win); constrain(win);
    doc?.ui.area.focus({ preventScroll: true });
  }
  function renderTabs(win) {
    win.tabs.replaceChildren(...win.docs.map(doc => {
      const tab = node('div', 'text-editor-tab'); tab.dataset.documentKey = doc.key;
      const active = win.active === doc;
      const select = button('', 'text-editor-tab-select', () => show(win, doc)); select.setAttribute('role', 'tab'); select.setAttribute('aria-selected', String(active)); select.setAttribute('aria-controls', doc.ui.panel.id);
      select.append(node('span', 'text-editor-tab-server', doc.owner), node('span', '', `${doc.path.split('/').pop() || '/'}${doc.dirty ? ' *' : ''}`));
      if (active) select.append(node('span', 'text-editor-current', '当前查看'));
      select.title = `${doc.address}\n${doc.path}${connected(doc) ? '' : '\n原连接不可用，可复制或保留草稿'}`;
      const close = button('×', 'text-editor-tab-close', () => closeDoc(doc)); close.setAttribute('aria-label', `关闭 ${doc.owner} · ${doc.path}`);
      tab.append(select, close); doc.ui.panel.hidden = !active; return tab;
    }));
    const doc = win.active;
    win.title.textContent = doc ? `${doc.owner} · ${doc.path}` : '文本编辑器';
    win.title.title = doc ? `${doc.address}\n${doc.path}` : '';
    win.detach.disabled = !doc;
    win.merge.disabled = windows.length < 2;
  }
  function removeWindow(win) {
    win.element.close(); win.element.remove(); windows.splice(windows.indexOf(win), 1);
    if (lastWindow === win) lastWindow = windows.at(-1);
    windows.forEach(renderTabs);
  }
  async function closeDoc(doc) {
    if (doc.closing || doc.closed) return false;
    if (doc.saving) { toast('此文件正在保存，请等待保存完成后再关闭'); return false; }
    doc.closing = true;
    try {
      const raw = doc.model?.raw;
      if (doc.dirty && !await ask({ title: '放弃未保存的修改？', description: `${doc.owner} · ${doc.address}\n${doc.path}`, confirm: '放弃修改' })) return false;
      if (doc.saving || doc.model?.raw !== raw) { toast('文件内容已变化，请检查后重新关闭'); return false; }
      const win = doc.win; doc.close(); documents.delete(doc.key); doc.ui.panel.remove();
      const i = win.docs.indexOf(doc); win.docs.splice(i, 1);
      if (win.active === doc) win.active = win.docs[Math.min(i, win.docs.length - 1)];
      if (!win.docs.length) removeWindow(win); else renderTabs(win);
      reflect(); return true;
    } finally { doc.closing = false; }
  }
  async function closeWindow(win) {
    for (const doc of [...win.docs]) { if (!await closeDoc(doc)) break; }
  }
  function moveDoc(doc, win) {
    const previous = doc.win; if (previous === win) return;
    previous.docs.splice(previous.docs.indexOf(doc), 1); if (previous.active === doc) previous.active = previous.docs[0];
    doc.win = win; win.docs.push(doc); win.body.append(doc.ui.panel); win.active = doc;
    if (!previous.docs.length) removeWindow(previous); else renderTabs(previous);
    show(win, doc); windows.forEach(renderTabs);
  }
  function makeWindow() {
    const win = { docs: [], active: null }, e = node('dialog', 'text-editor-dialog'); win.element = e;
    e.id = `text-editor-window-${++serial}`; e.setAttribute('aria-modal', 'false');
    const head = node('div', 'dialog-heading text-editor-drag-handle'); win.title = node('h2'); win.title.id = e.id + '-title'; e.setAttribute('aria-labelledby', win.title.id);
    const actions = node('div', 'text-editor-window-actions');
    actions.append(button('收起', 'text-button', () => e.close()), button('×', 'icon-button', () => closeWindow(win)));
    actions.lastChild.setAttribute('aria-label', '关闭此编辑窗口'); head.append(win.title, actions);
    win.tabs = node('div', 'text-editor-tabs'); win.tabs.setAttribute('role', 'tablist'); win.tabs.setAttribute('aria-label', '已打开的远程文件');
    const layout = node('div', 'text-editor-layout-tools');
    win.detach = button('移到新窗口', 'upload-button', () => { if (windows.length >= 12) { toast('最多同时打开 12 个编辑窗口'); return; } if (win.active) moveDoc(win.active, makeWindow()); });
    win.merge = button('合并到另一窗口', 'upload-button', () => { const target = windows.find(w => w !== win); if (target && win.active) moveDoc(win.active, target); });
    layout.append(node('span', '', '窗口可拖动；右下角可调整大小'), win.detach, win.merge);
    win.body = node('div', 'text-editor-panels'); e.append(head, win.tabs, layout, win.body);
    e.addEventListener('pointerdown', () => raise(win), true);
    e.addEventListener('cancel', event => { event.preventDefault(); e.close(); });
    e.addEventListener('keydown', event => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's') {
        const handled = event.defaultPrevented; event.preventDefault();
        if (!handled && win.active) safe(() => save(win.active))();
        return;
      }
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'w') { event.preventDefault(); safe(() => closeDoc(win.active))(); }
      if (event.key === 'Escape') { event.preventDefault(); e.close(); }
      if ((event.ctrlKey || event.metaKey) && event.key === 'Tab') { event.preventDefault(); const i = win.docs.indexOf(win.active); show(win, win.docs[(i + (event.shiftKey ? -1 : 1) + win.docs.length) % win.docs.length]); }
    });
    dragWindow(win, head); document.body.append(e); windows.push(win);
    const scale = typeof effectiveScale === 'number' ? effectiveScale : 1;
    e.style.left = Math.max(4, (innerWidth / scale - 900) / 2 + (windows.length - 1) * 24) + 'px';
    e.style.top = Math.max(4, 55 + (windows.length - 1) * 24) + 'px';
    return win;
  }
  function update(doc, replace = false) {
    if (!doc.ui || doc.closed) return;
    const ui = doc.ui, live = connected(doc), model = doc.model;
    if (replace && model) { ui.area.value = model.visible; ui.area.setSelectionRange(0, 0); }
    ui.area.disabled = doc.loading || !model;
    ui.encoding.disabled = doc.loading || doc.saving || !live;
    if (doc.baseline) { ui.encoding.value = doc.baseline.encoding; window.DengSelect?.refresh(ui.encoding); }
    ui.save.disabled = doc.loading || doc.saving || !doc.dirty || !live;
    ui.reload.disabled = doc.loading || doc.saving || !live;
    ui.copy.disabled = !model;
    const eol = [...new Set(model?.raw.match(/\r\n|\r|\n/g) || [])].map(s => s === '\r\n' ? 'CRLF' : s === '\r' ? 'CR' : 'LF').join(' + ') || '无换行';
    ui.status.textContent = [doc.loading ? '正在读取…' : doc.saving ? '正在保存…' : doc.dirty ? '未保存' : doc.baseline ? '已保存' : '', !live ? '原连接不可用，草稿保留，可复制' : '', doc.error || '', doc.baseline ? `${doc.baseline.encoding} · ${eol} · ${model.raw.length.toLocaleString()} 字符` : ''].filter(Boolean).join(' · ');
    renderTabs(doc.win);
    if (dock) { dock.hidden = !documents.size; dock.textContent = `文本编辑器 (${documents.size})${isDirty() ? ' *' : ''}`; }
  }
  async function reload(doc, encoding = 'auto') {
    if (!connected(doc)) throw new Error('原 SSH 连接已不可用，草稿已保留');
    const raw = doc.model?.raw;
    if (doc.dirty && !await ask({ title: '重新读取原文件？', description: `${doc.owner} · ${doc.path}\n重新读取会放弃此文件尚未保存的修改。`, confirm: '重新读取' })) { update(doc); return; }
    if (doc.closed || doc.model?.raw !== raw || !connected(doc)) { update(doc); return; }
    await doc.load(encoding);
  }
  async function save(doc) {
    if (!connected(doc)) throw new Error('原 SSH 连接已不可用，未保存到其他连接；可复制当前草稿');
    if (!await doc.save()) return;
    toast(`已保存到 ${doc.owner}：${doc.path}`);
    if (connected(doc)) {
      try { DengFileBrowser.invalidate(doc.state, doc.state.cwd); await navigate(doc.state.cwd, doc.state); }
      catch { toast('文件已保存，目录刷新失败，可稍后手动刷新'); }
    }
  }
  function makePanel(doc) {
    const ui = {}; doc.ui = ui;
    ui.panel = node('section', 'text-editor-panel'); ui.panel.id = `text-editor-document-${++serial}`; ui.panel.setAttribute('role', 'tabpanel');
    const owner = node('p', 'text-editor-owner', `服务器：${doc.owner} · ${doc.address}\n文件：${doc.path}`); owner.title = owner.textContent;
    const toolbar = node('div', 'text-editor-toolbar'), label = node('label', '', '编码'); ui.encoding = node('select'); ui.encoding.setAttribute('aria-label', '原文件编码');
    for (const [value, name] of encodings) { const option = node('option', '', name); option.value = value; ui.encoding.append(option); }
    ui.encoding.onchange = safe(() => reload(doc, ui.encoding.value)); label.append(ui.encoding);
    ui.reload = button('重新读取', 'upload-button', () => reload(doc, ui.encoding.value));
    ui.copy = button('复制全文', 'upload-button', async () => { if (doc.model) { await copyText(doc.model.raw); toast('已复制原样文本'); } });
    toolbar.append(label, ui.reload, ui.copy);
    const area = ui.area = node('textarea', 'text-editor-area'); area.spellcheck = false; area.autocapitalize = 'off'; area.autocomplete = 'off'; area.wrap = 'off'; area.setAttribute('aria-label', `${doc.owner} · ${doc.path}`);
    const sync = caret => { area.value = doc.model.visible; if (caret != null) area.setSelectionRange(caret, caret); update(doc); };
    area.oninput = () => { if (doc.model && !doc.loading) { doc.model.edit(area.value); update(doc); } };
    area.addEventListener('paste', event => {
      if (!doc.model || doc.loading) return; event.preventDefault();
      const model = doc.model, raw = model.raw, start = area.selectionStart, end = area.selectionEnd;
      const insert = text => { if (!doc.closed && !doc.loading && doc.win.active === doc && doc.win.element.open && doc.model === model && model.raw === raw && text != null) sync(model.replace(start, end, text)); };
      if (native()?.ReadClipboard) safe(async () => insert(await native().ReadClipboard()))(); else insert(event.clipboardData?.getData('text/plain'));
    });
    area.addEventListener('copy', event => {
      if (!doc.model || area.selectionStart === area.selectionEnd) return; event.preventDefault();
      const text = doc.model.raw.slice(doc.model.rawOffset(area.selectionStart), doc.model.rawOffset(area.selectionEnd));
      if (native()?.WriteClipboard) safe(() => copyText(text))(); else event.clipboardData.setData('text/plain', text);
    });
    area.addEventListener('cut', event => {
      if (!doc.model || doc.loading || area.selectionStart === area.selectionEnd) return; event.preventDefault();
      const model = doc.model, raw = model.raw, start = area.selectionStart, end = area.selectionEnd;
      const text = raw.slice(model.rawOffset(start), model.rawOffset(end));
      const erase = () => { if (!doc.closed && !doc.loading && doc.model === model && model.raw === raw) sync(model.replace(start, end, '')); };
      if (native()?.WriteClipboard) safe(async () => { await copyText(text); erase(); })(); else { event.clipboardData.setData('text/plain', text); erase(); }
    });
    area.addEventListener('keydown', event => {
      const ctrl = event.ctrlKey || event.metaKey;
      if (ctrl && event.key.toLowerCase() === 's') { event.preventDefault(); safe(() => save(doc))(); return; }
      if (!doc.model || doc.loading) return;
      if (ctrl && ['z','y'].includes(event.key.toLowerCase())) { event.preventDefault(); if (event.shiftKey || event.key.toLowerCase() === 'y') doc.model.redo(); else doc.model.undo(); sync(); }
      if (event.key === 'Tab' && !ctrl) { event.preventDefault(); sync(doc.model.replace(area.selectionStart, area.selectionEnd, '\t')); }
    });
    const foot = node('div', 'text-editor-footer'); ui.status = node('span'); ui.status.setAttribute('role', 'status'); ui.save = button('保存到此服务器', 'primary-button', () => save(doc));
    foot.append(ui.status, ui.save);
    ui.panel.append(owner, toolbar, area, foot, node('p', 'text-editor-note', '编码、BOM 与原有换行保持原样。Ctrl+S 保存此文件；Ctrl+Tab 切换文件；Ctrl+W 关闭当前文件。'));
    doc.win.body.append(ui.panel);
  }
  async function openText(state, path) {
    const key = keyFor(state, path), existing = documents.get(key);
    if (existing) { show(existing.win, existing); return; }
    if (!state?.connected || state.closed || sessions.get(state.id) !== state) throw new Error('此连接已断开，请重新连接后打开文件');
    if (documents.size >= 40) throw new Error('最多同时打开 40 个文件，请先关闭部分标签');
    const profile = profileFor(state), win = lastWindow || makeWindow();
    const doc = new DengRemoteTextDocument(state, path, replace => update(doc, replace));
    doc.key = key; doc.owner = profile?.name || '服务器'; doc.address = `${profile?.user || ''}@${profile?.host || ''}:${profile?.port || 22}`;
    doc.win = win; win.docs.push(doc); documents.set(key, doc); makePanel(doc); show(win, doc); reflect();
    try { await doc.load(); if (!doc.closed && doc.win.active === doc && doc.win.element.open) doc.ui.area.focus(); }
    catch (error) { toast(error.message); }
  }
  function init() {
    dock = button('文本编辑器', 'upload-button', () => { for (const win of [...windows]) show(win); }); dock.id = 'show-text-editors'; dock.hidden = true; $('#choose-files').after(dock);
    window.addEventListener('resize', () => windows.forEach(constrain));
    window.addEventListener('beforeunload', event => { if (isDirty()) { event.preventDefault(); event.returnValue = ''; } });
  }
  document.addEventListener('DOMContentLoaded', init, { once: true });
  return { openText, reflect, hasUnsaved: isDirty, unsavedCount: () => [...documents.values()].filter(d => d.dirty).length, savingCount: () => [...documents.values()].filter(d => d.saving).length };
})();
