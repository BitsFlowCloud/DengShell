'use strict';
window.DengDirectoryFavorites = (() => {
  let panel, button, owner, entries = [], generation = 0, busy = false, saved = true, opened = false;
  const valid = state => state && state === current() && state.connected && !state.closed && sessions.get(state.id) === state;
  const endpoint = state => `/api/sessions/${encodeURIComponent(state.id)}/directory-favorites`;
  const active = (state, token) => opened && token === generation && valid(state);
  function close() {
    opened = false; generation++; owner = null; entries = []; busy = false;
    if (!panel) return;
    if (panel.matches(':popover-open')) panel.hidePopover();
    panel.hidden = true; button.setAttribute('aria-expanded', 'false');
    $('#directory-favorites-list').replaceChildren(); $('#directory-favorites-error').textContent = '';
    $('#directory-favorites-search').value = ''; $('#directory-favorites-target').textContent = '';
    $('#favorite-current-directory').title = '';
  }
  function position() {
    if (!opened) return;
    const scale = Number(document.body.style.zoom) || 1, r = button.getBoundingClientRect();
    const width = innerWidth / scale, height = innerHeight / scale;
    panel.style.width = Math.min(360, width - 16) + 'px';
    panel.style.maxHeight = Math.min(400, height - 16) + 'px';
    const h = panel.getBoundingClientRect().height / scale;
    const below = r.bottom / scale + 6;
    panel.style.left = Math.max(8, Math.min(r.left / scale, width - panel.offsetWidth - 8)) + 'px';
    panel.style.top = Math.max(8, Math.min(below + h <= height - 8 ? below : r.top / scale - h - 6, height - h - 8)) + 'px';
  }
  function reflect() {
    if (!button) return;
    button.disabled = !valid(current());
    if (opened && (!valid(owner) || $('#files-view').hidden)) close();
    else if (opened) render();
  }
  function render() {
    if (!opened) return;
    const query = $('#directory-favorites-search').value.trim().toLocaleLowerCase();
    const list = $('#directory-favorites-list'); list.replaceChildren();
    for (const path of entries.filter(path => path.toLocaleLowerCase().includes(query))) {
      const row = node('div', 'directory-favorite-row'); row.dataset.path = path;
      const jump = node('button', 'directory-favorite-path'), text = node('span', '', path);
      jump.type = 'button'; jump.title = path; jump.disabled = busy;
      jump.append(icon('folder'), text); jump.onclick = () => navigateFavorite(path);
      const remove = node('button', 'icon-button directory-favorite-remove'); remove.type = 'button'; remove.disabled = busy;
      remove.title = '移除收藏'; remove.setAttribute('aria-label', `移除收藏：${path}`); remove.append(icon('close'));
      remove.onclick = () => change('DELETE', path); row.append(jump, remove); list.append(row);
    }
    const add = $('#favorite-current-directory'), path = owner?.cwd || owner?.home;
    add.disabled = busy || !saved || !path || entries.includes(path) || entries.length >= 100;
    add.textContent = entries.includes(path) ? '当前目录已收藏' : '收藏当前目录'; add.title = path || '';
    $('#directory-favorites-empty').hidden = list.children.length > 0;
    $('#directory-favorites-empty').textContent = busy ? '正在读取…' : !saved ? '请先保存此连接，再收藏常用目录。' : query ? '没有匹配的收藏' : '还没有收藏，进入常用目录后点击上方按钮。';
    $('#directory-favorites-count').textContent = `${entries.length} / 100`;
    position();
  }
  async function open() {
    const state = current(); if (!valid(state)) return;
    if (opened) { close(); return; }
    owner = state; entries = []; busy = true; saved = true; opened = true; const token = ++generation;
    $('#directory-favorites-target').textContent = profileFor(state)?.name || '当前服务器';
    panel.hidden = false; panel.showPopover?.(); button.setAttribute('aria-expanded', 'true'); render();
    $('#directory-favorites-search').focus();
    try {
      const data = await api(endpoint(state));
      if (!active(state, token)) return;
      entries = data.paths || []; saved = data.saved !== false;
    } catch (error) { if (active(state, token)) $('#directory-favorites-error').textContent = error.message; }
    finally { if (active(state, token)) { busy = false; render(); } }
  }
  async function change(method, path) {
    const state = owner, token = generation; if (busy || !active(state, token)) return;
    busy = true; $('#directory-favorites-error').textContent = ''; render();
    try {
      const data = await api(endpoint(state), { method, body: JSON.stringify({ path }) });
      if (!active(state, token)) return;
      entries = data.paths || []; saved = data.saved !== false;
    } catch (error) { if (active(state, token)) $('#directory-favorites-error').textContent = error.message; }
    finally { if (active(state, token)) { busy = false; render(); } }
  }
  async function navigateFavorite(path) {
    const state = owner, token = generation; if (busy || !active(state, token)) return;
    busy = true; $('#directory-favorites-error').textContent = ''; render();
    try { await navigate(path, state); if (active(state, token)) close(); }
    catch (error) { if (active(state, token)) $('#directory-favorites-error').textContent = error.message; }
    finally { if (active(state, token)) { busy = false; render(); } }
  }
  document.addEventListener('DOMContentLoaded', () => {
    button = $('#directory-favorites-button'); panel = node('div'); panel.id = 'directory-favorites'; panel.hidden = true;
    panel.setAttribute('role', 'dialog'); panel.setAttribute('aria-labelledby', 'directory-favorites-title');
    if (panel.showPopover) panel.setAttribute('popover', 'auto');
    panel.innerHTML = '<div class="directory-favorites-heading"><strong id="directory-favorites-title">常用目录</strong><span id="directory-favorites-count"></span></div><div id="directory-favorites-target"></div><button type="button" id="favorite-current-directory">收藏当前目录</button><input id="directory-favorites-search" type="search" placeholder="搜索收藏路径" aria-label="搜索常用目录" autocomplete="off" spellcheck="false"><p id="directory-favorites-error" role="alert"></p><div id="directory-favorites-list"></div><p id="directory-favorites-empty" role="status"></p>';
    document.body.append(panel); button.onclick = open;
    $('#favorite-current-directory').onclick = () => change('POST', owner?.cwd || owner?.home);
    $('#directory-favorites-search').oninput = render;
    panel.addEventListener('toggle', () => { if (opened && !panel.matches(':popover-open')) close(); });
    panel.addEventListener('keydown', event => {
      if (event.key === 'Escape') { event.preventDefault(); close(); button.focus(); }
      if (event.key === 'Enter' && event.target === $('#directory-favorites-search')) { event.preventDefault(); panel.querySelector('.directory-favorite-path')?.click(); }
    });
    document.addEventListener('pointerdown', event => { if (opened && !panel.contains(event.target) && !button.contains(event.target)) close(); });
    window.addEventListener('resize', position); window.addEventListener('dengshell:locked', close);
    document.querySelectorAll('.file-tab').forEach(tab => tab.addEventListener('click', close));
    reflect();
  }, { once: true });
  return { reflect, close };
})();
