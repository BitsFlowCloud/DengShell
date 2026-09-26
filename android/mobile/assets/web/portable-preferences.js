// Webview storage is a cache, never the owner of portable application settings.
(() => {
  'use strict';
  let entries = { ...(window.CLOUDSHELL?.uiPreferences || {}) }, ready = false, timer;
  const dirty = new Map(); let revision = 0;
  const allowed = key => ['cloudshell.layout', 'dengshell.appearance-palette', 'dengshell.server-groups.collapsed', 'dengshell.server-manager', 'dengshell.workspace'].includes(key) || /^dengshell\.(history|nic)\.[A-Za-z0-9_-]{1,128}$/.test(key);
  function read(key, fallback) { return Object.hasOwn(entries, key) ? structuredClone(entries[key]) : fallback; }
  function write(key, value) {
    if (!allowed(key) || key.startsWith('dengshell.history.')) return;
    if (value === undefined) delete entries[key]; else entries[key] = structuredClone(value);
    if (!ready) return;
    dirty.set(key, ++revision);
    clearTimeout(timer); timer = setTimeout(() => flush().catch(error => toast(`设置未能保存：${error.message || error}`)), 180);
  }
  async function flush() {
    await window.DengCommandHistory?.flush();
    clearTimeout(timer); timer = null;
    if (ready) {
      const submitted = new Map(dirty), patch = {};
      for (const key of submitted.keys()) patch[key] = Object.hasOwn(entries, key) ? structuredClone(entries[key]) : null;
      // Also flush pending font previews, without resending unrelated layout keys.
      await persistAppearance(submitted.size ? { layout: patch } : {});
      for (const [key, version] of submitted) if (dirty.get(key) === version) dirty.delete(key);
    }
    await appearanceSave;
  }
  function accept(config) {
    const next = { ...(config.appearance?.layout || {}) };
    for (const key of dirty.keys()) {
      if (Object.hasOwn(entries, key)) next[key] = entries[key]; else delete next[key];
    }
    entries = next; ready = true;
    // Do not let an old Wails origin or a different portable directory leak state.
    try { for (let i = localStorage.length - 1; i >= 0; i--) {
      const key = localStorage.key(i); if (allowed(key)) localStorage.removeItem(key);
    } } catch {}
  }
  window.DengPortablePreferences = { read, write, accept, flush, cache: (key, value) => { if (allowed(key)) entries[key] = structuredClone(value); }, snapshot: () => ({ ...entries }), get ready() { return ready; } };
  window.addEventListener('cloudshell:theme-preference', () => {
    if (ready) persistAppearance({ theme: window.CloudShellTheme.preference || 'system' }).catch(error => toast(`明暗设置未能保存：${error.message || error}`));
  });
  let windowTimer;
  window.addEventListener('resize', () => {
    if (!ready) return;
    clearTimeout(windowTimer); windowTimer = setTimeout(() => native()?.CaptureWindowState?.().catch(error => console.warn('窗口大小未能保存', error)), 350);
  });
})();
