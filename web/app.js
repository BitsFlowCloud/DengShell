'use strict';

const $ = s => document.querySelector(s);
const $$ = s => [...document.querySelectorAll(s)];
const node = (tag, className = '', text = '') => { const el = document.createElement(tag); el.className = className; el.textContent = text; return el; };
function icon(name, className = '') { const el = document.createElementNS('http://www.w3.org/2000/svg', 'svg'); const use = document.createElementNS(el.namespaceURI, 'use'); use.setAttribute('href', `#i-${name}`); el.setAttribute('class', className); el.setAttribute('aria-hidden', 'true'); el.append(use); return el; }
function readSaved(key, fallback) { return window.DengPortablePreferences.read(key, fallback); }
function save(key, value) { window.DengPortablePreferences.write(key, value); }
let toastTimer;
let toastLayerObserver;
const toastModalOrder = [];
function placeToast(force = false) {
  const notice = $('#toast'); if (!notice || notice.hidden) return;
  const isModal = dialog => { try { return dialog?.isConnected && dialog.matches('dialog:modal'); } catch { return dialog?.open && dialog.getAttribute('aria-modal') !== 'false'; } };
  const focused = document.activeElement?.closest('dialog');
  const modal = [...toastModalOrder].reverse().find(isModal) || (isModal(focused) ? focused : [...document.querySelectorAll('dialog[open]')].reverse().find(isModal));
  const parent = modal || document.body, moved = notice.parentElement !== parent;
  // A high z-index cannot rise above a native dialog backdrop. Keep the live
  // region inside its focus owner; a manual popover escapes scroll clipping.
  if (moved || force) {
    if (typeof notice.hidePopover === 'function' && notice.matches(':popover-open')) notice.hidePopover();
    if (moved) parent.append(notice);
  }
  if (typeof notice.showPopover === 'function') {
    notice.setAttribute('popover', 'manual');
    if (!notice.matches(':popover-open')) notice.showPopover();
  }
}
function toast(message) {
  clearTimeout(toastTimer);
  if (!toastLayerObserver) {
    toastLayerObserver = new MutationObserver(records => {
      for (const {target} of records) if (target.tagName === 'DIALOG') {
        const index = toastModalOrder.indexOf(target); if (index >= 0) toastModalOrder.splice(index, 1);
        if (target.open) toastModalOrder.push(target);
      }
      placeToast(true);
    });
    toastLayerObserver.observe(document.body, {subtree:true, attributes:true, attributeFilter:['open']});
  }
  const notice = $('#toast'); notice.textContent = message; notice.hidden = false; placeToast(true);
  toastTimer = setTimeout(() => {
    if (typeof notice.hidePopover === 'function' && notice.matches(':popover-open')) notice.hidePopover();
    notice.hidden = true; document.body.append(notice);
  }, 4500);
}
const safe = fn => (...args) => { try { return Promise.resolve(fn(...args)).catch(error => toast(error?.message || String(error))); } catch (error) { toast(error?.message || String(error)); } };
const native = () => window.go?.main?.Desktop;
function syncDesktopTheme() {
  const desktop = native();
  if (desktop?.SetTheme) desktop.SetTheme(window.CloudShellTheme.preference).catch(error => console.warn('无法同步窗口主题', error));
}
function reflectTheme() {
  const dark = window.CloudShellTheme.current === 'dark';
  $('#theme-toggle').setAttribute('aria-pressed', String(dark));
  const title = `${dark ? '切换到浅色模式' : '切换到深色模式'}${window.CloudShellTheme.preference === 'system' ? '（当前跟随系统，点击后改为手动）' : ''}`;
  $('#theme-toggle').title = title;
  $('#theme-toggle').setAttribute('aria-label', title);
  syncDesktopTheme();
}
$('#theme-toggle').onclick = () => window.CloudShellTheme.toggle();
window.addEventListener('cloudshell:theme', reflectTheme);
reflectTheme();
const boot = window.CLOUDSHELL;
const desktopPage = location.protocol === 'wails:' || location.hostname === 'wails.localhost';
let bridgeReady;
function waitForDesktop() {
  if (!bridgeReady) bridgeReady = (async () => {
    const deadline = Date.now() + 5000;
    while (!native()?.Request) { if (Date.now() >= deadline) throw new Error('桌面接口尚未就绪，请点击顶部服务器管理按钮重试'); await new Promise(resolve => setTimeout(resolve, 40)); }
  })().catch(error => { bridgeReady = null; throw error; });
  return bridgeReady;
}
function endpoint(path) { return boot.base + path; }
async function api(path, options = {}) {
  if (desktopPage) await waitForDesktop();
  if (native()) { try { return JSON.parse(await native().Request(options.method || 'GET', path, options.body || '')); } catch (error) { const message = error?.message || String(error); const marker = message.indexOf('DENGSHELL_ERROR:'); if (marker >= 0) { let data; try { data = JSON.parse(message.slice(marker + 16)); } catch {} if (data) throw Object.assign(new Error(data.error), { code: data.code, hostKey: data.hostKey }); } throw new Error(message); } } 
  const response = await fetch(endpoint(path), { ...options, headers: { 'Content-Type': 'application/json', 'X-CloudShell-Token': boot.token, ...options.headers } });
  const data = await response.json();
  if (!response.ok) throw Object.assign(new Error(data.error || `请求失败 (${response.status})`), { code: data.code, hostKey: data.hostKey });
  return data;
}
const post = (path, body) => api(path, { method: 'POST', body: JSON.stringify(body) });
const remove = path => api(path, { method: 'DELETE' });
function prettySize(bytes) { if (bytes == null || !Number.isFinite(bytes)) return '—'; if (bytes < 1024) return `${Math.round(bytes)} B`; for (const [power, unit] of [[4, 'TB'], [3, 'GB'], [2, 'MB'], [1, 'KB']]) if (bytes >= 1024 ** power) return `${(bytes / 1024 ** power).toFixed(1)} ${unit}`; }
function normalizePath(value, base = '/') { if (value === '~') return current()?.home || '/'; if (value.startsWith('~/')) value = (current()?.home || '') + value.slice(1); const result = []; for (const part of (value.startsWith('/') ? value : `${base}/${value}`).split('/')) { if (!part || part === '.') continue; if (part === '..') result.pop(); else result.push(part); } return '/' + result.join('/'); }
const parentPath = path => normalizePath('..', path);

// Consistent in-app dialogs; serialised so multiple transfer errors cannot overlap.
let dialogQueue = Promise.resolve();
function ask({ title, description = '', input = false, value = '', secret = false, confirm = '确定', signal }) {
  const result = dialogQueue.then(() => new Promise(resolve => {
    if (signal?.aborted) return resolve(null);
    const dialog = $('#action-dialog');
    $('#action-title').textContent = title; $('#action-description').textContent = description;
    $('#action-input-label').hidden = !input; $('#action-input').type = secret ? 'password' : 'text'; $('#action-input').value = value;
    $('#action-confirm').textContent = confirm;
    let done = false;
    const cancel = () => finish(null);
    const finish = answer => { if (done) return; done = true; signal?.removeEventListener('abort', cancel); dialog.close(); resolve(answer); };
    signal?.addEventListener('abort', cancel, { once: true });
    $('#action-form').onsubmit = event => { event.preventDefault(); finish(input ? $('#action-input').value : true); };
    $('#action-cancel').onclick = () => finish(null);
    dialog.oncancel = event => { event.preventDefault(); finish(null); };
    dialog.showModal(); (input ? $('#action-input') : $('#action-confirm')).focus();
  }));
  dialogQueue = result.then(() => undefined); return result;
}

let profiles = [], groups = [], activeID = null, ascending = true, fileSortKey = 'name', selectedName = '';
const sessions = new Map(), connecting = new Set(), credentials = new Map(), localTasks = new Map();
const connectionRequests = new Map();
const connectionAttempts = new Map();
let nextSessionOrder = 0;
const current = () => sessions.get(activeID);
const profileFor = session => profiles.find(p => p.id === session?.profileId);
async function loadProfiles() {
  if (window.DengPortablePreferences.ready) await window.DengPortablePreferences.flush();
  const config = await api('/api/config');
  window.DengPortablePreferences.accept(config);
  window.DengCommandHistory.acceptConfig(config);
  layout = readSaved('cloudshell.layout', { version: 2 });
  serverManager.collapsed = null;
  profiles = config.servers; groups = config.groups; commands = config.commands || []; commandGroups = config.commandGroups || []; managedKeys = config.keys || [];
  acceptServerManagerConfig(config); renderConnections(); renderTabs(); renderCommands(); renderKeyChoices(); renderKeys();
  await acceptAppearanceConfig(config);
  clampLayouts();
  const savedPane = readSaved('dengshell.workspace', {}).pane;
  if (['files', 'commands', 'common-apps', 'transfers'].includes(savedPane)) showPane(savedPane);
  window.DengShellHelp?.acceptConfig(config);
}

