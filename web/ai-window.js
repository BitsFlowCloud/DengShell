(() => {
  'use strict';
  window.DENG_AI_CHILD = true;
  const boot = window.CLOUDSHELL || {};
  const id = boot.aiWindowId || new URLSearchParams(location.search).get('aiWindow');
  const token = boot.token || new URLSearchParams(location.hash.slice(1)).get('token') || '';
  const nativePage = location.protocol === 'wails:' || location.hostname === 'wails.localhost';
  const desktop = () => window.go?.main?.Desktop;
  const base = '/api/ai/windows/' + encodeURIComponent(id || '');
  let after = 0, closed = false, timer, outgoing = Promise.resolve(), listener, closedListener, latest, readyPromise;
  const pending = new Map();
  async function api(path, method = 'GET', body) {
    if (nativePage) {
      const until = Date.now() + 6000;
      while (!desktop()?.Request) {if (Date.now() > until) throw new Error('桌面接口未就绪'); await new Promise(resolve => setTimeout(resolve, 30));}
    }
    if (desktop()?.Request) {
      try {return JSON.parse(await desktop().Request(method, path, body === undefined ? '' : JSON.stringify(body)));}
      catch (error) {throw new Error(error?.message || String(error));}
    }
    const response = await fetch((boot.base || '') + path, {method, headers: {'Content-Type': 'application/json', 'X-CloudShell-Token': token}, ...(body === undefined ? {} : {body: JSON.stringify(body)})});
    const data = await response.json(); if (!response.ok) throw new Error(data.error || 'AI 窗口连接失败'); return data;
  }
  function fail(reason = '原工作区已关闭或连接中断，AI 已停止。') {
    if (closed) return;
    closed = true; latest = null; clearTimeout(timer);
    for (const value of pending.values()) {clearTimeout(value.timer); value.reject(new Error(reason));} pending.clear();
    if (closedListener) closedListener(reason);
    void api(base, 'DELETE').catch(() => {});
  }
  let activityState = boot.securityLock || {}, activityBusy = false, lastActivitySent = 0;
  async function activity(event) {
    if (!event.isTrusted || closed || document.hidden || window.DengShellWindowHidden) return;
    const seconds = Number(activityState.idleSeconds) || 0;
    const interval = seconds > 0 ? Math.min(1000, Math.max(100, seconds * 250)) : 1000;
    if (activityBusy || Date.now() - lastActivitySent < interval) return;
    lastActivitySent = Date.now(); activityBusy = true;
    try {
      // The policy can change in the owner after this child was opened. Real
      // input is reported even if the startup policy had idle locking off.
      // Relay polls, model progress and programmatic DOM events never call here.
      const next = await api('/api/security-lock/activity', 'POST', {});
      if (closed) return;
      if (next?.locked || next?.code === 'DENGSHELL_LOCKED') {fail('工作区已锁定，AI 已停止。'); return;}
      if (typeof next?.enabled === 'boolean') activityState = next;
    } catch {fail('无法确认工作区锁定状态，AI 已停止。');}
    finally {activityBusy = false;}
  }
  for (const name of ['pointerdown', 'keydown', 'wheel', 'touchstart']) window.addEventListener(name, activity, {capture: true, passive: true});
  function post(payload) {
    outgoing = outgoing.then(() => {if (closed) throw new Error('AI 窗口已关闭'); return api(base + '/messages', 'POST', {side: 'assistant', payload});});
    outgoing.catch(() => fail()); return outgoing;
  }
  function request(action, body) {
    if (closed) return Promise.reject(new Error('AI 窗口已关闭'));
    if (!['hello', 'get_settings', 'save_settings', 'get_models', 'get_original', 'set_enabled', 'send', 'stop', 'clear'].includes(action)) return Promise.reject(new Error('不支持的 AI 操作'));
    const requestID = crypto.randomUUID();
    const promise = new Promise((resolve, reject) => {
      const timeout = setTimeout(() => {pending.delete(requestID); reject(new Error('AI 窗口请求超时'));}, action === 'get_models' ? 610000 : 30000);
      pending.set(requestID, {resolve, reject, timer: timeout});
    });
    void post({type: 'request', id: requestID, action, body}).catch(() => {});
    return promise;
  }
  function receive(payload) {
    if (payload?.type === 'state') {latest = payload; listener?.(payload); return;}
    if (payload?.type !== 'result' || typeof payload.id !== 'string') throw new Error('无效的原窗口响应');
    const entry = pending.get(payload.id); if (!entry) return;
    pending.delete(payload.id); clearTimeout(entry.timer);
    if (payload.error) entry.reject(new Error(payload.error)); else entry.resolve(payload.value);
  }
  async function poll() {
    if (closed) return;
    try {
      const response = await api(base + '?side=assistant&after=' + after);
      if (closed) return;
      if (response.closed) {fail(); return;}
      for (const event of response.events || []) {
        if (!Number.isSafeInteger(event.seq) || event.seq <= after) throw new Error('无效的窗口消息顺序');
        receive(event.payload); after = event.seq;
      }
      timer = setTimeout(() => void poll(), 250);
    } catch {fail();}
  }
  function ready() {
    if (!readyPromise) readyPromise = (async () => {if (!/^[a-f0-9]{48}$/.test(id || '')) {fail('AI 窗口标识无效，请从主窗口重新打开。'); throw new Error('AI 窗口标识无效');} void poll(); return request('hello');})();
    return readyPromise;
  }
  function close() {
    fail('AI 窗口已关闭。');
    if (desktop()?.WindowAction) void desktop().WindowAction('close').catch(() => {});
    else window.close();
  }
  window.addEventListener('pagehide', () => {
    if (!desktop()) {try {void fetch((boot.base || '') + base, {method: 'DELETE', headers: {'X-CloudShell-Token': token}, keepalive: true});} catch {}}
    fail();
  });
  window.DengAIWindowTransport = {
    ready, request, close,
    hasNativeWindowControls: () => !!window.DengAIWindowTitlebar?.ready,
    subscribe(fn) {listener = fn; if (latest) fn(latest);},
    onClosed(fn) {closedListener = fn; if (closed) fn();},
    canPin: () => !!desktop()?.SetAIWindowAlwaysOnTop,
    async pin(value) {if (!desktop()?.SetAIWindowAlwaysOnTop) throw new Error('窗口置顶仅在桌面客户端中可用'); return !!(await desktop().SetAIWindowAlwaysOnTop(!!value));},
  };
})();
