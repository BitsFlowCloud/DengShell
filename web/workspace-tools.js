'use strict';

let commands = [], commandGroups = [], managedKeys = [], commandGroup = '';
const commandPalette = [['blue', '雾蓝'], ['green', '松绿'], ['amber', '琥珀'], ['purple', '浅紫'], ['rose', '蔷薇'], ['teal', '青碧']];

function renderCommands() {
  const groups = [...new Set([...commandGroups, ...commands.map(command => command.group)])];
  if (!groups.includes(commandGroup)) commandGroup = '';
  $('#command-groups').replaceChildren(...[['', '全部'], ...groups.map(group => [group, group])].map(([value, label]) => {
    const button = node('button', value === commandGroup ? 'selected' : '', label);
    button.type = 'button'; button.title = label; button.setAttribute('aria-pressed', String(value === commandGroup));
    button.onclick = () => { commandGroup = value; renderCommands(); };
    return button;
  }));
  const state = current(), profile = profileFor(state);
  $('#command-target').textContent = state?.ready ? `执行到 ${profile?.name || '当前会话'}` : '连接后点击命令执行';
  const visible = commands.filter(command => !commandGroup || command.group === commandGroup);
  $('#commands-empty').hidden = visible.length > 0;
  $('#commands-empty').textContent = commandGroup ? `「${commandGroup}」还没有命令，点击「新建命令」添加。` : '把常用命令收在这里；右键可新建分组，点击「新建命令」开始。';
  $('#command-list').replaceChildren(...visible.map(command => {
    const pill = node('div', 'command-pill'); pill.dataset.color = command.color;
    const run = node('button', 'command-run', command.name); run.title = command.body; run.disabled = !state?.ready;
    run.onclick = () => {
      const active = current(); if (!active?.ready) return;
      // xterm handles multiline and bracketed paste according to the live shell.
      pasteTerminalText(active, command.body, { execute: true });
    };
    const edit = node('button', 'command-edit'); edit.append(icon('edit')); edit.title = `编辑 ${command.name}`; edit.setAttribute('aria-label', edit.title); edit.onclick = () => editCommand(command);
    pill.append(run, edit); return pill;
  }));
  renderCommandGroupChoices();
}

function renderCommandGroupChoices(preferred) {
  const select = $('#quick-command-group'), value = preferred ?? select.value;
  const groups = [...new Set([...commandGroups, ...commands.map(command => command.group)])];
  if (!groups.length) groups.push('常用命令');
  select.replaceChildren(...groups.map(group => { const option = node('option', '', group); option.value = group; return option; }));
  select.value = groups.includes(value) ? value : groups[0];
  window.DengSelect?.refresh(select);
}

async function createCommandGroup() {
  const name = await ask({ title: '新建命令分组', description: '分组会单独保存，暂时没有命令也会保留。', input: true, confirm: '创建分组' });
  if (name === null) return;
  const group = await post('/api/command-groups', { name });
  commandGroup = group.name;
  await loadProfiles();
  if ($('#command-dialog').open) renderCommandGroupChoices(group.name);
  toast(`已创建命令分组「${group.name}」`);
}

let commandContextMenu = null;
function closeCommandContextMenu(restoreFocus = false) {
  if (!commandContextMenu) return;
  commandContextMenu.remove(); commandContextMenu = null;
  if (restoreFocus) $('#commands-view .commands-content').focus({ preventScroll: true });
}

function showCommandContextMenu(event) {
  event.preventDefault(); event.stopPropagation(); closeCommandContextMenu();
  const menu = node('div', 'command-context-menu'); commandContextMenu = menu;
  menu.setAttribute('role', 'menu'); menu.setAttribute('aria-label', '快捷命令操作');
  for (const [label, glyph, action] of [['新建分组', 'folder', createCommandGroup], ['新建命令', 'plus', () => editCommand()]]) {
    const button = node('button', '', label); button.type = 'button'; button.setAttribute('role', 'menuitem'); button.prepend(icon(glyph));
    button.onclick = safe(async () => { closeCommandContextMenu(); await action(); }); menu.append(button);
  }
  document.body.append(menu);
  menu.style.width = '100px';
  const scale = menu.getBoundingClientRect().width / 100 || 1;
  menu.style.width = '';
  const rect = menu.getBoundingClientRect(), viewport = window.visualViewport;
  const left = viewport?.offsetLeft || 0, top = viewport?.offsetTop || 0;
  const right = left + (viewport?.width || innerWidth), bottom = top + (viewport?.height || innerHeight);
  menu.style.left = `${Math.max(left + 8, Math.min(event.clientX, right - rect.width - 8)) / scale}px`;
  menu.style.top = `${Math.max(top + 8, Math.min(event.clientY, bottom - rect.height - 8)) / scale}px`;
  menu.querySelector('button').focus({ preventScroll: true });
  menu.onkeydown = event => {
    if (event.key === 'Escape' || event.key === 'Tab') { event.preventDefault(); event.stopPropagation(); closeCommandContextMenu(true); }
    else if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
      event.preventDefault(); const items = [...menu.querySelectorAll('button')], i = items.indexOf(document.activeElement);
      items[event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : (i + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length].focus();
    }
  };
}

