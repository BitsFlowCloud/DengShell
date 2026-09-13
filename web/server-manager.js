/* Hierarchical server groups, connection history and recoverable local deletion. */
'use strict';

const serverManager = {
  nodes: [], trash: [], history: [], tab: 'servers', selectedGroup: '', collapsed: null,
  initialized: false, generation: 0, emoji: [], emojiMap: new Map(), menuGroup: '',
  selectedProfiles: new Set(), batch: null,
};
const serverNameCollator = new Intl.Collator('zh-Hans-CN-u-co-pinyin', { numeric: true, sensitivity: 'base' });
const serverNameSegmenter = typeof Intl.Segmenter === 'function' ? new Intl.Segmenter('zh', { granularity: 'grapheme' }) : null;

function serverNameSortKey(value) {
  const text = String(value || '').normalize('NFKC');
  const segments = serverNameSegmenter ? [...serverNameSegmenter.segment(text)].map(item => item.segment) : Array.from(text);
  while (segments.length && (/\p{Extended_Pictographic}|\p{Regional_Indicator}|\u20e3/u.test(segments[0]) || !/[\p{L}\p{N}]/u.test(segments[0]))) segments.shift();
  const key = segments.join('');
  const category = /^\p{Decimal_Number}/u.test(key) ? 0 : /^\p{Script=Latin}/u.test(key) ? 1 : /^\p{Script=Han}/u.test(key) ? 2 : 3;
  return { key, category };
}
function compareServerNames(a, b) {
  const left = serverNameSortKey(typeof a === 'string' ? a : a.name), right = serverNameSortKey(typeof b === 'string' ? b : b.name);
  return left.category - right.category || serverNameCollator.compare(left.key, right.key);
}
function initializeSavedGroupCollapse() {
  if (serverManager.collapsed) return;
  const saved = readSaved('dengshell.server-groups.collapsed', []);
  serverManager.collapsed = new Set(Array.isArray(saved) ? saved : []);
}
function acceptServerManagerConfig(config) {
  serverManager.generation++;
  serverManager.nodes = config.groupNodes || [];
  serverManager.trash = config.trash || [];
  serverManager.history = config.connectionHistory || [];
  const activeProfiles = new Set((config.servers || []).filter(profile => !profile.deletedAt).map(profile => profile.id));
  for (const id of serverManager.selectedProfiles) if (!activeProfiles.has(id)) serverManager.selectedProfiles.delete(id);
  initializeSavedGroupCollapse();
  const valid = new Set(serverManager.nodes.map(group => group.id));
  for (const id of serverManager.collapsed) if (!valid.has(id)) serverManager.collapsed.delete(id);
  if (!valid.has(serverManager.selectedGroup)) serverManager.selectedGroup = '';
}
async function refreshServerManagerHistory() {
  const generation = ++serverManager.generation, config = await api('/api/config');
  if (generation !== serverManager.generation) return;
  profiles = config.servers || [];
  acceptServerManagerConfig(config); renderConnections();
}

