'use strict';
document.addEventListener('DOMContentLoaded', () => {
  const button = node('button'); button.id = 'import-from-finalshell'; button.type = 'button'; button.setAttribute('role', 'menuitem');
  button.append(icon('folder'), document.createTextNode('从 FinalShell 导入'), node('span', '', '连接、密码与密钥'));
  const menu = $('#settings-menu'); menu.insertBefore(button, menu.querySelector('.scale-setting'));
  const dialog = node('dialog'); dialog.id = 'finalshell-import-dialog'; dialog.setAttribute('aria-labelledby', 'finalshell-import-title');
  dialog.innerHTML = `<div class="dialog-heading"><div><h2 id="finalshell-import-title">从 FinalShell 导入</h2><p>自动读取指定文件夹中的 SSH 配置，保存到“FinalShell 导入”分组。</p></div><button type="button" class="icon-button" id="close-finalshell-import" aria-label="关闭 FinalShell 导入"><svg><use href="#i-close"/></svg></button></div>
    <ol class="finalshell-import-guide"><li>在 FinalShell 连接列表中选中 SSH 配置，右键 → 导出 → 当前选中或全部。</li><li>将导出的文件放进新建的 <code>finalshell_oot_pot</code> 文件夹，再把此文件夹放到 DengShell 程序旁。</li><li>将私钥放入其中的 <code>key</code> 子文件夹；文件名（可带 .pem 后缀）与连接 JSON 的 <code>secret_key_id</code> 一致，或使用包含 ID 和私钥内容的密钥 JSON。</li><li>点击设置中的“从 FinalShell 导入”，自动导入并关联密钥；重复扫描会补齐已有连接缺失的密钥关联。</li></ol>
    <div class="finalshell-directory"><span>本次扫描目录</span><code id="finalshell-directory"></code><button type="button" class="upload-button" id="copy-finalshell-directory">复制路径</button></div>
    <p class="finalshell-credential-note">密码在本机解析并存入加密配置。key 文件夹中的私钥自动保存到密钥管理器，同一私钥只保存一份。未匹配到密钥的连接会在使用时提示重新配置；加密私钥的口令可在密钥管理器中保存一次。导入不会连接服务器，也不会运行导出目录中的脚本。</p>
    <p id="finalshell-import-status" role="status" aria-live="polite"></p><div id="finalshell-import-list"></div>
    <div class="dialog-buttons"><button type="button" class="upload-button" id="show-finalshell-connections" hidden>查看导入的连接</button><button type="button" class="primary-button" id="retry-finalshell-import">重新扫描并导入</button></div>`;
  document.body.append(dialog);
  let busy = false, directory = '', result;
  function render(value) {
    result = value; directory = value.directory; $('#finalshell-directory').textContent = directory;
    const status = $('#finalshell-import-status'), list = $('#finalshell-import-list'); list.replaceChildren();
    if (!value.found) status.textContent = '未找到导入文件夹。请按上方步骤放好文件，然后点击“重新扫描并导入”。';
    else if (!value.items.length && !value.keyItems?.length) status.textContent = '文件夹中没有 *_connect_config.json 文件，请将 FinalShell 导出的 SSH 配置放入此目录。';
    else status.textContent = `连接：导入 ${value.imported} 个 · 更新 ${value.updated || 0} 个 · 跳过 ${value.skipped} 个 · 失败 ${value.failed} 个。密钥：导入 ${value.keysImported || 0} 个 · 复用 ${value.keysSkipped || 0} 个 · 失败 ${value.keysFailed || 0} 个；自动关联 ${value.keysAssociated || 0} 个连接。${value.needsKey ? ` ${value.needsKey} 个连接未匹配到私钥，连接时提示重新配置。` : ''}${value.needsPassword ? ` ${value.needsPassword} 个待补充密码。` : ''}`;
    $('#show-finalshell-connections').hidden = !value.items.some(item => item.profileId);
    for (const item of [...(value.keyItems || []), ...value.items]) {
      const row = node('article', 'finalshell-import-item'); row.dataset.status = item.status;
      const text = node('div'), title = node('strong', '', item.name || item.file), message = node('p', '', item.message), file = node('small', '', item.file);
      text.append(title, message, file); row.append(text);
      const label = node('span', 'finalshell-import-label', { imported: '已导入', updated: '已关联', skipped: '已存在', failed: '未导入' }[item.status] || item.status); row.append(label);
      if (item.profileId) {
        const edit = node('button', 'upload-button', item.needsPassword ? '补充密码' : '编辑连接');
        edit.onclick = safe(async () => {
          await loadProfiles(); const profile = profiles.find(p => p.id === item.profileId);
          if (!profile) throw new Error('此连接已删除，请重新扫描');
          dialog.close(); showConnectionForm(profile);
        }); row.append(edit);
      }
      list.append(row);
    }
  }
  async function scan() {
    if (busy) return;
    busy = true; $('#retry-finalshell-import').disabled = true; button.disabled = true;
    $('#finalshell-import-list').replaceChildren(); $('#show-finalshell-connections').hidden = true;
    $('#finalshell-import-status').textContent = '正在扫描并导入，请稍候…';
    try {
      const info = await api('/api/imports/finalshell'); directory = info.directory; $('#finalshell-directory').textContent = directory;
      const value = await post('/api/imports/finalshell', {}); render(value);
      // A refresh failure must not present a committed import as a failed import.
      try { await loadProfiles(); } catch { $('#finalshell-import-status').textContent += '；连接列表刷新失败，请关闭后重新打开连接列表。'; }
    } catch (error) { $('#finalshell-import-status').textContent = `导入未完成：${error.message || error}`; }
    finally { busy = false; $('#retry-finalshell-import').disabled = false; button.disabled = false; }
  }
  button.onclick = () => { setSettingsMenu(false); if (!dialog.open) dialog.showModal(); scan(); };
  $('#retry-finalshell-import').onclick = scan;
  $('#close-finalshell-import').onclick = () => dialog.close();
  $('#copy-finalshell-directory').onclick = safe(async () => { if (directory) { await copyText(directory); toast('导入目录已复制'); } });
  $('#show-finalshell-connections').onclick = safe(async () => {
    await loadProfiles(); dialog.close(); $('#connection-search').value = ''; setServerManagerTab('servers');
    const first = profiles.find(p => result?.items.some(item => item.profileId === p.id));
    serverManager.selectedGroup = '';
    if (first) { let group = first.groupId; while (group) { serverManager.collapsed?.delete(group); group = serverManager.nodes.find(g => g.id === group)?.parentId; } }
    setDrawer(true);
  });
}, { once: true });