function editCommand(command = null) {
  const form = $('#quick-command-form'); form.reset();
  for (const key of ['id', 'name', 'body']) form.elements[key].value = command?.[key] ?? '';
  renderCommandGroupChoices(command?.group || commandGroup);
  form.elements.color.value = command?.color || commandPalette[commands.length % commandPalette.length][0];
  $('#command-dialog-title').textContent = command ? '编辑快捷命令' : '新建快捷命令';
  $('#delete-command').hidden = !command; $('#command-dialog').showModal(); form.elements.name.focus();
}

function renderKeyChoices() {
  const select = $('#profile-key'), selected = select.value;
  const file = node('option', '', '选择本机私钥文件'); file.value = '';
  select.replaceChildren(file, ...managedKeys.map(key => { const option = node('option', '', `${key.name}${key.encrypted ? ' · 已加密' : ''}`); option.value = key.id; return option; }));
  select.value = managedKeys.some(key => key.id === selected) ? selected : '';
}

function renderKeys() {
  $('#keys-empty').hidden = managedKeys.length > 0;
  $('#key-list').replaceChildren(...managedKeys.map(key => {
    const row = node('article', 'key-card'), detail = node('div', 'key-detail');
    detail.append(node('strong', '', key.name), node('small', '', `${key.publicKey.split(' ')[0]} · ${key.encrypted ? (key.hasPassphrase ? '已加密 · 口令已保存' : '已加密 · 口令未保存') : '无口令'}`), node('code', '', key.fingerprint));
    const tools = node('div', 'key-actions');
    const publicKey = node('button', '', '公钥'); publicKey.onclick = () => {
      $('#public-key-title').textContent = `${key.name} · 公钥`; $('#public-key-fingerprint').textContent = key.fingerprint; $('#public-key-content').value = key.publicKey; $('#public-key-dialog').showModal();
    };
    const rename = node('button', '', '编辑'); rename.onclick = () => editKey('edit', key);
    const del = node('button', 'danger-button', '删除'); del.onclick = safe(async () => {
      if (!await ask({ title: `删除密钥「${key.name}」？`, description: '删除密钥库中的本机副本。导入时的原文件不受影响。', confirm: '删除' })) return;
      await remove(`/api/keys/${key.id}`); await loadProfiles();
    });
    tools.append(publicKey, rename, del); row.append(icon('key'), detail, tools); return row;
  }));
}

let keyEditorMode = 'import';
function editKey(mode, key = null) {
  keyEditorMode = mode;
  const form = $('#key-editor-form'); form.reset(); form.elements.id.value = key?.id || ''; form.elements.name.value = key?.name || '';
  $('#key-editor-title').textContent = { import: '导入私钥', generate: '生成 Ed25519 密钥', edit: '编辑密钥' }[mode];
  $('#key-import-fields').hidden = mode !== 'import'; $('#key-passphrase-field').hidden = mode === 'edit' && !key?.encrypted;
  $('#key-passphrase-label').textContent = mode === 'generate' ? '加密口令（可选）' : '私钥口令（已加密时填写）';
  form.elements.passphrase.placeholder = key?.hasPassphrase ? '已保存，留空保留' : '';
  form.elements.passphrase.required = mode === 'edit' && !!key?.encrypted && !key.hasPassphrase;
  const sharedNote = '口令随密钥保存在本机加密配置中，所有引用此密钥的 SSH 连接自动使用。';
  $('#key-editor-note').textContent = mode === 'generate' ? `生成后可复制公钥，添加到服务器的 authorized_keys。填写口令可加密私钥。${sharedNote}` : mode === 'edit' ? (key?.encrypted ? `填写此私钥的正确口令即可保存；不会更改私钥文件的加密口令。${sharedNote}` : '此私钥没有加密，无需口令。') : `导入后保存一份本机副本，原文件不受影响。${sharedNote}`;
  $('#key-editor-note').hidden = false; $('#choose-library-key').hidden = !native();
  $('#key-editor-dialog').showModal(); form.elements.name.focus();
}

