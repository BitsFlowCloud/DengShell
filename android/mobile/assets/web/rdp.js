(() => {
  'use strict';
  const resolutions = new Map();
  const pending = new Map(), items = new Map(), dismissed = new Set();
  let available = false, refreshing = false, active = null, lastSSH = null, rendered = '', layoutQueued = false, viewportRequest = Promise.resolve();
  const byID = id => document.getElementById(id);
  const dialog = () => byID('rdp-sessions-dialog');
  const live = item => item && ['connecting','connected'].includes(item.status);
  function button(text, action) {
    const b = document.createElement('button'); b.type = 'button'; b.textContent = text;
    b.onclick = async () => { b.disabled = true; try { await action(); } catch (error) { toast(error.message || String(error)); } finally { b.disabled = false; } };
    return b;
  }
  function queueLayout() {
    if (layoutQueued) return;
    layoutQueued = true;
    requestAnimationFrame(() => {
      layoutQueued = false;
      viewportRequest = viewportRequest.catch(() => {}).then(async () => {
        if (!native()?.RDPViewport) return;
        const item = items.get(active), viewport = byID('rdp-viewport');
        const obstructed = document.hidden || document.querySelector('dialog[open], [role=menu]:not([hidden]), .themed-select-popup:not([hidden]), :popover-open') || !byID('connections-drawer').hidden || window.DengSecurityLock?.isLocked?.();
        const visible = !!active && item?.status === 'connected' && !obstructed && !viewport.closest('[hidden]');
        const r = viewport.getBoundingClientRect();
        // Serialize requests and calculate from current selection at execution
        // time, so a delayed resize cannot expose an old tab over a new dialog.
        await native().RDPViewport(active || '', Math.max(0,r.left), Math.max(0,r.top), Math.max(0,r.width), Math.max(0,r.height), innerWidth, visible);
      }).catch(() => {});
    });
  }
  function reflect() {
    const item = items.get(active);
    byID('rdp-panel').hidden = !item;
    if (!item) { queueLayout(); return; }
    byID('rdp-active-name').textContent = item.name;
    byID('rdp-resolution').value = resolutions.get(active) || '0x0';
    byID('rdp-active-status').textContent = item.message + (item.status === 'connected' && item.remoteWidth && item.remoteHeight ? ` · ${item.remoteWidth} × ${item.remoteHeight}` : '');
    byID('rdp-placeholder-message').textContent = item.message;
    byID('rdp-placeholder').hidden = item.status === 'connected';
    byID('rdp-secure-attention').disabled = item.status !== 'connected';
    byID('rdp-active-disconnect').disabled = !live(item);
    byID('rdp-active-reconnect').disabled = item.status === 'connecting';
    queueLayout();
  }
  function activateRDP(id) {
    if (!items.has(id) || dismissed.has(id)) return;
    if (activeID) lastSSH = activeID;
    activeID = null; active = id;
    for (const state of sessions.values()) state.host.hidden = true;
    window.DengProcessView?.activate(null,'terminal');
    document.body.classList.add('rdp-active');
    renderSessionInfo(); renderTabs(); reflect();
    requestAnimationFrame(() => document.querySelector('.session-tab.rdp-tab.active')?.scrollIntoView({block:'nearest',inline:'nearest'}));
  }
  function deactivate() {
    active = null; document.body.classList.remove('rdp-active'); byID('rdp-panel').hidden = true; queueLayout();
  }
  async function close(id) {
    dismissed.add(id);
    try { await remove(`/api/rdp/${encodeURIComponent(id)}`); }
    catch (error) { dismissed.delete(id); throw error; }
    items.delete(id); resolutions.delete(id);
    if (active === id) {
      const next = [...items.keys()].find(key => !dismissed.has(key));
      if (next) activateRDP(next);
      else { deactivate(); const ssh = sessions.has(lastSSH) ? lastSSH : sessions.keys().next().value; if (ssh) activate(ssh); else { renderSessionInfo(); renderTabs(); } }
    } else renderTabs();
    rendered = ''; renderList(); queueLayout();
  }
  async function disconnect(id) { await remove(`/api/rdp/${encodeURIComponent(id)}`); await refresh(); }
  async function reconnect(id) {
    const item = items.get(id); if (!item) return;
    await disconnect(id);
    const result = await connect(item.profileId);
    if (result) await close(id);
  }
  function appendTabs(host) {
    for (const item of items.values()) {
      if (dismissed.has(item.id)) continue;
      const selected = active === item.id;
      const tab = document.createElement('div'); tab.className = `session-tab rdp-tab${selected ? ' active' : ''}`; tab.dataset.sessionId = item.id;
      tab.dataset.connecting = String(item.status === 'connecting'); tab.dataset.failed = String(item.status === 'error');
      const select = button('', () => activateRDP(item.id)); select.setAttribute('role','tab'); select.setAttribute('aria-selected',String(selected)); select.setAttribute('aria-busy',String(item.status === 'connecting')); select.title = item.name + ' · RDP';
      const dot = document.createElement('span'); dot.className = `status-dot ${item.status === 'connected' ? 'green' : 'blue'}`;
      const text = document.createElement('span'); text.className = 'session-tab-text';
      const label = document.createElement('span'); label.className = 'session-tab-label'; label.textContent = item.name;
      const badge = document.createElement('span'); badge.className = 'session-current-badge'; badge.textContent = 'RDP'; text.append(label,badge); select.append(dot,text);
      const end = button('×', () => close(item.id)); end.className = 'tab-close'; end.title = '关闭 RDP 会话'; end.setAttribute('aria-label',`关闭 ${item.name}`);
      tab.append(select,end);host.append(tab);
    }
  }
  function renderList() {
    const values = [...items.values()].filter(item => !dismissed.has(item.id));
    const signature = JSON.stringify(values); if (signature === rendered) return; rendered = signature;
    byID('rdp-session-list').replaceChildren(); byID('rdp-session-empty').hidden = values.length > 0;
    for (const item of [...values].reverse()) {
      const row = document.createElement('section'); row.className = 'rdp-session-row'; row.dataset.status = item.status;
      const text = document.createElement('div'), name = document.createElement('strong'), status = document.createElement('p'); name.textContent = item.name; status.textContent = item.message; text.append(name,status);
      const actions = document.createElement('div'); actions.className = 'rdp-session-actions';
      actions.append(button('查看标签', () => { dialog().close(); activateRDP(item.id); }));
      actions.append(button(live(item) ? '断开' : '关闭', () => live(item) ? disconnect(item.id) : close(item.id)));
      row.append(text,actions); byID('rdp-session-list').append(row);
    }
    byID('open-rdp-sessions').textContent = `RDP 会话${values.filter(live).length ? ' · ' + values.filter(live).length : ''}`;
  }
  async function refresh() {
    if (refreshing || window.DengSecurityLock?.isLocked?.()) { queueLayout(); return; }
    refreshing = true;
    try {
      const data = await api('/api/rdp'); available = data.available && !!native()?.RDPConnect;
      byID('open-rdp-sessions').hidden = !available;
      byID('connection-protocol').querySelector('[value="rdp"]').disabled = !available;
      let changed = false;
      for (const item of data.sessions || []) {
        if (dismissed.has(item.id)) continue;
        if (JSON.stringify(items.get(item.id)) !== JSON.stringify(item)) { items.set(item.id,item); changed = true; }
      }
      if (changed) { renderTabs(); renderList(); reflect(); }
    } catch { /* The existing security overlay handles lock/transport failures. */ }
    finally { refreshing = false; }
  }
  async function open(profileID, {background = false} = {}) {
    const profile = connectionProfile(profileID); if (!profile) return null;
    if (!available) { await refresh(); if (!available) throw new Error('请在 Windows x64 测试版的主窗口使用 RDP 标签页'); }
    let secret = credentials.get(profileID) || '';
    if (!secret && !profile.hasSecret) {
      dialog().close();
      secret = await ask({title:`远程桌面密码 · ${profile.name}`,description:'密码仅用于本次连接。首次连接时还需要确认服务器证书。',input:true,secret:true,confirm:'连接'});
      if (secret === null) return null;
      if (!secret) throw new Error('请输入远程桌面密码');
    }
    connecting.add(profileID); renderConnections();
    try {
      const info = await native().RDPConnect(profileID,secret);
      items.set(info.id,info); setDrawer(false);
      if (!background || (!activeID && !active)) activateRDP(info.id); else renderTabs();
      renderList(); return {...info,protocol:'rdp'};
    } finally { connecting.delete(profileID); renderConnections(); }
  }
  window.DengRDP = {
    connect(profileID, options) {
      if (pending.has(profileID)) return pending.get(profileID);
      const request = open(profileID, options).finally(() => pending.delete(profileID));
      pending.set(profileID,request); return request;
    },
    has:id => items.has(id), active:() => active, close, deactivate, appendTabs, refresh,
    show() { if (!dialog().open) dialog().showModal(); queueLayout(); void refresh(); }
  };
  document.addEventListener('DOMContentLoaded', () => {
    byID('open-rdp-sessions').onclick = DengRDP.show;
    byID('close-rdp-sessions').onclick = () => dialog().close();
    byID('new-rdp-connection').onclick = () => { dialog().close(); showConnectionForm(); byID('connection-protocol').value = 'rdp'; byID('connection-protocol').dispatchEvent(new Event('change')); };
    byID('rdp-active-reconnect').onclick = safe(() => reconnect(active));
    byID('rdp-active-disconnect').onclick = safe(() => disconnect(active));
    byID('rdp-resolution').onchange = safe(async event => {
      const id = active, value = event.target.value; if (!id) return;
      const [width,height] = value.split('x').map(Number);
      await native().RDPResolution(id,width,height);resolutions.set(id,value);
    });
    byID('rdp-secure-attention').onclick = safe(() => native().RDPSendSecureAttention(active));
    const observer = new MutationObserver(queueLayout);
    observer.observe(document.body,{subtree:true,childList:true,attributes:true,attributeFilter:['open','hidden','class']});
    new ResizeObserver(queueLayout).observe(byID('rdp-viewport'));
    window.addEventListener('resize',queueLayout); document.addEventListener('visibilitychange',queueLayout);
    void refresh(); setInterval(() => void refresh(),750);
    // A native surface may be created after its tab was measured.
    setInterval(() => { if (active) queueLayout(); },300);
  });
})();
