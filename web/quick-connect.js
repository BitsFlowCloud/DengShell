'use strict';
window.DengQuickConnect = (() => {
  let dialog, form, output, saveDialog, savingID = '';
  function open(command = '') {
    setDrawer(false);
    form.elements.command.value = command; output.textContent = '';
    const select = form.elements.keyId;
    select.replaceChildren(new Option('选择已导入的私钥', ''), ...managedKeys.map(k => new Option(k.name, k.id)));
    form.elements.auth.value = 'password'; select.parentElement.hidden = true;
    if (!dialog.open) dialog.showModal(); form.elements.command.focus();
  }
  async function run(command, auth = 'password', keyId = '') {
    if (/^ping(?:\s|$)/.test(command.trim())) {
      if (!dialog.open) open(command);
      output.textContent = '正在从本机探测…';
      try { output.textContent = (await post('/api/quick-connect/ping', { host: command.trim().slice(4).trim() })).output; }
      catch (error) { output.textContent = error.message; }
      return;
    }
    const profile = await post('/api/quick-connect', { command, auth, keyId });
    temporaryProfiles.set(profile.id, profile); dialog.close();
    return connect(profile.id);
  }
  function reflect() {
    const state = current(), profile = profileFor(state), b = $('#save-quick-connection');
    if (b) b.hidden = !state?.connected || !profile?.temporary;
  }
  function saveCurrent() {
    const state = current(), profile = profileFor(state);
    if (!state?.connected || !profile?.temporary) return;
    savingID = profile.id;
    const f = saveDialog.querySelector('form'); f.elements.name.value = profile.name; f.elements.group.value = '快速连接';
    f.elements.remember.checked = false; f.elements.remember.closest('label').hidden = profile.auth === 'agent' || !credentials.has(savingID);
    $('#quick-save-target').textContent = `${profile.user}@${profile.host}:${profile.port}`;
    $('#quick-save-error').textContent = ''; saveDialog.showModal(); f.elements.name.focus();
  }
  document.addEventListener('DOMContentLoaded', () => {
    dialog = node('dialog'); dialog.id = 'quick-connect-dialog'; dialog.setAttribute('aria-labelledby','quick-connect-title');
    dialog.innerHTML = `<form id="quick-connect-form"><div class="dialog-heading"><h2 id="quick-connect-title">快速连接</h2><button class="icon-button" type="button" aria-label="关闭快速连接" data-close><svg><use href="#i-close"/></svg></button></div>
      <label>SSH 地址或命令<input name="command" id="quick-connect-command" required maxlength="2048" autocomplete="off" spellcheck="false" placeholder="ssh -p 22 root@example.com"></label>
      <p>支持 用户@主机、ssh -p 端口 用户@主机、IPv6，或 ping 主机进行本机探测。连接成功后可保存到服务器列表。</p>
      <div class="form-row"><label>认证方式<select name="auth"><option value="password">密码</option><option value="key">私钥</option><option value="agent">SSH Agent</option></select></label><label hidden>私钥<select name="keyId"></select></label></div>
      <pre id="quick-connect-output" role="status"></pre><div class="dialog-buttons"><button type="button" class="upload-button" id="quick-open-manager">服务器管理</button><button type="submit" class="primary-button" id="quick-connect-submit">连接 / 探测</button></div></form>`;
    document.body.append(dialog); form = dialog.querySelector('form'); output = $('#quick-connect-output');
    dialog.querySelector('[data-close]').onclick = () => dialog.close();
    form.elements.auth.onchange = () => { form.elements.keyId.parentElement.hidden = form.elements.auth.value !== 'key'; };
    $('#quick-open-manager').onclick = () => { dialog.close(); setDrawer(true); };
    form.onsubmit = async e => {
      e.preventDefault(); const button = $('#quick-connect-submit'); if (button.disabled) return;
      button.disabled = true; output.textContent = '';
      try { await run(form.elements.command.value, form.elements.auth.value, form.elements.keyId.value); }
      catch (error) { output.textContent = error.message; }
      finally { button.disabled = false; }
    };
    $('#add-session').onclick = () => open();
    const welcome = node('button', '', '快速连接'); welcome.id = 'welcome-quick-connect'; welcome.onclick = () => open(); $('#welcome-connect').before(welcome);
    const b = node('button','terminal-popout'); b.id = 'save-quick-connection'; b.hidden = true; b.append(icon('plus'),node('span','','保存连接')); b.title = '保存此临时连接'; b.onclick = saveCurrent; $('#toggle-sftp').before(b);
    saveDialog = node('dialog'); saveDialog.id = 'quick-save-dialog'; saveDialog.setAttribute('aria-labelledby','quick-save-title');
    saveDialog.innerHTML = `<form><div class="dialog-heading"><h2 id="quick-save-title">保存连接</h2><button type="button" class="icon-button" aria-label="取消保存" data-close><svg><use href="#i-close"/></svg></button></div><p id="quick-save-target"></p><label>名称<input name="name" required maxlength="100"></label><label>分组<input name="group" maxlength="100" value="快速连接"></label><label class="checkbox-label"><input name="remember" type="checkbox">记住本次登录凭据</label><p id="quick-save-error" role="alert"></p><button type="submit" class="primary-button">保存连接</button></form>`;
    document.body.append(saveDialog); saveDialog.querySelector('[data-close]').onclick = () => saveDialog.close();
    saveDialog.querySelector('form').onsubmit = async e => {
      e.preventDefault(); const f = e.currentTarget, b = f.querySelector('[type=submit]'); if (b.disabled) return; b.disabled = true;
      try {
        const id = savingID, p = await post(`/api/quick-connect/${id}/save`, { name:f.elements.name.value,group:f.elements.group.value,remember:f.elements.remember.checked,secret:f.elements.remember.checked ? credentials.get(id) || '' : '' });
        temporaryProfiles.delete(id); profiles = profiles.filter(p => p.id !== id); profiles.push(p); renderTabs(); reflect();
        saveDialog.close(); await loadProfiles(); toast('连接已保存');
      } catch (error) { $('#quick-save-error').textContent = error.message; } finally { b.disabled = false; }
    };
  }, { once:true });
  return { open, run, reflect };
})();