async function connect(profileID, force = false, options = {}) {
  // Concurrent callers share only the request for this profile, never another PTY.
  if (connectionRequests.has(profileID)) return connectionRequests.get(profileID);
  const request = connectProfile(profileID, force, options);
  connectionRequests.set(profileID, request);
  try { return await request; } finally { if (connectionRequests.get(profileID) === request) connectionRequests.delete(profileID); }
}
async function connectProfile(profileID, force, { background = false, refreshHistory = true } = {}) {
  if (connecting.has(profileID)) return null;
  const profile = profiles.find(p => p.id === profileID); if (!profile) return;
  if (profile.auth === 'key' && !profile.keyId && !profile.keyPath) {
    toast(`「${profile.name}」未找到私钥，请重新配置密钥。`);
    if (!background) showConnectionForm(profile);
    return null;
  }
  const matches = [...sessions.values()].filter(s => s.profileId === profileID);
  const existing = matches.find(s => s.id === activeID) || matches.find(s => s.connected) || matches[0];
  if (existing?.detaching || existing?.handoffProvisional || existing?.ownershipUncertain) { toast('此 SSH 正在交接窗口，请稍后再试'); return null; }
  if (existing?.connected && !force) { if (!background) { activate(existing.id); setDrawer(false); } return existing; }
  const tabOrder = existing?.tabOrder ?? nextSessionOrder++;
  const state = makeSessionState({ id: `pending:${profileID}:${crypto.randomUUID()}`, profileId: profileID, home: '/' });
  Object.assign(state, { tabOrder, connected: false, localOnly: true, pendingConnection: true, connectionMessage: '正在连接…', connectionAbort: new AbortController() });
  const alive = () => !state.closed && sessions.get(state.id) === state;
  connecting.add(profileID); connectionAttempts.set(profileID, state);
  // Removing an old session is independent of every other selected profile.
  const previousClose = existing ? preserveReconnectTerminal(existing, state) : Promise.resolve();
  sessions.set(state.id, state);
  try {
    createTerminal(state);
    if (!background || !current()) activate(state.id); else renderTabs();
    if (!background) setDrawer(false);
    renderConnections();
    let secret = credentials.get(profileID) || '';
    // Managed key credentials are resolved by Go first. Another window may
    // have saved its passphrase since this window last loaded the key list.
    if (!secret && !profile.hasSecret && (profile.auth === 'password' || (profile.auth === 'key' && !profile.keyId))) {
      showConnectionProgress(state, '等待输入凭据…');
      secret = await ask({ title: profile.auth === 'key' ? `私钥口令 · ${profile.name}` : `连接 ${profile.name}`, description: profile.auth === 'key' ? '如果私钥没有加密，可直接确定。' : `${profile.user}@${profile.host}:${profile.port}`, input: true, secret: true, confirm: '连接', signal: state.connectionAbort.signal });
      if (!alive()) return null;
      if (secret === null) { showConnectionProgress(state, '已取消连接', true); return null; }
      if (profile.auth === 'password' && !secret) throw new Error('请输入 SSH 密码');
    }
    await previousClose;
    if (!alive()) return null;
    await restoreReconnectTerminal(state);
    if (!alive()) return null;
    showConnectionProgress(state, '正在连接…');
    const requestConnection = async () => {
      let hostKeyApproval;
      for (let confirmations = 0; ; confirmations++) {
        try { return await api('/api/sessions', { method: 'POST', body: JSON.stringify({ profileId: profileID, secret, hostKeyApproval }), signal: state.connectionAbort.signal }); }
        catch (error) {
          if (!alive()) return null;
          if (!['ssh_host_key_unknown', 'ssh_host_key_changed'].includes(error.code) || !error.hostKey || confirmations >= 2) throw error;
          const key = error.hostKey, changed = error.code === 'ssh_host_key_changed';
          showConnectionProgress(state, '等待核实服务器指纹…');
          const accepted = await ask({ title: changed ? `服务器指纹已变化 · ${profile.name}` : `首次连接 · ${profile.name}`, description: `${key.host}\n算法：${key.algorithm}\n${changed ? `原指纹：${key.previousFingerprint}\n` : ''}新指纹：${key.fingerprint}\n\n${changed ? '服务器重装、密钥更换或连接被冒充都可能导致此变化。' : ''}请通过服务器控制台或可信管理员核对指纹；确认一致后才继续发送登录凭据。`, confirm: '已核实，信任并连接', signal: state.connectionAbort.signal });
          if (!alive()) return null;
          if (!accepted) { showConnectionProgress(state, '已取消连接', true); return null; }
          hostKeyApproval = { host: key.host, fingerprint: key.fingerprint, previousFingerprint: key.previousFingerprint };
          showConnectionProgress(state, '正在连接…');
        }
      }
    };
    let info;
    try { info = await requestConnection(); }
    catch (error) {
      if (!alive()) return null;
      credentials.delete(profileID);
      const needsKeyPassphrase = profile.auth === 'key' && ['ssh_key_passphrase_required', 'ssh_key_passphrase_invalid'].includes(error.code);
      if (!needsKeyPassphrase && !(profile.auth === 'password' && error.code === 'ssh_authentication_failed')) throw error;
      showConnectionProgress(state, '等待重新输入凭据…');
      secret = await ask({ title: `${needsKeyPassphrase ? (error.code === 'ssh_key_passphrase_required' ? '私钥口令' : '重新输入私钥口令') : '重新输入 SSH 密码'} · ${profile.name}`, description: error.message + '\n本次输入会替代缓存凭据，仅保存在本次运行中。', input: true, secret: true, confirm: '连接', signal: state.connectionAbort.signal });
      if (!alive()) return null;
      if (secret === null) { showConnectionProgress(state, '已取消连接', true); return null; }
      if (profile.auth === 'password' && !secret) throw new Error('请输入 SSH 密码');
      showConnectionProgress(state, '正在连接…');
      info = await requestConnection();
    }
    if (!info) return null;
    // The user may close/retry this tab while a native request is still running.
    // Dispose only that late result; never resurrect its placeholder or touch a newer attempt.
    if (!alive()) { await remove(`/api/sessions/${info.id}`).catch(() => {}); return null; }
    const pendingID = state.id, selected = activeID === pendingID;
    sessions.delete(pendingID);
    Object.assign(state, info, { cwd: info.home, connected: true, localOnly: false, pendingConnection: false });
    sessions.set(state.id, state); state.host.dataset.session = state.id;
    state.connectionView?.remove(); state.connectionView = null;
    state.openTerminalSocket();
    // Each tab becomes usable independently, preserving order and the user's current focus.
    if (selected) activate(state.id); else renderTabs();
    if (secret) credentials.set(profileID, secret);
    window.DengCommandHistory?.refresh(profileID).catch(error => toast('命令历史刷新失败：' + error.message));
    if (current() === state) { pollStats(); pollLatency(); }
    // A slow or failed SFTP listing must not block any other SSH connection.
    navigate(info.home, state).catch(error => { if (!state.closed) toast(`${profile.name} 已连接，目录读取失败：${error.message}`); });
    if (refreshHistory) safe(refreshServerManagerHistory)();
    return state;
  } catch (error) {
    if (alive()) {
      showConnectionProgress(state, error.message || String(error), true); toast(`${profile.name}：${error.message || error}`);
      if (!background && ['ssh_key_missing', 'ssh_key_unavailable', 'ssh_private_key_invalid'].includes(error.code)) showConnectionForm(profile);
    }
    return null;
  } finally {
    if (connectionAttempts.get(profileID) === state) { connectionAttempts.delete(profileID); connecting.delete(profileID); }
    if (alive() && state.localOnly) { state.pendingConnection = false; renderTabs(); if (current() === state) renderSessionInfo(); }
    renderConnections(); updateStatus();
  }
}
function writeTerminalAndWait(state, text = '') {
  if (state.closed) return Promise.resolve(false);
  return new Promise((resolve, reject) => {
    state.terminalWriteWaiters ||= new Set();
    const done = () => { state.terminalWriteWaiters.delete(done); resolve(!state.closed); };
    state.terminalWriteWaiters.add(done);
    try { state.term.write(text, done); }
    catch (error) { state.terminalWriteWaiters.delete(done); reject(error); }
  });
}
async function preserveReconnectTerminal(previous, next) {
  // Drain xterm's asynchronous write queue, including the final output frame
  // and shell-confirmed history events, before disposing the old renderer.
  await writeTerminalAndWait(previous);
  if (!previous.closed) {
    next.reconnectScreen = {
      cols: previous.term.cols, rows: previous.term.rows,
      // A new SSH shell must not inherit an old TUI's mouse/paste/alternate
      // screen modes. Only rendered normal-buffer text and colors are copied.
      text: previous.serialize.serialize({ excludeAltBuffer: true, excludeModes: true }),
    };
    await closeSession(previous.id);
  }
}
async function restoreReconnectTerminal(state) {
  const screen = state.reconnectScreen;
  if (!screen || state.closed) return;
  state.term.resize(screen.cols, screen.rows);
  await writeTerminalAndWait(state, screen.text + '\x1b[0m\r\n\x1b[38;5;245m── 重新连接 · 以上为上一会话记录 ──\x1b[0m\r\n');
  state.reconnectScreen = null;
  if (current() === state && !state.closed) fitActive();
}
function showConnectionProgress(state, message, failed = false) {
  state.connectionMessage = message; state.connectionFailed = failed;
  if (state.connectionView) {
    state.connectionView.classList.toggle('failed', failed);
    state.connectionView.querySelector('.connection-progress-message').textContent = message;
    state.connectionView.querySelector('small').textContent = failed ? '可点击重新连接重试，其他 SSH 不受影响。' : '此连接独立进行，可随时切换其他标签。';
  }
  if (current() === state) renderSessionInfo();
}
function makeSessionState(info) { return { ...info, connected: true, ready: false, cwd: info.home, entries: [], folders: new Map(), expanded: new Set(), history: getCommandHistory(info.profileId), historyIndex: getCommandHistory(info.profileId).length, navGeneration: 0, stats: null, chart: [], latency: null, follow: false, ws: null }; }
// Desktop IPC keeps WebKit independent of system proxy and loopback policies.
class DesktopSocket {
  constructor(id, handoffNonce = '') {
    this.readyState = WebSocket.CONNECTING; let finished = false; let writes = Promise.resolve();
    const finish = () => { if (finished) return; finished = true; this.readyState = WebSocket.CLOSED; off(); this.onclose?.(); };
    const off = window.runtime.EventsOn('cloudshell:terminal:' + id, message => {
      if ((message.handoff || '') !== handoffNonce) return;
      if (message.kind === 0) { finish(); return; }
      const bytes = Uint8Array.from(atob(message.data), c => c.charCodeAt(0));
      this.onmessage?.({ data: message.kind === 2 ? bytes.buffer : new TextDecoder().decode(bytes) });
    });
    this.send = data => { writes = writes.then(() => (native().SendTerminalWithHandoff ? native().SendTerminalWithHandoff(id, handoffNonce, data) : native().SendTerminal(id, data))).catch(() => finish()); };
    this.close = () => { (native().CloseTerminalWithHandoff ? native().CloseTerminalWithHandoff(id, handoffNonce) : native().CloseTerminal(id)).finally(finish); };
    queueMicrotask(() => { this.readyState = WebSocket.OPEN; (native().OpenTerminalWithHandoff ? native().OpenTerminalWithHandoff(id, handoffNonce) : native().OpenTerminal(id)).catch(error => { toast(String(error)); this.onerror?.(); finish(); }); });
  }
}

