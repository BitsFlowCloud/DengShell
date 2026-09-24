'use strict';
window.DengPathHistory = (() => {
  let dialog, owner, entries = [], request = 0, busy = false, saved = true;
  const valid = state => state && state === current() && state.connected && state.sftpAvailable !== false && !state.closed && sessions.get(state.id) === state;
  const endpointFor = state => `/api/sessions/${encodeURIComponent(state.id)}/path-history`;
  function reflect() {
    const button = $('#path-history-button'); if (button) button.disabled = !valid(current());
    if (dialog?.open && !valid(owner)) dialog.close();
  }
  function highlight(container, value, query) {
    const lower = value.toLocaleLowerCase(), needle = query.toLocaleLowerCase();
    let start = 0, at;
    if (!needle) { container.textContent = value; return; }
    while ((at = lower.indexOf(needle, start)) >= 0) {
      container.append(document.createTextNode(value.slice(start, at)), node('mark', '', value.slice(at, at + needle.length)));
      start = at + needle.length;
    }
    container.append(document.createTextNode(value.slice(start)));
  }
  function render() {
    const query = $('#path-history-search').value.trim(), items = entries.filter(e => e.path.toLocaleLowerCase().includes(query.toLocaleLowerCase()));
    const list = $('#path-history-list'); list.replaceChildren();
    for (const item of items) {
      const row = node('button', 'path-history-row'); row.type = 'button'; row.disabled = busy; row.dataset.path = item.path;
      const text = node('span', 'path-history-text'), path = node('span', 'path-history-path'); highlight(path, item.path, query);
      text.append(path, node('small', '', `${item.kind === 'folder' ? '目录' : '文件'} · ${new Date(item.visitedAt).toLocaleString('zh-CN', { hour12: false })}`));
      row.append(icon(item.kind === 'folder' ? 'folder' : 'file'), text); row.title = `${item.kind === 'folder' ? '打开目录' : '打开文本文件'}：${item.path}`;
      row.onclick = () => jump(item); list.append(row);
    }
    $('#path-history-empty').hidden = items.length > 0;
    $('#path-history-empty').textContent = busy ? '正在读取…' : !saved ? '请先保存此连接，再记录访问过的文件或目录。' : query ? '没有匹配的历史路径' : '暂无历史，成功打开文件或目录后会自动记录。';
    $('#path-history-count').textContent = query ? `${items.length} / ${entries.length} 条` : `${entries.length} 条 · 最近 200 条`;
    $('#clear-path-history').disabled = busy || !entries.length;
  }
  async function open(query = '') {
    const state = current(); if (!dialog || !valid(state)) return;
    owner = state; entries = []; busy = true; saved = true; const token = ++request;
    const profile = profileFor(state);
    $('#path-history-target').textContent = `${profile?.name || '当前服务器'} · ${profile?.user || ''}@${profile?.host || ''}:${profile?.port || ''}`;
    $('#path-history-search').value = query; $('#path-history-error').textContent = ''; render();
    if (!dialog.open) dialog.showModal(); $('#path-history-search').focus();
    try {
      const data = await api(endpointFor(state));
      if (token !== request || !dialog.open || !valid(state)) return;
      entries = data.entries || [];
      saved = data.saved !== false;
    } catch (error) { if (token === request && dialog.open) $('#path-history-error').textContent = error.message; }
    finally { if (token === request) { busy = false; render(); } }
  }
  async function jump(item) {
    const state = owner; if (busy || !valid(state)) { reflect(); return; }
    busy = true; const token = request; $('#path-history-error').textContent = ''; render();
    try {
      if (item.kind === 'folder') {
        await navigate(item.path, state);
        if (token === request && valid(state)) dialog.close();
      } else {
        // File editors retain their owning SSH session; they never use whichever
        // terminal becomes current while the file is being loaded.
        dialog.close(); await DengFileTools.openText(state, item.path);
      }
    } catch (error) { if (token === request && dialog.open) $('#path-history-error').textContent = error.message; else toast(error.message); }
    finally { if (token === request) { busy = false; render(); } }
  }
  document.addEventListener('DOMContentLoaded', () => {
    dialog = node('dialog'); dialog.id = 'path-history-dialog'; dialog.setAttribute('aria-labelledby', 'path-history-title');
    dialog.innerHTML = '<div class="dialog-heading"><h2 id="path-history-title">历史路径</h2><button type="button" class="icon-button" id="close-path-history" aria-label="关闭历史路径"><svg><use href="#i-close"/></svg></button></div><p id="path-history-target"></p><label class="path-history-search-field"><svg><use href="#i-search"/></svg><input id="path-history-search" type="search" placeholder="搜索访问过的文件或目录，例如 xray" aria-label="搜索历史路径" autocomplete="off" spellcheck="false"></label><p id="path-history-error" role="alert"></p><div id="path-history-list" aria-label="匹配的历史路径"></div><p id="path-history-empty" role="status"></p><div class="path-history-footer"><span id="path-history-count"></span><button type="button" class="text-button" id="clear-path-history">清空此服务器历史</button></div>';
    document.body.append(dialog);
    $('#path-history-button').onclick = () => open();
    $('#close-path-history').onclick = () => dialog.close();
    $('#path-history-search').oninput = render;
    dialog.addEventListener('keydown', event => {
      if (event.isComposing || busy) return;
      const rows = [...dialog.querySelectorAll('.path-history-row')], index = rows.indexOf(document.activeElement);
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
        event.preventDefault();
        if (event.key === 'ArrowUp' && index <= 0) $('#path-history-search').focus();
        else rows[Math.min(rows.length - 1, Math.max(0, index + (event.key === 'ArrowDown' ? 1 : -1)))]?.focus();
      } else if (event.key === 'Enter' && document.activeElement === $('#path-history-search')) { event.preventDefault(); rows[0]?.click(); }
    });
    $('#clear-path-history').onclick = safe(async () => {
      const state = owner, token = request; if (busy || !valid(state)) return;
      const confirmed = await ask({ title: '清空此服务器的历史路径？', description: '仅清除本机记录，服务器上的文件和目录不受影响。', confirm: '清空历史' });
      if (!confirmed || token !== request || !dialog.open || !valid(state)) return;
      busy = true; $('#path-history-error').textContent = ''; render();
      try { await remove(endpointFor(state)); if (token === request) entries = []; }
      catch (error) { if (token === request) $('#path-history-error').textContent = error.message; }
      finally { if (token === request) { busy = false; render(); } }
    });
    dialog.addEventListener('close', () => { request++; owner = null; entries = []; busy = false; $('#path-history-list').replaceChildren(); $('#path-history-search').value = ''; $('#path-history-target').textContent = ''; $('#path-history-error').textContent = ''; });
    reflect();
  }, { once: true });
  return { open, reflect };
})();