// Flat traversal keeps arbitrary-depth groups out of both the JS and DOM call stacks.
function serverGroupTree() {
  const byID = new Map(serverManager.nodes.map(group => [group.id, group]));
  const children = new Map([['', []]]), members = new Map(), trashCounts = new Map();
  for (const group of serverManager.nodes) {
    const parent = byID.has(group.parentId) ? group.parentId : '';
    if (!children.has(parent)) children.set(parent, []);
    children.get(parent).push(group);
  }
  for (const list of children.values()) list.sort(compareServerNames);
  for (const profile of profiles) {
    if (!members.has(profile.groupId)) members.set(profile.groupId, []);
    members.get(profile.groupId).push(profile);
  }
  for (const list of members.values()) list.sort(compareServerNames);
  for (const profile of serverManager.trash) trashCounts.set(profile.groupId, (trashCounts.get(profile.groupId) || 0) + 1);
  const flat = [], paths = new Map(), depths = new Map(), visited = new Set();
  const stack = [...(children.get('') || [])].reverse().map(group => ({ group, depth: 0, path: '' }));
  while (stack.length) {
    const { group, depth, path } = stack.pop();
    if (visited.has(group.id)) continue;
    visited.add(group.id);
    const fullPath = path ? `${path} / ${group.name}` : group.name;
    flat.push(group); paths.set(group.id, fullPath); depths.set(group.id, depth);
    const childList = children.get(group.id) || [];
    for (let i = childList.length - 1; i >= 0; i--) stack.push({ group: childList[i], depth: depth + 1, path: fullPath });
  }
  const counts = new Map(flat.map(group => [group.id, (members.get(group.id) || []).length]));
  for (let i = flat.length - 1; i >= 0; i--) {
    const group = flat[i];
    if (byID.has(group.parentId)) counts.set(group.parentId, (counts.get(group.parentId) || 0) + counts.get(group.id));
  }
  return { byID, children, members, trashCounts, flat, paths, depths, counts };
}
function serverGroupIcon(emoji) {
  const slot = node('span', 'server-group-icon'), item = serverManager.emojiMap.get(emoji);
  if (item) { const image = node('img'); image.src = item.file; image.alt = ''; image.width = 20; image.height = 20; slot.append(image); }
  else if (emoji) slot.textContent = emoji;
  else slot.append(icon('folder'));
  slot.setAttribute('aria-hidden', 'true'); return slot;
}
function serverManagerButton(text, action, className = '') {
  const button = node('button', className, text); button.type = 'button'; button.onclick = safe(action); return button;
}
function serverGroupPath(profile, tree) { return tree.paths.get(profile.groupId) || profile.group || '未分组'; }
function serverMatches(profile, query, tree) {
  return !query || [profile.name, profile.host, profile.user, serverGroupPath(profile, tree)].some(value => String(value || '').toLocaleLowerCase().includes(query));
}
function serverTimestamp(value) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString('zh-CN', { hour12: false });
}
function persistGroupCollapse() { save('dengshell.server-groups.collapsed', [...serverManager.collapsed]); }
function toggleServerGroup(id, force) {
  const collapsed = force ?? !serverManager.collapsed.has(id);
  if (collapsed) serverManager.collapsed.add(id); else serverManager.collapsed.delete(id);
  serverManager.selectedGroup = id; persistGroupCollapse(); renderConnections();
}
function setServerManagerTab(tab) {
  if (!['servers', 'history', 'trash'].includes(tab)) return;
  if (serverManager.tab !== tab) serverManager.selectedProfiles.clear();
  serverManager.tab = tab; closeServerGroupMenu(); $('#connection-search').value = ''; renderConnections();
  if (tab === 'history') safe(refreshServerManagerHistory)();
}

function reflectServerSelection() {
  const selected = serverManager.selectedProfiles, batch = serverManager.batch;
  for (const row of $('#connection-groups').querySelectorAll('[data-profile-id]')) {
    const button = row.querySelector('.connection-card'), chosen = selected.has(row.dataset.profileId) && button?.dataset.selectable === 'true';
    row.classList.toggle('server-multi-selected', chosen);
    if (button?.dataset.selectable === 'true') button.setAttribute('aria-pressed', String(chosen));
    const marker = row.querySelector('.server-selection-mark'); if (marker) marker.hidden = !chosen;
  }
  const bar = $('#server-selection-bar'); if (!bar) return;
  bar.hidden = serverManager.tab === 'trash';
  const open = $('#open-selected-servers'), clear = $('#clear-selected-servers'), hint = $('#server-selection-hint');
  open.textContent = batch ? `正在打开 ${batch.completed}/${batch.total}` : `打开所选 (${selected.size})`;
  open.disabled = !!batch || !selected.size; open.setAttribute('aria-busy', String(!!batch));
  clear.hidden = !selected.size;
  hint.textContent = selected.size ? `已选 ${selected.size} 台 · Enter 打开` : 'Ctrl / ⌘ + 单击多选';
}
function clearServerSelection() { serverManager.selectedProfiles.clear(); reflectServerSelection(); }
function toggleServerSelection(profile) {
  if (serverManager.tab === 'trash' || profile.deletedAt || !profiles.some(item => item.id === profile.id && !item.deletedAt)) return;
  if (serverManager.selectedProfiles.has(profile.id)) serverManager.selectedProfiles.delete(profile.id);
  else serverManager.selectedProfiles.add(profile.id);
  reflectServerSelection();
}
async function openSelectedServers() {
  if (serverManager.batch || serverManager.tab === 'trash') return;
  const ids = [...serverManager.selectedProfiles].filter(id => profiles.some(profile => profile.id === id && !profile.deletedAt));
  if (!ids.length) return clearServerSelection();
  const batch = { total:ids.length, completed:0, connected:0 }; serverManager.batch = batch; clearServerSelection();
  setDrawer(false);
  async function openOne(id) {
    try {
      if (!profiles.some(profile => profile.id === id && !profile.deletedAt)) return;
      const state = await connect(id, false, { background: true, refreshHistory: false });
      if (state?.connected && sessions.get(state.id) === state) batch.connected++;
    } catch (error) { toast(error.message || String(error)); }
    finally { batch.completed++; reflectServerSelection(); }
  }
  // SSH opens are independent: a slow host or a credential prompt never occupies
  // a worker slot needed by another host. File uploads retain their own limit.
  try { await Promise.allSettled(ids.map(openOne)); }
  finally { serverManager.batch = null; reflectServerSelection(); safe(refreshServerManagerHistory)(); toast(`SSH 连接完成：已连接 ${batch.connected} / ${batch.total} 台`); }
}