function createTerminal(state) {
  const host = node('div', 'terminal-session'); host.dataset.session = state.id; host.hidden = true; $('#terminal-output').append(host); state.host = host;
  const terminal = new Terminal({ cols: state.restoration?.cols || 100, rows: state.restoration?.rows || 30, fontSize: terminalFont, fontFamily: terminalFontFamily, fontWeight: boldForFont() ? '700' : '400', fontWeightBold: '700', lineHeight: 1.25, cursorBlink: !matchMedia('(prefers-reduced-motion: reduce)').matches, scrollback: 10000, allowTransparency: true, theme: terminalTheme() });
  const fit = new FitAddon.FitAddon(); terminal.loadAddon(fit); const serialize = new SerializeAddon.SerializeAddon(); terminal.loadAddon(serialize); state.serialize = serialize; terminal.open(host); state.term = terminal; state.fit = fit;
  if (state.localOnly) {
    terminal.options.disableStdin = true;
    const view = node('div', 'terminal-connection-state'); view.setAttribute('role', 'status');
    view.append(node('strong', '', profileFor(state)?.name || 'SSH'), node('span', 'connection-progress-message', state.connectionMessage), node('small', '', '此连接独立进行，可随时切换其他标签。'));
    host.append(view); state.connectionView = view;
  }
  installTerminalFontMetrics(state);
  bindTerminalContext(state);
  bindShellIntegration(state);
  if (state.restoration?.clientState?.shellIntegration) state.shellIntegration = { ...state.restoration.clientState.shellIntegration };
  terminal.onData(data => sendInput(state, data));
  terminal.onResize(({ cols, rows }) => { if (!state.detaching && !state.restoring) sendMessage(state, { type: 'resize', cols, rows }); });
  terminal.attachCustomKeyEventHandler(event => {
    if (event.type !== 'keydown') return true;
    if (event.ctrlKey && !event.altKey && event.code === 'KeyC') {
      event.preventDefault();
      if (event.shiftKey) copyTerminalSelection(state).catch(() => toast('无法访问剪贴板'));
      else { terminal.clearSelection(); sendInput(state, '\x03'); }
      return false;
    }
    if (event.ctrlKey && !event.altKey && event.shiftKey && event.code === 'KeyV') { event.preventDefault(); pasteClipboard(state).catch(() => toast('无法访问剪贴板')); return false; }
    return true;
  });
  terminal.parser.registerOscHandler(7, data => { if (!state.follow) return false; try { const url = new URL(data); if (url.protocol === 'file:') { const path = decodeURIComponent(url.pathname); if (path !== state.cwd) navigate(path, state).catch(error => toast(error?.message || String(error))); } } catch {} return true; });
  const openSocket = () => {
  if (state.closed) return;
  const socketURL = new URL(endpoint(`/api/sessions/${state.id}/terminal`)); socketURL.protocol = socketURL.protocol === 'https:' ? 'wss:' : 'ws:'; socketURL.searchParams.set('token', boot.token); socketURL.searchParams.set('cols', String(terminal.cols)); socketURL.searchParams.set('rows', String(terminal.rows)); if (state.handoffNonce) socketURL.searchParams.set('handoff', state.handoffNonce);
  const ws = native() ? new DesktopSocket(state.id, state.handoffNonce || '') : new WebSocket(socketURL); ws.binaryType = 'arraybuffer'; state.ws = ws;
  ws.onmessage = event => {
    if (state.closed || !state.connected || sessions.get(state.id) !== state) return;
    if (event.data instanceof ArrayBuffer) { terminal.write(new Uint8Array(event.data), () => sendMessage(state, { type: 'ack' })); return; }
    const message = JSON.parse(event.data);
    if (window.DengSessionWindows.handleMessage(state, message)) return;
    if (message.type === 'ready') { acceptShellIntegration(state, message.integration); state.ready = true; state.handoffProvisional = false; state.ownershipUncertain = false; terminal.options.disableStdin = !!state.detaching || !state.connected; state.restoring = false; state.restoration = null; if (activeID === state.id) renderSessionInfo(); if (activeID === state.id) { fitActive(); terminal.focus(); } }
    else if (message.message) terminal.writeln(`\r\n\x1b[38;5;245m${message.message.replaceAll('\x1b', '')}\x1b[0m`);
  };
  ws.onclose = () => { if (state.ownershipUncertain && !state.detaching) dropSessionView(state.id); else if (!state.detaching) markSessionDisconnected(state); };
  ws.onerror = () => toast('终端连接失败，请重新连接');
  };
  state.openTerminalSocket = openSocket;
  if (state.localOnly) return;
  if (state.restoration) terminal.write(state.restoration.terminal, () => {
    try { DengTerminalSnapshot.restore(terminal, state.restoration.clientState.terminalRuntime); openSocket(); }
    catch (error) { state.windowRestoreError = error; if (state.initialWindowRestore) { toast(error.message); dropSessionView(state.id, state); post('/api/windows/handoff/'+state.handoffNonce+'/cancel',{}).then(result=>{if(!result.attached&&!native())window.close()}).catch(()=>{}); } }
  }); else openSocket();
}
function sendMessage(state, value) { if (state.ws?.readyState === WebSocket.OPEN) state.ws.send(JSON.stringify(value)); }
function sendInput(state, data) {
  if (!state.connected || !state.ready || state.detaching || state.restoring || state.ownershipUncertain) return;
  // Any input can leave an unfinished line, start a command, or enter a TUI.
  // Only the next authenticated shell prompt makes automatic command injection
  // safe again; do not guess prompt state from local echo or key sequences.
  if (data.length && state.shellIntegration) state.shellIntegration.atPrompt = false;
  for (let start = 0; start < data.length;) { let end = Math.min(start + 16000, data.length); if (end < data.length && /[\uD800-\uDBFF]/.test(data[end - 1])) end--; sendMessage(state, { type: 'input', data: data.slice(start, end) }); start = end; }
}
function fitActive() { const state = current(); if (state && !state.detaching && !state.restoring && !state.host.hidden && state.host.clientWidth > 0 && state.host.clientHeight > 0) { state.fit.fit(); if (state.ready) sendMessage(state, { type: 'resize', cols: state.term.cols, rows: state.term.rows }); } }
new ResizeObserver(() => requestAnimationFrame(fitActive)).observe($('#terminal-output'));
function markSessionDisconnected(state) {
  if (state.closed || !state.connected) return;
  state.connected = false; state.ready = false; state.navGeneration++; state.navAbort?.abort();
  state.term.options.disableStdin = true;
  state.term.writeln('\r\n\x1b[38;5;245m连接已断开，终端内容已保留。点击重新连接可建立新会话。\x1b[0m');
  showDisconnectDiagnostic(state);
  renderTabs();
  if (activeID === state.id) { renderSessionInfo(); renderFiles(); }
}
async function showDisconnectDiagnostic(state) {
  if (state.disconnectDiagnosticRequested) return;
  state.disconnectDiagnosticRequested = true;
  let result;
  try {
    for (let attempt = 0; attempt < 4; attempt++) {
      let timer;
      try {
        result = await Promise.race([
          api(`/api/sessions/${state.id}/disconnect-diagnostic`),
          new Promise((_, reject) => { timer = setTimeout(() => reject(new Error('诊断接口未响应')), 2000); }),
        ]);
      } finally { clearTimeout(timer); }
      if (result.code || state.closed) break;
      await new Promise(resolve => setTimeout(resolve, 150));
    }
  } catch {}
  if (state.closed) return;
  if (!/^DS-\d{3}$/.test(result?.code || '')) result = { code: 'DS-290', traceId: state.id.slice(0, 12), message: '无法取得后端断开原因，可能是本地窗口通信异常；请提供本地诊断日志。' };
  state.disconnectDiagnostic = result;
  const clean = value => String(value || '').replace(/[\x00-\x1f\x7f-\x9f]/g, ' ');
  const label = `断开诊断码 ${result.code}${result.traceId ? ' / ' + clean(result.traceId) : ''}`;
  state.term.writeln(`\r\n\x1b[38;5;214m[${label}]\x1b[0m ${clean(result.message)}`);
  if (result.logPath) state.term.writeln(`诊断日志：${clean(result.logPath)}${result.logWriteFailed ? '（文件写入失败，请保留本页诊断码）' : ''}`);
  if (current() === state) {
    $('#terminal-state').textContent = `已断开 · ${result.code}`;
    $('#terminal-state').title = label + ' · ' + clean(result.message);
  }
}
async function disconnectSession(state = current()) {
  if (!state?.connected || state.disconnecting || state.detaching || state.handoffProvisional || state.ownershipUncertain) return;
  state.disconnecting = true;
  if (activeID === state.id) $('#disconnect').disabled = true;
  try {
    await remove(`/api/sessions/${state.id}`);
    markSessionDisconnected(state); state.ws?.close();
  } finally {
    state.disconnecting = false;
    if (activeID === state.id) { $('#disconnect').disabled = !state.connected; $('#reconnect').disabled = false; }
  }
}
function dropSessionView(id, expected = null) {
  const state = sessions.get(id); if (!state || expected && state !== expected) return;
  state.closed = true; state.connected = false; state.ready = false;
  for (const done of state.terminalWriteWaiters || []) done();
  if (state.localOnly) {
    state.connectionAbort?.abort();
    if (connectionAttempts.get(state.profileId) === state) {
      connectionAttempts.delete(state.profileId); connecting.delete(state.profileId); connectionRequests.delete(state.profileId);
    }
  }
  window.DengProcessView?.drop(id);
  state.navAbort?.abort(); state.ws?.close(); state.term?.dispose(); state.host?.remove(); sessions.delete(id);
  if (activeID === id) activeID = sessions.keys().next().value || null;
  if (activeID) activate(activeID, window.DengProcessView?.active() === activeID ? 'processes' : 'terminal'); else { renderTabs(); renderSessionInfo(); renderFiles(); }
}
async function closeSession(id) {
  const state = sessions.get(id); if (!state || state.detaching || state.handoffProvisional) return;
  dropSessionView(id);
  if (state.ownershipUncertain || state.localOnly) return;
  await remove(`/api/sessions/${id}`).catch(() => {});
}
function renderTabs() {
  window.DengSessionWindows.cancelDrag();
  $('#session-tabs').setAttribute('role', 'tablist');
  $('#session-tabs').replaceChildren(...[...sessions.values()].sort((a,b) => (a.tabOrder ?? 0) - (b.tabOrder ?? 0)).flatMap(state => {
    const selected = state.id === activeID && !window.DengProcessView?.active();
    const profile = profileFor(state); const tab = node('div', `session-tab${selected ? ' active' : ''}`);
    tab.dataset.sessionId = state.id;
    tab.dataset.connecting = String(!!state.pendingConnection);
    tab.dataset.failed = String(!!state.connectionFailed);
    const label = node('span', 'session-tab-text');
    label.append(node('span', 'session-tab-label', profile?.name || '已删除配置'));
    const button = node('button'); button.type = 'button'; button.append(node('span', `status-dot ${state.connected ? 'green' : 'blue'}`), label); button.onclick = () => activate(state.id); button.setAttribute('role', 'tab'); button.setAttribute('aria-selected', String(selected)); button.setAttribute('aria-current', selected ? 'page' : 'false'); button.title = profile ? `${profile.name} · ${profile.user}@${profile.host}:${profile.port}` : '已删除配置';
    if (selected && state.connected) { label.append(node('span', 'session-current-badge', '当前连接')); tab.classList.add('current-session'); }
    button.setAttribute('aria-busy', String(!!state.pendingConnection));
    if (state.localOnly) button.title += ` · ${state.connectionMessage}`;
    const close = node('button', 'tab-close'); close.append(icon('close')); close.title = state.pendingConnection ? '取消连接' : '关闭连接'; close.setAttribute('aria-label', `${state.pendingConnection ? '取消' : '关闭'} ${profile?.name || '会话'}`); close.onclick = safe(() => closeSession(state.id)); close.disabled = !!(state.detaching || state.handoffProvisional); tab.dataset.detaching = String(!!state.detaching); tab.append(button, close); window.DengSessionWindows.bindTab(tab, state); return [tab, ...(window.DengProcessView?.tabs(state) || [])];
  })); updateStatus(); window.DengTextEditors?.reflect(); window.DengCommandComposer?.reflect();
}
function activate(id, view = 'terminal') {
  if (!sessions.has(id)) return;
  activeID = id; selectedName = ''; $('#file-filter').value = '';
  window.DengProcessView?.activate(id, view);
  for (const state of sessions.values()) state.host.hidden = state.id !== id;
  renderTabs(); renderSessionInfo(); renderFiles();
  requestAnimationFrame(() => {
    if (activeID !== id) return;
    $('#session-tabs .session-tab.active')?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
    if (!window.DengProcessView?.active()) { fitActive(); current()?.term.focus(); }
  });
}
function updateStatus() { $('#connection-button').setAttribute('aria-label', '管理服务器'); window.DengSessionWindows.reflect(); }
function renderSessionInfo() {
  const state = current(), profile = profileFor(state);
  $('#welcome-state').hidden = !!state;
  $('#terminal-meta-host').textContent = profile ? `${profile.user}@${profile.name}` : 'SSH 终端'; $('#terminal-state').textContent = state?.handoffProvisional ? '正在接收标签…' : state?.pendingConnection ? '正在连接…' : state?.connectionFailed ? '连接未完成' : state?.connected ? '已连接' : state?.disconnectDiagnostic ? `已断开 · ${state.disconnectDiagnostic.code}` : '待连接';
  $('#terminal-state').title = state?.disconnectDiagnostic?.message || '';
  $('#command-history').disabled = false; $('#command-input').disabled = !state?.connected; $('#command-input').value = ''; resizeCommandInput(); $('#reconnect').disabled = !state || !!(state.pendingConnection || state.disconnecting || state.detaching || state.handoffProvisional || state.ownershipUncertain); $('#disconnect').disabled = !state?.connected || !!(state.disconnecting || state.detaching || state.handoffProvisional || state.ownershipUncertain);
  $('#follow-terminal').checked = !!state?.follow; $('#follow-terminal').disabled = !state?.ready;
  renderMonitor(state?.connected ? state.stats : null); renderLatency(); renderCommands(); window.DengCommonApps?.render(); window.DengProcessView?.reflect(); updateStatus();
}
$('#command-form').onsubmit = event => { event.preventDefault(); const state = current(); const input = $('#command-input'); if (!state?.ready || !input.value) return; pasteTerminalText(state, input.value, { execute: true }); input.value = ''; resizeCommandInput(); };
$('#command-input').onkeydown = event => { if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) { event.preventDefault(); $('#command-form').requestSubmit(); return; } const state = current(); if (!state || !['ArrowUp', 'ArrowDown'].includes(event.key)) return; event.preventDefault(); state.historyIndex = Math.max(0, Math.min(state.history.length, state.historyIndex + (event.key === 'ArrowUp' ? -1 : 1))); event.target.value = state.history[state.historyIndex] || ''; resizeCommandInput(); };
$('#reconnect').onclick = safe(() => current() && connect(current().profileId, true));
$('#disconnect').onclick = safe(() => disconnectSession());
$('#clear-terminal').onclick = () => { current()?.term.clear(); current()?.term.focus(); };
let terminalFont = normalizeTerminalFontSize(appearance.terminalFontSize);
function changeFont(amount) { terminalFont = normalizeTerminalFontSize(terminalFont + amount); for (const state of sessions.values()) state.term.options.fontSize = terminalFont; persistAppearance({ terminalFontSize: terminalFont }).catch(error => toast(error.message)); renderAppearanceControls(); fitActive(); }
$('#font-up').onclick = () => changeFont(.5); $('#font-down').onclick = () => changeFont(-.5);
$('#follow-terminal').onchange = safe(async event => {
  const state = current(); if (!state?.connected) return;
  if (!event.target.checked) { state.follow = false; return; }
  if (!state.shellIntegration?.ready) {
    event.target.checked = false;
    toast('目录跟随需要 Bash / Zsh / Fish 提示符集成，请等待终端就绪或重新连接。');
    return;
  }
  state.follow = true;
  if (state.terminalDirectory && state.terminalDirectory !== state.cwd) await navigate(state.terminalDirectory, state);
});

