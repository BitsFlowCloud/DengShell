(() => {
  'use strict';
  let channel = null, opening = null, publishTimer = null, launchPopup = null, lifecycle = 0;
  const actions = new Set(['get_settings', 'save_settings', 'get_models', 'get_original', 'set_enabled', 'send', 'stop', 'clear']);
  const path = id => '/api/ai/windows/' + encodeURIComponent(id);
  const desktop = () => window.go?.main?.Desktop;
  function dispose(reason = '') {
    lifecycle += 1; const previous = channel; channel = null; clearTimeout(publishTimer); publishTimer = null;
    try {if (launchPopup && !launchPopup.closed) launchPopup.close();} catch {} launchPopup = null;
    window.DengShellAIController?.close();
    if (!previous) return;
    clearTimeout(previous.timer);
    void api(path(previous.id), {method: 'DELETE'}).catch(() => {});
    if (previous.popup && !previous.popup.closed) {try {previous.popup.close();} catch {}}
    if (reason && !window.DengSecurityLock?.isLocked()) toast(reason);
  }
  function postMessage(record, payload) {
    record.outgoing = record.outgoing.then(async () => {
      if (channel !== record) return;
      await post(path(record.id) + '/messages', {side: 'owner', payload});
    }).catch(() => {if (channel === record) dispose('AI 窗口连接中断，已停止自动操作。');});
    return record.outgoing;
  }
  function prepareState() {
    const state = window.DengShellAIController.snapshot();
    state.events = state.events.slice(-100);
    const size = () => new TextEncoder().encode(JSON.stringify(state)).length;
    while (state.events.length && size() > 240000) state.events.shift();
    if (size() > 250000) state.settings = null;
    return state;
  }
  function publish() {
    if (!channel || publishTimer) return;
    const record = channel;
    publishTimer = setTimeout(() => {
      publishTimer = null;
      if (channel !== record) return;
      const state = prepareState(), serialized = JSON.stringify(state);
      if (serialized === record.lastState) return;
      record.lastState = serialized; void postMessage(record, state);
    }, 80);
  }
  function receive(record, payload) {
    if (!payload || payload.type !== 'request' || typeof payload.id !== 'string' || !/^[A-Za-z0-9_-]{1,80}$/.test(payload.id)) throw new Error('无效的 AI 窗口消息');
    const reply = (value, error) => {if (channel === record) void postMessage(record, {type: 'result', id: payload.id, value, error});};
    if (payload.action === 'hello') {record.lastState = ''; publish(); reply({ok: true}); return;}
    if (!actions.has(payload.action)) {reply(undefined, '不支持的 AI 窗口操作'); return;}
    if (payload.action === 'set_enabled' && typeof payload.body?.enabled !== 'boolean') {reply(undefined, '无效的启用状态'); return;}
    // Do not await long model/settings requests in the polling loop. Stop and
    // lock must remain responsive while a remote service is unavailable.
    Promise.resolve().then(() => {
      if (channel !== record) throw new Error('AI 窗口已关闭');
      return window.DengShellAIController.dispatch(payload.action, payload.body);
    }).then(value => reply(value), error => reply(undefined, error?.message || String(error))).finally(publish);
  }
  async function poll(record) {
    if (channel !== record) return;
    try {
      if (record.popup?.closed) {dispose(); return;}
      const response = await api(path(record.id) + '?side=owner&after=' + record.after);
      if (channel !== record) return;
      if (response.closed) {dispose(response.reason ? 'AI 窗口已断开，自动操作已停止。' : ''); return;}
      for (const event of response.events || []) {
        if (!Number.isSafeInteger(event.seq) || event.seq <= record.after) throw new Error('无效的 AI 窗口消息顺序');
        receive(record, event.payload); record.after = event.seq;
      }
      record.timer = setTimeout(() => void poll(record), 250);
    } catch {if (channel === record) dispose('AI 窗口连接中断，已停止自动操作。');}
  }
  function open() {
    if (window.DengSecurityLock?.isLocked()) return Promise.resolve();
    if (opening) {channel?.popup?.focus(); return opening;}
    if (channel) {
      if (desktop()?.OpenAIWindow) return desktop().OpenAIWindow(channel.id).catch(error => {dispose(); toast(error?.message || String(error));});
      if (channel.popup && !channel.popup.closed) {channel.popup.focus(); return Promise.resolve();}
      dispose();
    }
    // Browser popup creation must occur in this synchronous click stack, before
    // the broker request. Navigation follows once its unpredictable ID exists.
    const nativeWindow = !!desktop()?.OpenAIWindow;
    const popup = nativeWindow ? null : window.open('about:blank', 'DengShellAI-' + crypto.randomUUID(), 'popup,width=450,height=760,resizable=yes,scrollbars=no');
    if (!nativeWindow && !popup) {toast('浏览器阻止了 AI 弹出窗口，请允许此站点弹出窗口后重试。'); return Promise.resolve();}
    if (popup) {try {popup.opener = null; popup.document.title = 'DengShell AI'; popup.document.body.textContent = '正在打开 AI 助手…';} catch {}}
    launchPopup = popup; const started = lifecycle;
    opening = (async () => {
      let record;
      try {
        const response = await post('/api/ai/windows', {});
        if (!/^[a-f0-9]{48}$/.test(response.id)) throw new Error('AI 窗口标识无效');
        if (started !== lifecycle) {void api(path(response.id), {method: 'DELETE'}).catch(() => {}); return;}
        launchPopup = null; record = {id: response.id, popup, after: 0, outgoing: Promise.resolve(), lastState: '', timer: null}; channel = record;
        if (window.DengSecurityLock?.isLocked() || popup?.closed) {dispose(); return;}
        void poll(record);
        await window.DengShellAIController.open();
        if (channel !== record) return;
        publish();
        if (nativeWindow) await desktop().OpenAIWindow(record.id);
        else {const url = new URL('ai-window.html', location.href); url.search = '?aiWindow=' + record.id; url.hash = 'token=' + encodeURIComponent(boot.token); popup.location.replace(url.href); popup.focus();}
      } catch (error) {if (!record || channel === record) {dispose(); try {popup?.close();} catch {} toast(error?.message || String(error));}}
      finally {opening = null;}
    })();
    return opening;
  }
  window.addEventListener('pagehide', () => {
    if (channel && !desktop()) {try {void fetch(endpoint(path(channel.id)), {method: 'DELETE', headers: {'X-CloudShell-Token': boot.token}, keepalive: true});} catch {}}
    dispose();
  });
  window.addEventListener('cloudshell:theme', publish);
  window.DengShellAIWindowOwner = {open, close: dispose, publish};
})();