function renderServerProfile(profile, tree, mode = 'servers', history = null) {
  const row = node('div', 'server-profile-row'); row.dataset.profileId = profile.id;
  const deleted = mode === 'trash' || !!profile.deletedAt;
  const button = node('button', `connection-card${current()?.profileId === profile.id ? ' selected' : ''}`);
  button.type = 'button'; button.disabled = deleted || connecting.has(profile.id);
  button.setAttribute('aria-label', `${deleted ? '已删除' : '连接'} ${profile.name}`);
  button.dataset.selectable = String(!deleted);
  if (!deleted) { button.setAttribute('aria-pressed', String(serverManager.selectedProfiles.has(profile.id))); button.setAttribute('aria-describedby', 'server-selection-hint'); }
  const glyph = node('span', 'server-icon'); glyph.append(icon('server'));
  const text = node('span', 'connection-card-text'); text.append(node('strong', '', profile.name), node('small', '', connecting.has(profile.id) ? '正在连接…' : `${profile.user}@${profile.host}:${profile.port}`));
  button.append(glyph, text);
  if (!deleted) {
    const marker = node('span', 'server-selection-mark', '✓'); marker.setAttribute('aria-hidden','true'); marker.hidden = !serverManager.selectedProfiles.has(profile.id); button.append(marker);
    button.append(node('span', `status-dot ${[...sessions.values()].some(session => session.profileId === profile.id && session.connected) ? 'green' : 'blue'}`));
  }
  button.onclick = safe(event => {
    if (event.ctrlKey || event.metaKey) { event.preventDefault(); toggleServerSelection(profile); return; }
    clearServerSelection(); return connect(profile.id);
  });
  button.onkeydown = event => {
    if ((event.ctrlKey || event.metaKey) && event.key === ' ') { event.preventDefault(); event.stopPropagation(); toggleServerSelection(profile); }
  };
  row.append(button);
  const meta = node('div', 'server-profile-meta');
  if (mode !== 'servers') {
    const path = node('span', 'server-profile-path', serverGroupPath(profile, tree)); path.title = path.textContent; meta.append(path);
    meta.append(node('span', 'server-profile-date', mode === 'trash' ? `删除于 ${serverTimestamp(profile.deletedAt)}` : `${serverTimestamp(history.lastConnectedAt)} · ${history.count} 次连接${deleted ? ' · 已删除' : ''}`));
  }
  const actions = node('div', 'server-profile-actions');
  if (mode === 'trash') {
    actions.append(serverManagerButton('恢复', async () => { await post(`/api/trash/${encodeURIComponent(profile.id)}/restore`, {}); await loadProfiles(); toast(`已恢复「${profile.name}」`); }));
    actions.append(serverManagerButton('永久删除', async () => {
      if (!await ask({ title: `永久删除「${profile.name}」？`, description: '保存的连接配置、凭据和连接历史将被永久移除，无法恢复。远程服务器本身不受影响。', confirm: '永久删除' })) return;
      await remove(`/api/trash/${encodeURIComponent(profile.id)}`); credentials.delete(profile.id); save(`dengshell.history.${profile.id}`, undefined); save(`dengshell.nic.${profile.id}`, undefined); await loadProfiles(); toast('连接配置已永久删除');
    }, 'server-danger'));
  } else if (deleted) actions.append(serverManagerButton('查看已删除', () => setServerManagerTab('trash')));
  else {
    actions.append(serverManagerButton('编辑', () => showConnectionForm(profile)));
    if (mode === 'servers') actions.append(serverManagerButton('删除', async () => {
      const openSessions = [...sessions.values()].filter(session => session.profileId === profile.id);
      if (openSessions.length && !await ask({ title: `将「${profile.name}」移至已删除？`, description: '对应的会话将关闭，连接配置可在“已删除”中恢复。', confirm: '移至已删除' })) return;
      await remove(`/api/profiles/${encodeURIComponent(profile.id)}`);
      for (const session of openSessions) await closeSession(session.id);
      credentials.delete(profile.id); await loadProfiles(); toast(`「${profile.name}」已移至已删除，可随时恢复`);
    }));
  }
  if (meta.childNodes.length) row.append(meta);
  row.append(actions); return row;
}