// SFTP browser. Only successful requests replace the current path and entries.
async function navigate(path, state = current()) {
  if (!state?.connected) return;
  path = normalizePath(path, state.cwd);
  const generation = ++state.navGeneration; state.navAbort?.abort(); state.navAbort = new AbortController();
  if (activeID === state.id) $('#file-status-count').textContent = '读取目录…';
  try {
    const data = await api(`/api/sessions/${state.id}/files?path=${encodeURIComponent(path)}`, { signal: state.navAbort.signal });
    if (state.navGeneration !== generation || !sessions.has(state.id)) return;
    state.cwd = data.path; state.entries = data.entries; state.folders.set(data.path, data.entries.filter(entry => entry.kind === 'folder')); DengFileBrowser.invalidate(state, data.path);
    if (activeID === state.id) { selectedName = ''; $('#file-filter').value = ''; renderFiles(); }
  } catch (error) { if (error.name === 'AbortError') return; if (activeID === state.id) { $('#path-input').value = state.cwd; $('#file-status-count').textContent = '目录读取失败'; } throw error; }
}
function renderFiles() {
  const state = current(); const filter = $('#file-filter').value.toLowerCase();
  const entries = (state?.entries || []).filter(e => e.name.toLowerCase().includes(filter)).slice().sort((a, b) => compareFileEntries(a, b, fileSortKey, ascending));
  $('#path-input').value = state?.cwd || ''; $('#path-input').disabled = !state?.connected; $('#drop-path').textContent = state?.cwd || '—';
  $('#file-status-count').textContent = state ? `${entries.length} 个项目${filter ? ' · 已筛选' : ''}` : '尚未连接'; $('#file-empty').hidden = !!entries.length; $('#file-empty').textContent = state ? '此目录暂无匹配文件' : '连接后浏览远程文件';
  for (const id of ['parent-directory', 'refresh-files', 'mkdir', 'choose-files']) $(`#${id}`).disabled = !state?.connected;
  $('#parent-directory').disabled ||= state?.cwd === '/';
  $('#file-list').replaceChildren(...entries.map(entry => {
    const row = node('tr', selectedName === entry.name ? 'selected' : ''); row.tabIndex = 0; row.dataset.fileName = entry.name; row.setAttribute('aria-label', entry.name);
    const first = node('td'); const name = node('span', 'file-name'); name.append(icon(entry.kind === 'folder' ? 'folder' : 'file', `${entry.kind}-icon`), node('span', 'file-name-text', entry.name + (entry.link ? ' ↗' : ''))); first.append(name);
    row.append(first, node('td', '', entry.kind === 'folder' ? '—' : prettySize(entry.bytes)), node('td', '', entry.time), node('td', '', entry.mode), node('td', '', entry.owner));
    for (const cell of row.cells) cell.title = cell.textContent;
    row.onclick = () => { selectedName = entry.name; $$('#file-list tr').forEach(el => el.classList.remove('selected')); row.classList.add('selected'); updateFileActions(); };
    const open = safe(async () => { const target = normalizePath(entry.name, state.cwd); if (entry.kind === 'folder') await navigate(target, state); else if (entry.link) { try { await navigate(target, state); } catch { await DengFileTools.openText(state, target); } } else await DengFileTools.openText(state, target); });
    row.ondblclick = open; row.onkeydown = event => { if (event.key === 'Enter') open(); }; return row;
  })); renderTree(); updateFileActions();
}
function selectedEntry() { return current()?.entries.find(e => e.name === selectedName); }
function updateFileActions() { const entry = selectedEntry(); $('#download-file').disabled = !current()?.connected || !entry || entry.kind === 'folder'; $('#rename-file').disabled = !current()?.connected || !entry; $('#delete-file').disabled = !current()?.connected || !entry; }
function renderTree() { window.DengFileBrowser.render(current()); }
$('#path-form').onsubmit = safe(async event => { event.preventDefault(); await navigate($('#path-input').value); });
$('#parent-directory').onclick = safe(() => navigate(parentPath(current().cwd)));
$('#refresh-files').onclick = safe(() => navigate(current().cwd));
$('#file-filter').oninput = renderFiles;
function compareFileEntries(a, b, key = 'name', asc = true) {
  if ((a.kind === 'folder') !== (b.kind === 'folder')) return a.kind === 'folder' ? -1 : 1;
  const direction = asc ? 1 : -1;
  if (key !== 'name') {
    const field = key === 'size' ? 'bytes' : 'modifiedAt';
    const left = a[field], right = b[field];
    const leftValid = typeof left === 'number' && Number.isFinite(left), rightValid = typeof right === 'number' && Number.isFinite(right);
    if (leftValid !== rightValid) return leftValid ? -1 : 1;
    if (leftValid && left !== right) return (left < right ? -1 : 1) * direction;
  }
  return a.name.localeCompare(b.name) * direction;
}
for (const [id, key] of [['sort-files', 'name'], ['sort-size', 'size'], ['sort-time', 'time']]) {
  document.getElementById(id).onclick = () => {
    ascending = fileSortKey === key ? !ascending : true;
    fileSortKey = key;
    updateFileSortHeaders();
    renderFiles();
  };
}
function updateFileSortHeaders() {
  for (const [id, key, label] of [['sort-files', 'name', '文件名'], ['sort-size', 'size', '大小'], ['sort-time', 'time', '修改时间']]) {
    const button = document.getElementById(id), active = fileSortKey === key;
    button.closest('th').setAttribute('aria-sort', active ? (ascending ? 'ascending' : 'descending') : 'none');
    button.querySelector('span').textContent = active ? (ascending ? '↑' : '↓') : '↕';
    button.title = `按${label}${active && ascending ? '降序' : '升序'}排列`;
  }
}
updateFileSortHeaders();
$('#mkdir').onclick = event => window.DengFileTools.showCreateMenu(event.currentTarget);
async function renameRemoteFile(state, path) {
  if (!state?.connected) return;
  const previousName = path.split('/').filter(Boolean).at(-1) || '';
  const name = await ask({ title: '重命名', input: true, value: previousName });
  if (!name || name === previousName) return;
  if (!state.connected || !sessions.has(state.id)) return toast('此会话已断开，请重新连接');
  await post(`/api/sessions/${state.id}/file-action`, { action: 'rename', path, name });
  DengFileBrowser.invalidate(state, path.slice(0, path.lastIndexOf('/')) || '/');
  await navigate(state.cwd, state);
}
$('#rename-file').onclick = safe(() => { const state = current(), entry = selectedEntry(); if (entry) return renameRemoteFile(state, normalizePath(entry.name, state.cwd)); });
$('#delete-file').onclick = safe(async () => { const state = current(), entry = selectedEntry(); if (!state?.connected || !entry) return; const target = normalizePath(entry.name, state.cwd); await DengFileTools.removeFile(state, target, false, entry.kind === 'folder'); });
async function downloadSelected() { const state = current(), entry = selectedEntry(); if (!entry || entry.kind === 'folder') return; const path = normalizePath(entry.name, state.cwd); if (native()) { toast('正在下载…'); const target = await native().Download(state.id, path); if (target) toast('下载完成：' + target); } else { const link = node('a'); link.href = endpoint(`/api/sessions/${state.id}/download?path=${encodeURIComponent(path)}&token=${encodeURIComponent(boot.token)}`); link.download = entry.name; link.click(); } }
$('#download-file').onclick = safe(downloadSelected);

