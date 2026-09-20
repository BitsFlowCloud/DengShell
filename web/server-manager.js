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
  return !query || [profile.name, profile.host, profile.user, profile.notes, serverGroupPath(profile, tree)].some(value => String(value || '').toLocaleLowerCase().includes(query));
}
function serverTimestamp(value) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString('zh-CN', { hour12: false });
}
function persistGroupCollapse() { save('dengshell.server-groups.collapsed', [...serverManager.collapsed]); }
function toggleServerGroup(id, force) {
  const collapsed = force ?? !serverManager.collapsed.has(id);
  if (collapsed) serverManager.collapsed.add(id); else serverManager.collapsed.delete(id);
  persistGroupCollapse(); renderConnections();
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
  reflectServerExplorerDetails();
  const bar = $('#server-selection-bar'); if (!bar) return;
  bar.hidden = serverManager.tab === 'trash';
  const open = $('#open-selected-servers'), clear = $('#clear-selected-servers'), hint = $('#server-selection-hint');
  open.textContent = batch ? `正在打开 ${batch.completed}/${batch.total}` : `打开所选 (${selected.size})`;
  open.disabled = !!batch || !selected.size; open.setAttribute('aria-busy', String(!!batch));
  clear.hidden = !selected.size;
  hint.textContent = selected.size ? `已选 ${selected.size} 台 · Enter 打开` : '单击选择 · 双击打开 · Ctrl / ⌘ 多选';
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
  const button = node('button', 'connection-card');
  button.type = 'button'; button.disabled = deleted || connecting.has(profile.id);
  button.setAttribute('aria-label', `${deleted ? '已删除' : '选择'} ${profile.name}`);
  button.dataset.selectable = String(!deleted);
  if (!deleted) { button.setAttribute('aria-pressed', String(serverManager.selectedProfiles.has(profile.id))); button.setAttribute('aria-describedby', 'server-selection-hint'); }
  const glyph = node('span', 'server-icon'); glyph.append(icon('server'));
  const text = node('span', 'connection-card-text'); text.append(node('strong', '', profile.name), node('small', '', connecting.has(profile.id) ? '正在连接…' : `${profile.user}@${profile.host}:${profile.port}${profile.auth === 'key' && !profile.keyId && !profile.keyPath ? ' · 待配置私钥' : ''}`));
  if (profile.notes?.trim()) {
    const note = node('span', 'server-card-notes');
    note.append(node('span', 'server-card-notes-label', '备注'), node('span', 'server-card-notes-text', window.DengProfileNotes.preview(profile.notes)));
    note.title = profile.notes; text.append(note);
  }
  button.append(glyph, text);
  if (!deleted) {
    const marker = node('span', 'server-selection-mark', '✓'); marker.setAttribute('aria-hidden','true'); marker.hidden = !serverManager.selectedProfiles.has(profile.id); button.append(marker);
    button.append(node('span', `status-dot ${[...sessions.values()].some(session => session.profileId === profile.id && session.connected) ? 'green' : 'blue'}`));
  }
  button.onclick = safe(event => {
    if (event.ctrlKey || event.metaKey) { event.preventDefault(); toggleServerSelection(profile); return; }
    serverManager.selectedProfiles.clear(); serverManager.selectedProfiles.add(profile.id); serverManager.focusedProfile = profile.id; reflectServerSelection();
  });
  button.ondblclick = safe(event => { if (!deleted && !event.ctrlKey && !event.metaKey) { clearServerSelection(); return connect(profile.id); } });
  button.title = `${profile.name}\n${profile.user}@${profile.host}:${profile.port}\n${serverGroupPath(profile, tree)}`;
  if (profile.notes) button.title += `\n备注：${profile.notes}`;
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
  const more = serverManagerButton('⋯', event => openServerProfileMenu(profile, actions, event.currentTarget), 'server-profile-more');
  more.setAttribute('aria-label', `管理连接 ${profile.name}`); more.setAttribute('aria-haspopup', 'menu');
  row.append(more); row.oncontextmenu = event => { event.preventDefault(); openServerProfileMenu(profile, actions, row); };
  return row;
}