function updateProxyFields() {
  const form = $('#connection-form'), type = form.elements.proxyType.value, library = managedProxies.find(item => item.id === form.elements.proxyId.value), enabled = !library && type !== 'direct';
  $('#proxy-type-field').hidden = !!library;
  $('#proxy-fields').hidden = !enabled;
  form.elements.proxyHost.required = enabled; form.elements.proxyPort.required = enabled;
  $('#proxy-summary').textContent = library?.name || { direct: '直连', socks5: 'SOCKS5', http: 'HTTP CONNECT' }[type];
}

function initializeWorkspaceTools() {
  $('#command-colors').replaceChildren(...commandPalette.map(([color, name]) => {
    const label = node('label', 'color-option'); label.dataset.color = color; label.title = name;
    const radio = node('input'); radio.type = 'radio'; radio.name = 'color'; radio.value = color; radio.required = true;
    label.append(radio, node('span', '', name)); return label;
  }));
  $('#new-command').onclick = () => editCommand(); $('#cancel-command').onclick = () => $('#command-dialog').close();
  $('#new-command-group').onclick = safe(createCommandGroup);
  $('#commands-view').addEventListener('contextmenu', showCommandContextMenu);
  $('#commands-view').addEventListener('keydown', event => {
    if (event.key !== 'ContextMenu' && !(event.shiftKey && event.key === 'F10')) return;
    const rect = event.target.getBoundingClientRect();
    showCommandContextMenu({ clientX: rect.left + 12, clientY: rect.top + 20, preventDefault: () => event.preventDefault(), stopPropagation: () => event.stopPropagation() });
  });
  document.addEventListener('pointerdown', event => { if (commandContextMenu && !commandContextMenu.contains(event.target)) closeCommandContextMenu(); });
  window.addEventListener('resize', () => closeCommandContextMenu());
  $('#quick-command-form').onsubmit = safe(async event => {
    event.preventDefault(); const button = $('#save-command'); if (button.disabled) return; button.disabled = true;
    try { await post('/api/commands', Object.fromEntries(new FormData(event.currentTarget))); await loadProfiles(); $('#command-dialog').close(); } finally { button.disabled = false; }
  });
  $('#delete-command').onclick = safe(async () => {
    const form = $('#quick-command-form');
    if (!await ask({ title: `删除命令「${form.elements.name.value}」？`, confirm: '删除' })) return;
    await remove(`/api/commands/${form.elements.id.value}`); await loadProfiles(); $('#command-dialog').close();
  });
  for (const id of ['manage-keys', 'manage-profile-keys']) $(`#${id}`).onclick = () => { renderKeys(); $('#keys-dialog').showModal(); };
  $('#close-keys').onclick = () => $('#keys-dialog').close(); $('#import-key').onclick = () => editKey('import'); $('#generate-key').onclick = () => editKey('generate');
  $('#cancel-key-editor').onclick = () => $('#key-editor-dialog').close();
  $('#key-editor-dialog').addEventListener('close', () => $('#key-editor-form').reset());
  $('#choose-library-key').onclick = safe(async () => { const path = await native().ChooseKey(); if (path) $('#key-editor-form').elements.sourcePath.value = path; });
  $('#key-editor-form').onsubmit = safe(async event => {
    event.preventDefault(); const button = $('#save-key'); if (button.disabled) return; button.disabled = true;
    try {
      const saved = await post('/api/keys', { ...Object.fromEntries(new FormData(event.currentTarget)), generate: keyEditorMode === 'generate' });
      await loadProfiles(); $('#key-editor-dialog').close();
      if ($('#connection-dialog').open) { $('#profile-key').value = saved.id; $('#connection-form').elements.auth.value = 'key'; updateAuthFields(); }
      toast(keyEditorMode === 'generate' ? '密钥已生成，可查看并复制公钥' : '密钥已保存');
    } finally { button.disabled = false; }
  });
  $('#close-public-key').onclick = () => $('#public-key-dialog').close();
  $('#copy-public-key').onclick = safe(async () => {
    const text = $('#public-key-content').value;
    if (native()) await native().WriteClipboard(text); else await navigator.clipboard.writeText(text);
    toast('公钥已复制');
  });
  $('#profile-key').onchange = updateAuthFields; $('#connection-form').elements.proxyType.onchange = updateProxyFields;
  renderCommands(); renderKeyChoices(); renderKeys();
}