// The backend samples every connected SSH independently. The visible tab only
// reads its cached history; one slow view request cannot block another tab.
let statsTimer = 0, networkTimer = 0, networkPolledSession = '';
const statsRequests = new Set();
const networkRequests = new Set();
function monitorVisible() { return !document.hidden && !window.DengShellWindowHidden; }
function monitorPollDelay(value, interval) { return Number.isFinite(value) ? Math.max(0, Math.min(interval, value)) + 5 : interval; }
async function pollNetwork() {
  const state = current(); if (!state?.connected || networkRequests.has(state) || !monitorVisible()) return;
  clearTimeout(networkTimer); networkTimer = 0; networkRequests.add(state); networkPolledSession = state.id;
  let delay = 1000;
  try {
    const stats = await api(`/api/sessions/${state.id}/network`);
    if (sessions.get(state.id) !== state || !state.connected) return;
    const sampledAt = Date.parse(stats.sampledAt), previous = Date.parse(state.networkStats?.sampledAt);
    rememberNetworkSample(state, stats);
    if (!state.networkStats || Number.isFinite(sampledAt) && (!Number.isFinite(previous) || sampledAt >= previous)) {
      const { history, ...latest } = stats; state.networkStats = latest; state.networkError = stats.sampleError || '';
    }
    delay = Math.max(100, monitorPollDelay(stats.nextSampleInMilliseconds, 1000));
  } catch (error) { if (sessions.get(state.id) === state && state.connected) state.networkError = error.message || String(error); }
  finally {
    networkRequests.delete(state);
    if (current() === state && state.connected && monitorVisible()) {
      renderNetwork(state.stats);
      networkTimer = setTimeout(() => { networkTimer = 0; pollNetwork(); }, delay);
    }
  }
}
async function pollStats() {
  const state = current(); if (!state?.connected || statsRequests.has(state) || !monitorVisible()) return;
  if ((!networkTimer && !networkRequests.has(state)) || networkPolledSession !== state.id) pollNetwork();
  clearTimeout(statsTimer); statsTimer = 0; statsRequests.add(state); const includeProcesses = true;
  let delay = 5000;
  try { const stats = await api(`/api/sessions/${state.id}/stats?processes=${includeProcesses ? 1 : 0}`); if (sessions.get(state.id) !== state || !state.connected) return; rememberProcessSample(state, stats); state.stats = stats; delay = monitorPollDelay(stats.nextSampleInMilliseconds, 5000); if (activeID === state.id) { renderMonitor(stats); $('.system-section').classList.remove('stale'); $('#monitor-state').title = ''; } }
  catch (error) { if (activeID === state.id) { $('#monitor-state').textContent = '读取失败'; $('#monitor-state').hidden = false; $('#monitor-state').title = error.message; $('.system-section').classList.add('stale'); } }
  finally { statsRequests.delete(state); if (current() === state && state.connected && monitorVisible()) { clearTimeout(statsTimer); statsTimer = setTimeout(() => { statsTimer = 0; pollStats(); }, delay); } }
}
function meter(selector, percent, detail, valid = true) { const el = $(selector); el.querySelector('i').style.width = `${valid ? Math.max(0, Math.min(100, percent)) : 0}%`; el.querySelector('b').textContent = valid ? `${percent.toFixed(1)}%` : '—'; el.querySelector('em').textContent = detail; }
function renderMonitor(stats) {
  if (window.DengSystemIdentity) window.DengSystemIdentity.render(stats?.os);
  else { $('#system-os').textContent = stats?.os || '—'; $('#system-os').title = stats?.os || ''; }
  $('#cpu-model').textContent = stats?.cpuModel || '—'; $('#cpu-model').title = stats?.cpuModel || ''; $('#cpu-cores').textContent = stats ? `${stats.cores} vCPU` : '—';
  $('#uptime').textContent = stats ? `${Math.floor(stats.uptime / 86400)} 天 ${Math.floor(stats.uptime % 86400 / 3600)} 时` : '—'; $('#system-load').textContent = stats?.load || '—';
  meter('#cpu-meter', stats?.cpu || 0, stats ? `${stats.cores} 核心` : '—', !!stats?.sampleReady);
  meter('#memory-meter', stats?.memoryTotal ? stats.memoryUsed / stats.memoryTotal * 100 : 0, stats ? `${prettySize(stats.memoryUsed)} / ${prettySize(stats.memoryTotal)}` : '—', !!stats);
  meter('#swap-meter', stats?.swapTotal ? stats.swapUsed / stats.swapTotal * 100 : 0, stats ? `${prettySize(stats.swapUsed)} / ${prettySize(stats.swapTotal)}` : '—', !!stats);
  renderProcesses(stats);
  const disks = stats?.disks || []; const root = disks.find(disk => disk.path === '/') || disks[0];
  $('#disk-used').textContent = root ? compactDiskSize(root.used) : '—'; $('#disk-total').textContent = root ? compactDiskSize(root.total) : '—'; const availablePercent = root?.total ? Math.max(0, Math.min(100, root.available / root.total * 100)) : 0; $('#disk-percent').textContent = root ? `${availablePercent.toFixed(1)}%` : '—'; $('.disk-summary').title = root?.path || '分区容量'; $('.disk-total-bar i').style.width = `${availablePercent}%`;
  $('#disk-list').replaceChildren(...disks.map(disk => { const row = node('div', 'disk-row'); row.append(node('span', '', disk.path), node('span', '', `${prettySize(disk.available)} / ${prettySize(disk.total)}`)); row.title = disk.path; return row; }));
  $('#disk-read').textContent = stats?.sampleReady ? prettySize(stats.diskRead) + '/s' : '—'; $('#disk-write').textContent = stats?.sampleReady ? prettySize(stats.diskWrite) + '/s' : '—';
  renderNetwork(stats);
  renderServerAddresses(stats);
  $('#system-load').title = '系统负载约每 5 秒刷新';
  if (current()?.connected && networkPolledSession !== current().id) queueMicrotask(pollNetwork);
}
function drawCharts(samples) {
  renderTrafficChart($('#network-chart'),samples);
}
let latencyBusy = false;
async function pollLatency() {
  const state = current(); if (!state?.connected || latencyBusy) return;
  latencyBusy = true;
  try {
    const sample = await api(`/api/sessions/${state.id}/latency`); if (!sessions.has(state.id)) return;
    state.latency = sample;
  } catch { state.latency = null; }
  finally { latencyBusy = false; if (current() === state) renderLatency(); }
}
function renderLatency() { renderLatencyDetails(); }
setInterval(pollLatency, 1000);
setInterval(() => {
  if (!monitorVisible()) { clearTimeout(networkTimer); clearTimeout(statsTimer); networkTimer = statsTimer = 0; return; }
  if (!current()?.connected) return;
  if (!networkRequests.has(current()) && !networkTimer) pollNetwork();
  if (!statsRequests.has(current()) && !statsTimer) pollStats();
  renderNetwork(current().stats);
}, 1000);
document.addEventListener('visibilitychange', () => { if (!document.hidden) { pollStats(); pollNetwork(); } });