function renderServerManager() {
  initializeServerManager();
  const target = $('#connection-groups'), scroll = target.scrollTop;
  const focused = target.contains(document.activeElement) ? document.activeElement : null;
  const focusGroup = focused?.closest('[data-group-id]')?.dataset.groupId, focusAction = focused?.dataset.groupAction;
  const tree = serverGroupTree(), query = $('#connection-search').value.trim().toLocaleLowerCase();
  const fragment = document.createDocumentFragment();
  const tabCounts = { servers: profiles.length, history: serverManager.history.length, trash: serverManager.trash.length };
  for (const tab of document.querySelectorAll('[data-server-tab]')) {
    const selected = tab.dataset.serverTab === serverManager.tab;
    tab.setAttribute('aria-selected', String(selected)); tab.tabIndex = selected ? 0 : -1;
    tab.querySelector('span').textContent = String(tabCounts[tab.dataset.serverTab]);
  }
  $('#server-manager-create').hidden = serverManager.tab !== 'servers';
  target.setAttribute('aria-labelledby', `server-tab-${serverManager.tab}`);
  if (serverManager.tab === 'servers') {
    const matches = new Set();
    if (query) {
      for (const group of tree.flat) if (tree.paths.get(group.id).toLocaleLowerCase().includes(query) || (tree.members.get(group.id) || []).some(profile => serverMatches(profile, query, tree))) {
        let ancestor = group;
        while (ancestor && !matches.has(ancestor.id)) { matches.add(ancestor.id); ancestor = tree.byID.get(ancestor.parentId); }
      }
    }
    let hiddenBelow = Infinity;
    for (const group of tree.flat) {
      const depth = tree.depths.get(group.id);
      if (depth <= hiddenBelow) hiddenBelow = Infinity;
      if (depth > hiddenBelow || query && !matches.has(group.id)) continue;
      const collapsed = !query && serverManager.collapsed.has(group.id);
      const heading = node('div', 'server-group-row'); heading.dataset.groupId = group.id;
      heading.style.setProperty('--group-indent', `${Math.min(depth, 6) * 9}px`);
      const toggle = node('button', 'server-group-toggle'); toggle.type = 'button'; toggle.dataset.groupAction = 'toggle';
      toggle.setAttribute('aria-expanded', String(!collapsed)); toggle.title = tree.paths.get(group.id);
      const caret = icon('chevron', 'server-group-caret');
      const label = node('span', 'server-group-label'); label.append(node('strong', '', group.name));
      if (depth > 0) { const path = node('small', '', tree.paths.get(group.parentId)); label.append(path); }
      toggle.append(caret, serverGroupIcon(group.emoji), label, node('span', 'server-group-count', String(tree.counts.get(group.id))));
      toggle.onclick = () => toggleServerGroup(group.id);
      toggle.onkeydown = event => {
        if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') { event.preventDefault(); toggleServerGroup(group.id, event.key === 'ArrowLeft'); }
      };
      const tools = serverManagerButton('⋯', event => openServerGroupMenu(group.id, event.currentTarget), 'server-group-more');
      tools.dataset.groupAction = 'menu'; tools.setAttribute('aria-label', `管理分组 ${group.name}`); tools.setAttribute('aria-haspopup', 'menu');
      heading.append(toggle, tools); fragment.append(heading);
      if (collapsed) { hiddenBelow = depth; continue; }
      const members = (tree.members.get(group.id) || []).filter(profile => serverMatches(profile, query, tree));
      for (const profile of members) { const row = renderServerProfile(profile, tree); row.style.setProperty('--group-indent', `${Math.min(depth, 6) * 9}px`); fragment.append(row); }
      if (!members.length && !query && !(tree.children.get(group.id) || []).length) {
        const empty = serverManagerButton('＋ 添加服务器', () => showConnectionForm(null, group.id), 'server-group-empty');
        empty.style.setProperty('--group-indent', `${Math.min(depth, 6) * 9}px`); fragment.append(empty);
      }
    }
    if (!fragment.childNodes.length) fragment.append(node('p', 'server-manager-empty', query ? '没有匹配的服务器或分组' : '创建分组，添加第一台服务器。'));
  } else {
    const allProfiles = new Map([...profiles, ...serverManager.trash].map(profile => [profile.id, profile]));
    if (serverManager.tab === 'trash') {
      for (const profile of [...serverManager.trash].sort((a, b) => new Date(b.deletedAt) - new Date(a.deletedAt))) if (serverMatches(profile, query, tree)) fragment.append(renderServerProfile(profile, tree, 'trash'));
    } else {
      for (const history of [...serverManager.history].sort((a, b) => new Date(b.lastConnectedAt) - new Date(a.lastConnectedAt))) {
        const profile = allProfiles.get(history.profileId);
        if (profile && serverMatches(profile, query, tree)) fragment.append(renderServerProfile(profile, tree, 'history', history));
      }
    }
    if (!fragment.childNodes.length) fragment.append(node('p', 'server-manager-empty', query ? '没有匹配的连接' : serverManager.tab === 'trash' ? '暂无已删除的连接' : '连接成功后会显示在这里。'));
  }
  target.replaceChildren(fragment); target.scrollTop = scroll; reflectServerSelection();
  if (focusGroup && focusAction) [...target.querySelectorAll('[data-group-id]')].find(row => row.dataset.groupId === focusGroup)?.querySelector(`[data-group-action="${focusAction}"]`)?.focus({ preventScroll: true });
}