function renderServerManager() {
  initializeServerManager();
  const target = $('#connection-groups'), scroll = target.scrollTop;
  const focused = $('#server-manager-body').contains(document.activeElement) ? document.activeElement : null;
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
  renderServerExplorer(tree, query);
  if (serverManager.tab === 'servers') {
    for (const profile of serverExplorerProfiles(tree, query)) fragment.append(renderServerProfile(profile, tree));
    if (!fragment.childNodes.length) fragment.append(node('p', 'server-manager-empty', query ? '没有匹配的连接；可搜索名称、地址或分组。' : '此分组暂无连接，可新建连接或选择包含子分组。'));
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
  if (focusGroup !== undefined && focusAction) [...$('#server-manager-body').querySelectorAll('[data-group-id]')].find(row => row.dataset.groupId === focusGroup)?.querySelector(`[data-group-action="${focusAction}"]`)?.focus({ preventScroll: true });
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
  form.elements.backgroundColor.value = group?.backgroundColor || '';
  $('#group-background-color').value = group?.backgroundColor || '#eaf1f7';
  updateGroupColorPreview();
  $('#server-group-error').hidden = true; $('#server-group-error').textContent = '';
  renderServerGroupChoices(group?.parentId || parentID, $('#group-parent'), group?.id || '');
  $('#server-group-editor-title').textContent = group ? '编辑分组' : parentID ? '新建子分组' : '新建分组';
  $('#group-emoji-search').value = ''; $('#group-emoji-picker').open = !group;
  renderGroupEmojiPicker(); updateGroupEmojiPreview(); $('#server-group-dialog').showModal(); form.elements.name.focus();
}
function closeServerGroupMenu() { const menu = $('#server-group-menu'); if (menu) menu.hidden = true; serverManager.menuGroup = ''; serverManager.menuProfile = ''; }
async function deleteServerGroupTree(id) {
  if (serverManager.deletingGroup) return;
  serverManager.deletingGroup = id;
  closeServerGroupMenu();
  try {
    const plan = await api(`/api/group-nodes/${encodeURIComponent(id)}/deletion`);
    const scope = `「${plan.name}」及其全部子分组：共 ${plan.groups} 个分组、${plan.servers} 个连接、${plan.trashed} 个回收站连接。`;
    if (!await ask({title: '重要操作 · 第 1 / 3 次确认', description: scope + '\n将永久删除这些本地 SSH 连接配置及保存的登录凭据，无法从回收站恢复。', confirm: '已核对范围，继续'})) return;
    if (!await ask({title: '重要操作 · 第 2 / 3 次确认', description: scope + '\n对应 SSH 会话将断开，连接记录及每台服务器的终端历史将清理。请先保存远程文件，未保存的编辑器草稿会保留。\n全局历史列表、密钥管理器、代理和远程服务器文件保留。', confirm: '已了解影响，继续'})) return;
    const name = await ask({title: '永久删除 · 第 3 / 3 次确认', description: `最后确认：${scope}\n请输入分组名称「${plan.name}」以永久删除。`, input: true, confirm: '永久删除全部上述连接'});
    if (name === null) return;
    if (name !== plan.name) { toast('分组名称不匹配，未删除任何数据'); return; }
    const result = await post(`/api/group-nodes/${encodeURIComponent(id)}/deletion`, { revision: plan.revision, name, confirmations: 3 });
    const removed = new Set(result.profileIds);
    for (const profileID of removed) credentials.delete(profileID);
    for (const state of [...sessions.values()]) if (removed.has(state.profileId)) dropSessionView(state.id);
    await loadProfiles();
    toast(`已删除 ${plan.groups} 个分组及 ${removed.size} 个连接配置`);
  } finally { serverManager.deletingGroup = ''; }
}
function updateGroupColorPreview() {
  const color = $('#server-group-form').elements.backgroundColor.value;
  const preview = $('#group-color-preview');
  preview.textContent = color || '跟随主题';
  preview.style.backgroundColor = color || '';
  preview.style.color = color ? serverGroupColorText(color) : '';
}
function openServerGroupMenu(id, anchor) {
  if (serverManager.menuGroup === id && !$('#server-group-menu').hidden) { closeServerGroupMenu(); return; }
  const tree = serverGroupTree(), group = tree.byID.get(id); if (!group) return;
  serverManager.menuGroup = id; serverManager.menuProfile = '';
  const menu = $('#server-group-menu');
  const erase = serverManagerButton('删除分组及全部连接', () => deleteServerGroupTree(id), 'server-danger');
  erase.title = '永久删除此分组、子分组及其连接，需要三次确认';
  menu.replaceChildren(
    serverManagerButton('添加服务器', () => { closeServerGroupMenu(); showConnectionForm(null, id); }),
    serverManagerButton('新建子分组', () => openServerGroupEditor('', id)),
    serverManagerButton('名称、背景色与层级', () => openServerGroupEditor(id)), erase,
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
  initializeServerExplorer();
  const bar = node('div', 'server-selection-bar'); bar.id = 'server-selection-bar';
  const hint = node('span', 'server-selection-hint'); hint.id = 'server-selection-hint'; hint.setAttribute('aria-live','polite');
  const clear = serverManagerButton('取消选择', clearServerSelection, 'server-selection-clear'); clear.id = 'clear-selected-servers';
  const open = serverManagerButton('打开所选 (0)', openSelectedServers, 'server-selection-open'); open.id = 'open-selected-servers'; open.disabled = true;
  open.setAttribute('aria-describedby', hint.id);
  bar.title = '单击选择 · 双击打开 · Ctrl / ⌘ 多选；Enter 打开所选，Esc 取消选择';
  bar.append(hint,clear,open); $('#server-manager-tools').append(bar);
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
  $('#group-background-color').oninput = event => { $('#server-group-form').elements.backgroundColor.value = event.target.value; updateGroupColorPreview(); };
  $('#clear-group-color').onclick = () => { $('#server-group-form').elements.backgroundColor.value = ''; updateGroupColorPreview(); };
  $('#server-group-form').onsubmit = safe(async event => {
    event.preventDefault(); const form = event.currentTarget, values = Object.fromEntries(new FormData(form));
    const button = $('#save-server-group'); button.disabled = true; $('#server-group-error').hidden = true;
    try {
      const group = await post('/api/group-nodes', values); serverManager.selectedGroup = group.id;
      if (values.parentId) serverManager.collapsed.delete(values.parentId);
      persistGroupCollapse(); saveServerExplorer(); await loadProfiles();
      if ($('#connection-dialog').open) renderServerGroupChoices(group.id);
      $('#server-group-dialog').close();
    } catch (error) { $('#server-group-error').textContent = error.message || String(error); $('#server-group-error').hidden = false; }
    finally { button.disabled = false; }
  });
  document.addEventListener('pointerdown', event => { if (!event.target.closest('#server-group-menu,.server-group-more,.server-profile-more')) closeServerGroupMenu(); });
  $('#connection-groups').addEventListener('scroll', closeServerGroupMenu, { passive: true });
  window.addEventListener('resize', closeServerGroupMenu);
  $('#server-group-menu').onkeydown = event => {
    const buttons = [...event.currentTarget.querySelectorAll('button:not(:disabled)')], index = buttons.indexOf(document.activeElement);
    if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); const id = serverManager.menuGroup, profile = serverManager.menuProfile; closeServerGroupMenu(); const row = [...$('#server-manager-body').querySelectorAll(profile ? '[data-profile-id]' : '[data-group-id]')].find(row => profile ? row.dataset.profileId === profile : row.dataset.groupId === id); row?.querySelector(profile ? '.server-profile-more' : '.server-group-more')?.focus(); }
    else if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) { event.preventDefault(); buttons[event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : buttons.length - 1)) % buttons.length]?.focus(); }
  };
  fetch('assets/group-emoji/catalog.json').then(response => { if (!response.ok) throw new Error('图标目录不可用'); return response.json(); }).then(catalog => {
    serverManager.emoji = catalog.items || []; serverManager.emojiMap = new Map(serverManager.emoji.map(item => [item.emoji, item]));
    renderConnections(); renderGroupEmojiPicker();
  }).catch(error => { $('#group-emoji-options').textContent = '预设图标暂不可用，仍可直接输入表情符号'; console.warn(error.message); });
}