// Connection groups and credentials are persisted by Go, never in browser storage.
let drawerTrigger;
function setDrawer(open) { if (open) drawerTrigger = document.activeElement; $('#connections-drawer').hidden = !open; $('#drawer-backdrop').hidden = !open; if (open) { renderConnections(); $('#connection-search').focus(); safe(refreshServerManagerHistory)(); } else { closeServerGroupMenu(); drawerTrigger?.focus(); } }
for (const id of ['connection-button', 'add-session', 'welcome-connect']) $(`#${id}`).onclick = () => setDrawer(true);
$('#close-connections').onclick = () => setDrawer(false); $('#drawer-backdrop').onclick = () => setDrawer(false);
$('#connection-search').oninput = renderConnections;
function renderConnections() { renderServerManager(); }
function showConnectionForm(profile = null, groupID = '') {
  const form = $('#connection-form'); form.reset(); form.elements.id.value = profile?.id || '';
  renderKeyChoices(); renderProxyChoices(); form.elements.proxyId.value = profile?.proxyId || '';
  for (const key of ['name', 'host', 'user', 'port', 'auth', 'keyPath', 'keyId']) if (profile?.[key] != null) form.elements[key].value = profile[key];
  form.elements.proxyType.value = profile?.proxy?.type || 'direct';
  for (const [field, key] of [['proxyHost', 'host'], ['proxyPort', 'port'], ['proxyUser', 'user']]) form.elements[field].value = profile?.proxy?.[key] || '';
  form.elements.proxyPassword.placeholder = profile?.proxy?.hasPassword ? '已保存，留空保留' : '代理密码（可选）';
  $('#proxy-settings').open = !!profile?.proxyId || form.elements.proxyType.value !== 'direct'; updateProxyFields();
  chooseConnectionGroup(profile, groupID);
  form.elements.remember.checked = !!profile?.hasSecret; $('#saved-secret-note').hidden = !profile?.hasSecret; $('#reset-host-key').hidden = !profile;
  $('#connection-form-title').textContent = profile ? '编辑连接' : '新建连接'; $('#save-connection').textContent = profile ? '保存更改' : '保存并连接'; updateAuthFields(); $('#connection-dialog').showModal();
}
function updateAuthFields() {
  const form = $('#connection-form'), auth = form.elements.auth.value, key = managedKeys.find(key => key.id === form.elements.keyId.value);
  const needsSecret = auth === 'password' || (auth === 'key' && (!key || (key.encrypted && !key.hasPassphrase)));
  $('#key-library-field').hidden = auth !== 'key'; $('#key-path-field').hidden = auth !== 'key' || !!form.elements.keyId.value;
  $('#secret-field').hidden = !needsSecret; $('#remember-secret-field').hidden = !needsSecret;
  $('#saved-secret-note').hidden = !needsSecret || !profiles.find(profile => profile.id === form.elements.id.value)?.hasSecret;
  $('#managed-key-secret-note').hidden = auth !== 'key' || !key?.hasPassphrase;
  $('#secret-label').textContent = auth === 'key' ? '私钥口令' : '密码'; $('#choose-key').hidden = !native();
}
$('#connection-form').elements.auth.onchange = updateAuthFields;
$('#new-connection').onclick = () => showConnectionForm(); $('#cancel-connection').onclick = () => $('#connection-dialog').close();
$('#choose-key').onclick = safe(async () => { const path = await native().ChooseKey(); if (path) $('#connection-form').elements.keyPath.value = path; });
$('#connection-form').onsubmit = safe(async event => {
  event.preventDefault(); const form = event.target, values = Object.fromEntries(new FormData(form)), isNew = !values.id;
  const key = managedKeys.find(key => key.id === values.keyId), needsSecret = values.auth === 'password' || (values.auth === 'key' && (!key || (key.encrypted && !key.hasPassphrase)));
  const secret = needsSecret ? values.secret : '', remember = needsSecret && form.elements.remember.checked;
  const previous = profiles.find(profile => profile.id === values.id);
  const proxy = { type: values.proxyId ? 'direct' : values.proxyType, host: values.proxyHost, port: Number(values.proxyPort), user: values.proxyUser, password: values.proxyPassword, clearPassword: form.elements.clearProxyPassword.checked };
  for (const name of ['proxyType', 'proxyHost', 'proxyPort', 'proxyUser', 'proxyPassword', 'clearProxyPassword']) delete values[name];
  const saved = await post('/api/profiles', { ...values, port: Number(values.port), secret: remember ? secret : '', clearSecret: !remember, proxy });
  credentials.delete(saved.id);
  if (secret) credentials.set(saved.id, secret); else if (!remember) credentials.delete(saved.id);
  $('#connection-dialog').close(); await loadProfiles(); if (isNew) await connect(saved.id); else { renderSessionInfo(); toast('连接配置已保存，下次连接时生效'); }
});
$('#reset-host-key').onclick = safe(async () => { const id = $('#connection-form').elements.id.value; if (!id || !await ask({ title: '重置主机指纹？', description: '仅在确认服务器重装或主机密钥正常更换后操作。清除后，下次连接仍需核实并确认新指纹。', confirm: '重置指纹' })) return; await post(`/api/profiles/${id}/reset-key`, {}); toast('已清除记录，下次连接需要确认指纹'); });
$('#new-group').onclick = () => openServerGroupEditor();
document.addEventListener('keydown', event => { if (event.key === 'Escape' && !document.querySelector('dialog[open]')) setDrawer(false); if (event.key === 'Tab' && !$('#connections-drawer').hidden && !document.querySelector('dialog[open]')) { const controls = [...$('#connections-drawer').querySelectorAll('button,input')].filter(el => !el.disabled && el.getClientRects().length); if (event.shiftKey && document.activeElement === controls[0]) { event.preventDefault(); controls.at(-1).focus(); } else if (!event.shiftKey && document.activeElement === controls.at(-1)) { event.preventDefault(); controls[0].focus(); } } });