function renderServerGroupChoices(selected = '', target = $('#profile-group'), exclude = '') {
  const tree = serverGroupTree(), excluded = new Set();
  if (exclude) for (const group of tree.flat) if (group.id === exclude || excluded.has(group.parentId)) excluded.add(group.id);
  const options = [];
  if (target.id === 'group-parent') { const top = node('option', '', '顶层分组'); top.value = ''; options.push(top); }
  for (const group of tree.flat) if (!excluded.has(group.id)) {
    const parent = tree.paths.get(group.parentId);
    const option = node('option', '', `${parent ? '↳ ' : ''}${group.emoji ? group.emoji + ' ' : ''}${group.name}${parent ? ' · ' + parent : ''}`);
    option.value = group.id; option.title = tree.paths.get(group.id); options.push(option);
  }
  target.replaceChildren(...options);
  target.value = options.some(option => option.value === selected) ? selected : options[0]?.value || '';
  window.DengSelect?.refresh(target);
}
function chooseConnectionGroup(profile, preferred = '') {
  renderServerGroupChoices(profile?.groupId || preferred || serverManager.selectedGroup);
}
function openServerGroupEditor(id = '', parentID = '') {
  closeServerGroupMenu();
  const group = serverManager.nodes.find(item => item.id === id), form = $('#server-group-form');
  form.reset(); form.elements.id.value = group?.id || ''; form.elements.name.value = group?.name || ''; form.elements.emoji.value = group?.emoji || '';
  $('#server-group-error').hidden = true; $('#server-group-error').textContent = '';
  renderServerGroupChoices(group?.parentId || parentID, $('#group-parent'), group?.id || '');
  $('#server-group-editor-title').textContent = group ? '编辑分组' : parentID ? '新建子分组' : '新建分组';
  $('#group-emoji-search').value = ''; $('#group-emoji-picker').open = !group;
  renderGroupEmojiPicker(); updateGroupEmojiPreview(); $('#server-group-dialog').showModal(); form.elements.name.focus();
}
function closeServerGroupMenu() { const menu = $('#server-group-menu'); if (menu) menu.hidden = true; serverManager.menuGroup = ''; }
function openServerGroupMenu(id, anchor) {
  if (serverManager.menuGroup === id && !$('#server-group-menu').hidden) { closeServerGroupMenu(); return; }
  const tree = serverGroupTree(), group = tree.byID.get(id); if (!group) return;
  serverManager.selectedGroup = id; serverManager.menuGroup = id;
  const menu = $('#server-group-menu');
  const erase = serverManagerButton('删除空分组', async () => {
    closeServerGroupMenu();
    if (!await ask({ title: `删除空分组「${group.name}」？`, confirm: '删除分组' })) return;
    await remove(`/api/group-nodes/${encodeURIComponent(id)}`); await loadProfiles();
  }, 'server-danger');
  erase.disabled = !!((tree.children.get(id) || []).length || (tree.members.get(id) || []).length || tree.trashCounts.get(id));
  erase.title = erase.disabled ? '请先移走子分组、服务器，以及“已删除”中属于此分组的连接' : '删除此空分组';
  menu.replaceChildren(
    serverManagerButton('添加服务器', () => { closeServerGroupMenu(); showConnectionForm(null, id); }),
    serverManagerButton('新建子分组', () => openServerGroupEditor('', id)),
    serverManagerButton('名称、图标与层级', () => openServerGroupEditor(id)), erase,
  );
  for (const button of menu.children) button.setAttribute('role', 'menuitem');
  menu.hidden = false;
  const scale = typeof effectiveScale === 'number' ? effectiveScale : 1, rect = anchor.getBoundingClientRect();
  menu.style.left = `${Math.max(8, Math.min(rect.right / scale - menu.offsetWidth, innerWidth / scale - menu.offsetWidth - 8))}px`;
  menu.style.top = `${Math.max(8, Math.min(rect.bottom / scale + 5, innerHeight / scale - menu.offsetHeight - 8))}px`;
  menu.querySelector('button').focus();
}
function updateGroupEmojiPreview() {
  const value = $('#server-group-form').elements.emoji.value.trim();
  $('#group-emoji-preview').replaceChildren(serverGroupIcon(value));
  for (const button of $('#group-emoji-options').children) button.setAttribute('aria-pressed', String(button.dataset.emoji === value));
}
function renderGroupEmojiPicker() {
  const query = $('#group-emoji-search').value.trim().toLocaleLowerCase();
  const items = serverManager.emoji.filter(item => !query || `${item.name} ${item.keywords} ${item.emoji}`.toLocaleLowerCase().includes(query));
  $('#group-emoji-options').replaceChildren(...items.map(item => {
    const button = serverManagerButton('', () => { $('#server-group-form').elements.emoji.value = item.emoji; updateGroupEmojiPreview(); }, 'group-emoji-option');
    button.dataset.emoji = item.emoji; button.title = item.name; button.setAttribute('aria-label', item.name); button.append(serverGroupIcon(item.emoji), node('small', '', item.name)); return button;
  }));
  if (!items.length) $('#group-emoji-options').append(node('span', 'group-emoji-empty', serverManager.emoji.length ? '没有匹配的图标，可直接输入表情符号' : '正在加载图标…'));
  updateGroupEmojiPreview();
}
function initializeServerManager() {
  if (serverManager.initialized) return;
  serverManager.initialized = true;
  initializeSavedGroupCollapse();
  const bar = node('div', 'server-selection-bar'); bar.id = 'server-selection-bar';
  const hint = node('span', 'server-selection-hint'); hint.id = 'server-selection-hint'; hint.setAttribute('aria-live','polite');
  const clear = serverManagerButton('取消选择', clearServerSelection, 'server-selection-clear'); clear.id = 'clear-selected-servers';
  const open = serverManagerButton('打开所选 (0)', openSelectedServers, 'server-selection-open'); open.id = 'open-selected-servers'; open.disabled = true;
  bar.append(hint,clear,open); $('#connection-groups').before(bar);
  $('#connections-drawer').addEventListener('keydown', event => {
    if (event.key === 'Escape' && serverManager.selectedProfiles.size) { event.preventDefault(); event.stopPropagation(); clearServerSelection(); }
    else if (event.key === 'Enter' && !event.repeat && serverManager.selectedProfiles.size && event.target.closest('.connection-card,#connection-search,#open-selected-servers')) { event.preventDefault(); event.stopPropagation(); safe(openSelectedServers)(); }
  });
  new MutationObserver(() => { if ($('#connections-drawer').hidden) clearServerSelection(); }).observe($('#connections-drawer'), {attributes:true,attributeFilter:['hidden']});
  for (const tab of document.querySelectorAll('[data-server-tab]')) {
    tab.onclick = () => setServerManagerTab(tab.dataset.serverTab);
    tab.onkeydown = event => {
      const tabs = [...document.querySelectorAll('[data-server-tab]')], index = tabs.indexOf(tab);
      const next = event.key === 'ArrowRight' ? (index + 1) % tabs.length : event.key === 'ArrowLeft' ? (index + tabs.length - 1) % tabs.length : event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : -1;
      if (next >= 0) { event.preventDefault(); tabs[next].click(); tabs[next].focus(); }
    };
  }
  $('#cancel-server-group').onclick = () => $('#server-group-dialog').close();
  $('#profile-new-group').onclick = () => openServerGroupEditor();
  $('#group-emoji-search').oninput = renderGroupEmojiPicker;
  $('#server-group-form').elements.emoji.oninput = updateGroupEmojiPreview;
  $('#clear-group-emoji').onclick = () => { $('#server-group-form').elements.emoji.value = ''; updateGroupEmojiPreview(); };
  $('#server-group-form').onsubmit = safe(async event => {
    event.preventDefault(); const form = event.currentTarget, values = Object.fromEntries(new FormData(form));
    const button = $('#save-server-group'); button.disabled = true; $('#server-group-error').hidden = true;
    try {
      const group = await post('/api/group-nodes', values); serverManager.selectedGroup = group.id;
      if (values.parentId) serverManager.collapsed.delete(values.parentId);
      persistGroupCollapse(); await loadProfiles();
      if ($('#connection-dialog').open) renderServerGroupChoices(group.id);
      $('#server-group-dialog').close();
    } catch (error) { $('#server-group-error').textContent = error.message || String(error); $('#server-group-error').hidden = false; }
    finally { button.disabled = false; }
  });
  document.addEventListener('pointerdown', event => { if (!event.target.closest('#server-group-menu,.server-group-more')) closeServerGroupMenu(); });
  $('#connection-groups').addEventListener('scroll', closeServerGroupMenu, { passive: true });
  window.addEventListener('resize', closeServerGroupMenu);
  $('#server-group-menu').onkeydown = event => {
    const buttons = [...event.currentTarget.querySelectorAll('button:not(:disabled)')], index = buttons.indexOf(document.activeElement);
    if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); const id = serverManager.menuGroup; closeServerGroupMenu(); [...$('#connection-groups').querySelectorAll('[data-group-id]')].find(row => row.dataset.groupId === id)?.querySelector('.server-group-more').focus(); }
    else if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) { event.preventDefault(); buttons[event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : buttons.length - 1)) % buttons.length]?.focus(); }
  };
  fetch('assets/group-emoji/catalog.json').then(response => { if (!response.ok) throw new Error('图标目录不可用'); return response.json(); }).then(catalog => {
    serverManager.emoji = catalog.items || []; serverManager.emojiMap = new Map(serverManager.emoji.map(item => [item.emoji, item]));
    renderConnections(); renderGroupEmojiPicker();
  }).catch(error => { $('#group-emoji-options').textContent = '预设图标暂不可用，仍可直接输入表情符号'; console.warn(error.message); });
}