// Stream files through SFTP. Completion reflects remote writes, not just browser upload progress.
function showPane(pane) { $$('.file-tab').forEach(tab => tab.classList.toggle('active', tab.dataset.pane === pane)); $('#files-view').hidden = pane !== 'files'; $('#transfers-view').hidden = pane !== 'transfers'; $('#commands-view').hidden = pane !== 'commands'; $('#common-apps-view').hidden = pane !== 'common-apps'; window.DengCommonApps?.render(); $('.follow-label').hidden = pane !== 'files'; $('.files-tip').hidden = pane !== 'files'; }
$$('.file-tab').forEach(tab => { tab.onclick = () => { showPane(tab.dataset.pane); save('dengshell.workspace', { ...readSaved('dengshell.workspace', {}), pane: tab.dataset.pane }); }; });
$('#choose-files').onclick = event => { const menu = $('#upload-menu'), rect = event.currentTarget.getBoundingClientRect(); menu.hidden = !menu.hidden; menu.style.left = `${Math.min(rect.left / effectiveScale, logicalWidth() - 155)}px`; menu.style.top = `${Math.min(rect.bottom / effectiveScale + 5, logicalHeight() - 90)}px`; };
document.addEventListener('click', event => { if (!event.target.closest('#upload-menu, #choose-files')) $('#upload-menu').hidden = true; });
$('#upload-files-option').onclick = safe(async () => { $('#upload-menu').hidden = true; if (native()) { const state = current(); const directory = state?.cwd; const paths = await native().ChooseUploads(); if (paths?.length) await uploadNative(paths, state, directory); } else $('#file-picker').click(); });
$('#upload-folder-option').onclick = safe(async () => { $('#upload-menu').hidden = true; if (native()) { const state = current(); const directory = state?.cwd; const path = await native().ChooseFolder(); if (path) await uploadNative([path], state, directory); } else $('#folder-picker').click(); });
for (const id of ['file-picker', 'folder-picker']) $(`#${id}`).onchange = event => { queueFiles([...event.target.files].map(file => ({ file, relativePath: file.webkitRelativePath || file.name }))); event.target.value = ''; };
let activeUploads = 0;
function queueFiles(items, state = current(), destination = state?.cwd) {
  if (!state?.connected) return toast('请先连接服务器');
  if (!items.length) return toast('没有可上传的文件');
  for (const { file, relativePath } of items) { const id = crypto.randomUUID(); localTasks.set(id, { id, sessionId: state.id, name: file.name, target: normalizePath(relativePath, destination), total: file.size, done: 0, status: 'queued', file }); }
  showPane('transfers'); renderTransfers(); pumpUploads();
}
function pumpUploads() { for (const task of localTasks.values()) if (task.file && !task.xhr && task.status === 'queued' && activeUploads < 4) { activeUploads++; task.status = 'uploading'; uploadBrowser(task).finally(() => { activeUploads--; pumpUploads(); renderTransfers(); }); } }
async function uploadBrowser(task) {
  try {
    if (!sessions.get(task.sessionId)?.connected) throw new Error('连接已断开，请重新连接后再次选择文件');
    await new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest(); task.xhr = xhr;
      xhr.open('POST', endpoint(`/api/sessions/${task.sessionId}/upload?path=${encodeURIComponent(task.target)}&task=${encodeURIComponent(task.id)}&overwrite=${task.overwrite ? '1' : '0'}`)); xhr.setRequestHeader('X-CloudShell-Token', boot.token); xhr.setRequestHeader('Content-Type', 'application/octet-stream');
      xhr.onload = () => { if (xhr.status >= 200 && xhr.status < 300) resolve(); else { let message = '上传失败'; try { message = JSON.parse(xhr.responseText).error; } catch {} reject(new Error(message)); } };
      xhr.onerror = () => reject(new Error('连接中断，文件未上传完成')); xhr.onabort = () => reject(new Error('已取消')); xhr.send(task.file);
    }); task.status = 'done'; task.done = task.total; refreshUploaded(task);
  } catch (error) { task.status = error.message === '已取消' ? 'cancelled' : 'failed'; task.error = error.message; }
  finally { task.xhr = null; renderTransfers(); }
}
async function uploadNative(paths, state = current(), directory = state?.cwd, overwrite = false) {
  if (!state?.connected) return toast('请先连接服务器');
  const result = await post(`/api/sessions/${state.id}/upload-local`, { paths, directory, overwrite });
  showPane('transfers'); await pollTransfers(); if (!result.ids.length) { toast('目录已创建'); await navigate(state.cwd, state); }
}
window.cloudshellNativeDrop = safe(async ({ x, y, paths }) => {
  const rect = $('#files-panel').getBoundingClientRect(); if (x < rect.left || x > rect.right || y < rect.top || y > rect.bottom) return toast('请将文件拖入下方文件区域');
  if (paths?.length) await uploadNative(paths);
});
function refreshUploaded(task) { const state = sessions.get(task.sessionId); if (!state?.connected || task.refreshed) return; task.refreshed = true; if (task.target.startsWith(state.cwd === '/' ? '/' : state.cwd + '/')) navigate(state.cwd, state).catch(() => {}); }
let transfersBusy = false;
async function pollTransfers() {
  if (transfersBusy) return; transfersBusy = true;
  try { const tasks = await api('/api/transfers'); for (const incoming of tasks) { let task = localTasks.get(incoming.id); if (task) { if (!['done', 'failed', 'cancelled'].includes(task.status) || incoming.status !== 'uploading') Object.assign(task, incoming); } else { task = incoming; localTasks.set(task.id, task); } if (task.status === 'done') refreshUploaded(task); } renderTransfers(); }
  catch {} finally { transfersBusy = false; }
}
setInterval(pollTransfers, 1000);
async function cancelTask(task) {
  if (task.status === 'queued') { if (task.xhr || !task.file) { task.xhr?.abort(); await remove(`/api/transfers/${task.id}`); } task.status = 'cancelled'; }
  else if (task.status === 'uploading') { task.xhr?.abort(); await remove(`/api/transfers/${task.id}`); }
  else { await remove(`/api/transfers/${task.id}`); localTasks.delete(task.id); }
  renderTransfers();
}
async function retryTask(task) {
  const overwrite = task.error?.includes('已存在'); if (overwrite && !await ask({ title: `覆盖 ${task.name}？`, description: `将替换远程文件 ${task.target}。`, confirm: '覆盖并上传' })) return;
  if (!task.file) { await post(`/api/transfers/${task.id}/retry`, { overwrite }); await remove(`/api/transfers/${task.id}`); localTasks.delete(task.id); await pollTransfers(); return; }
  await remove(`/api/transfers/${task.id}`); localTasks.delete(task.id); const next = { ...task, id: crypto.randomUUID(), done: 0, error: '', status: 'queued', overwrite, refreshed: false }; localTasks.set(next.id, next); pumpUploads(); renderTransfers();
}
function renderTransfers() {
  $('#transfer-count').textContent = localTasks.size; $('#transfer-empty').hidden = localTasks.size > 0;
  const label = { queued: '等待上传', uploading: '上传中', done: '已完成', failed: '失败', cancelled: '已取消' };
  $('#transfer-list').replaceChildren(...[...localTasks.values()].map(task => {
    const row = node('div', 'transfer-row'), info = node('div', 'transfer-info'); const name = node('strong', '', task.name), detail = node('small', '', task.error || task.target); detail.title = task.error || task.target; info.append(name, detail);
    const progress = node('progress', 'transfer-progress'); progress.max = task.total || 1; progress.value = task.done || 0; progress.setAttribute('aria-label', task.name + ' 上传进度'); info.append(progress);
    const percent = task.total > 0 ? Math.min(100, Math.round(task.done / task.total * 100)) : 0;
    const status = node('span', `transfer-state ${task.status}`, `${label[task.status] || task.status}${task.status === 'uploading' ? ' ' + percent + '%' : ''}`);
    const cancel = node('button', 'icon-button'); cancel.title = ['queued', 'uploading'].includes(task.status) ? '取消上传' : '移除记录'; cancel.setAttribute('aria-label', cancel.title); cancel.append(icon('close')); cancel.onclick = safe(() => cancelTask(task));
    row.append(icon('file'), info, node('span', 'transfer-size', prettySize(task.done) + ' / ' + prettySize(task.total)), status);
    if (['failed', 'cancelled'].includes(task.status) && (task.file || task.retryable)) { const retry = node('button', 'transfer-retry', task.error?.includes('已存在') ? '覆盖' : '重试'); retry.onclick = safe(() => retryTask(task)); row.append(retry); }
    row.append(cancel); return row;
  }));
}
const filesPanel = $('#files-panel'); let dragDepth = 0;
const isFileDrag = event => [...(event.dataTransfer?.types || [])].includes('Files');
window.addEventListener('dragover', event => { if (isFileDrag(event)) { event.preventDefault(); event.dataTransfer.dropEffect = filesPanel.contains(event.target) && current()?.connected ? 'copy' : 'none'; } });
window.addEventListener('drop', event => { if (isFileDrag(event)) event.preventDefault(); dragDepth = 0; $('#drop-overlay').hidden = true; });
filesPanel.addEventListener('dragenter', event => { if (!isFileDrag(event) || !current()?.connected || native()) return; event.preventDefault(); dragDepth++; $('#drop-overlay').hidden = false; });
filesPanel.addEventListener('dragleave', () => { dragDepth = Math.max(0, dragDepth - 1); if (!dragDepth) $('#drop-overlay').hidden = true; });
async function readEntry(entry, prefix = '') {
  if (entry.isFile) { const file = await new Promise((resolve, reject) => entry.file(resolve, reject)); return [{ file, relativePath: prefix + file.name }]; }
  if (!entry.isDirectory) return []; const reader = entry.createReader(), result = [];
  while (true) { const batch = await new Promise((resolve, reject) => reader.readEntries(resolve, reject)); if (!batch.length) break; for (const child of batch) result.push(...await readEntry(child, prefix + entry.name + '/')); }
  return result;
}
filesPanel.addEventListener('drop', safe(async event => {
  if (!isFileDrag(event) || native()) return; event.preventDefault(); const state = current(), directory = state?.cwd, files = [...event.dataTransfer.files], entries = [...event.dataTransfer.items].filter(item => item.kind === 'file').map(item => item.webkitGetAsEntry?.());
  dragDepth = 0; $('#drop-overlay').hidden = true;
  if (entries.length && entries.every(Boolean)) { const items = []; for (const entry of entries) items.push(...await readEntry(entry)); queueFiles(items, state, directory); }
  else queueFiles(files.map(file => ({ file, relativePath: file.name })), state, directory);
}));

const savedLayout = readSaved('cloudshell.layout', {});
let layout = savedLayout && typeof savedLayout === 'object' ? savedLayout : {};
// Adopt the roomier terminal once; subsequent manual pane sizes stay intact.
if (layout.version !== 2) { delete layout.files; layout.version = 2; save('cloudshell.layout', layout); }
function clampLayouts() {
  const maxWidth = Math.min(460, Math.max(225, logicalWidth() - 400));
  const maxHeight = Math.max(100, $('#workspace').clientHeight - 160 - $('#files-splitter').clientHeight);
  if (Number.isFinite(layout.sidebar)) {
    layout.sidebar = Math.max(Math.min(240, maxWidth), Math.min(maxWidth, layout.sidebar));
    document.documentElement.style.setProperty('--sidebar-width', `${layout.sidebar}px`);
  }
  if (Number.isFinite(layout.files)) {
    layout.files = Math.max(100, Math.min(maxHeight, layout.files));
    document.documentElement.style.setProperty('--files-height', `${layout.files}px`);
  }
  $('#sidebar-splitter').setAttribute('aria-valuenow', Math.round($('.sidebar').offsetWidth));
  $('#sidebar-splitter').setAttribute('aria-valuemax', maxWidth);
  $('#files-splitter').setAttribute('aria-valuenow', Math.round($('#files-panel').clientHeight));
  $('#files-splitter').setAttribute('aria-valuemax', maxHeight);
}
function resizeSidebar(width) { layout.sidebar = width; clampLayouts(); }
function resizeFiles(y) { const rect = $('#workspace').getBoundingClientRect(); layout.files = (appearance.filesPosition === 'top' ? y - rect.top : rect.bottom - y) / effectiveScale; clampLayouts(); }
function bindSplitter(selector, handler, keyboardHandler) {
  const splitter = $(selector);
  let dragging = false;
  splitter.addEventListener('pointerdown', event => {
    if (event.button !== 0) return;
    dragging = true;
    splitter.setPointerCapture(event.pointerId);
    document.body.style.userSelect = 'none';
    document.body.style.cursor = getComputedStyle(splitter).cursor;
  });
  splitter.addEventListener('pointermove', event => { if (dragging) handler(event); });
  const stop = () => { dragging = false; document.body.style.userSelect = ''; document.body.style.cursor = ''; save('cloudshell.layout', layout); };
  splitter.addEventListener('pointerup', stop);
  splitter.addEventListener('pointercancel', stop);
  splitter.addEventListener('lostpointercapture', stop);
  splitter.addEventListener('keydown', event => {
    if (!['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown'].includes(event.key)) return;
    event.preventDefault(); keyboardHandler(event.key); save('cloudshell.layout', layout);
  });
}
bindSplitter('#sidebar-splitter', event => { const rect = $('.sidebar').getBoundingClientRect(); resizeSidebar((appearance.monitorSide === 'right' ? rect.right - event.clientX : event.clientX - rect.left) / effectiveScale); }, key => { if (key === 'ArrowLeft' || key === 'ArrowRight') resizeSidebar($('.sidebar').offsetWidth + (key === 'ArrowLeft' ? -12 : 12) * (appearance.monitorSide === 'right' ? -1 : 1)); });
bindSplitter('#files-splitter', event => resizeFiles(event.clientY), key => { if (key === 'ArrowUp' || key === 'ArrowDown') { layout.files = $('#files-panel').clientHeight + (key === 'ArrowUp' ? 20 : -20) * (appearance.filesPosition === 'top' ? -1 : 1); clampLayouts(); } });
$('#reset-layout').onclick = () => {
  layout = { version: 2 };
  save('cloudshell.layout', layout);
  document.documentElement.style.removeProperty('--sidebar-width');
  document.documentElement.style.removeProperty('--files-height');
  document.documentElement.style.removeProperty('--terminal-font');
  terminalFont = 14; changeFont(0);
  clampLayouts();
  chooseAppearance({ monitorSide: 'left', filesPosition: 'bottom' }).catch(error => toast(error.message));
  toast('已恢复默认布局与终端字号');
};
window.addEventListener('resize', () => {
  clampLayouts();
  requestAnimationFrame(() => {
    fitActive();
  });
});

initializeWorkspaceTools(); initializeAppearance(); initializeRefinements(); renderSessionInfo(); renderFiles(); renderTransfers(); applyUIScale();
let windowsDropReady = false;
function initializeWindowsDrop() {
  if (!windowsDropReady && window.chrome?.webview?.postMessageWithAdditionalObjects && window.runtime?.OnFileDrop) {
    // Register WebView2's file-path resolver. Go handles the resolved drop once.
    window.runtime.OnFileDrop(() => {}, false);
    windowsDropReady = true;
  }
}
async function initializeConfig() {
  try { if (desktopPage) await waitForDesktop(); await initializeWindowControls(); await initializeQuitConfirmation(); window.DengSessionWindows.prepareRestore(); await loadProfiles(); await window.DengSessionWindows.restore(); initializeWindowsDrop(); syncDesktopTheme(); $('#connection-button').classList.remove('failed'); $('#connection-button').title = '打开服务器管理'; window.runtime?.EventsEmit('cloudshell:ready'); }
  catch (error) { $('#connection-button').classList.add('failed'); $('#connection-button').title = error.message + ' · 点击重试'; toast(error.message); }
}
$('#connection-button').onclick = () => $('#connection-button').classList.contains('failed') ? initializeConfig() : setDrawer(true);
initializeConfig();
document.fonts.ready.then(fitActive);
window.addEventListener('pagehide', () => { for (const state of sessions.values()) state.ws?.close(); });
for (const pane of $$('.sidebar, .file-content, .transfers-view, .connections-drawer, .commands-content')) {
  let idleTimer;
  pane.addEventListener('scroll', () => { pane.classList.add('is-scrolling'); clearTimeout(idleTimer); idleTimer = setTimeout(() => pane.classList.remove('is-scrolling'), 800); }, { passive: true });
}
