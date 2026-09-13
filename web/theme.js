// Apply before styles load so a saved dark workspace never starts as a white page.
(() => {
  'use strict';
  const key = 'cloudshell.theme';
  const system = matchMedia('(prefers-color-scheme: dark)');
  const reducedMotion = matchMedia('(prefers-reduced-motion: reduce)');
  let transition, transitionTimer, fallbackAnimation, generation = 0;
  let nativeTheme = '', nativePending = false, nativeTimer;
  const valid = value => value === 'light' || value === 'dark';
  const validPreference = value => valid(value) || value === 'system';
  function read() { try { const value = JSON.parse(localStorage.getItem(key)); return validPreference(value) ? value : 'system'; } catch { return 'system'; } }
  const bootTheme = window.CLOUDSHELL?.theme;
  let preference = validPreference(bootTheme) ? bootTheme : read();
  function resolved() { return preference === 'system' ? nativeTheme || (system.matches ? 'dark' : 'light') : preference; }
  function reflectSetting() {
    const control = document.getElementById('follow-system-theme');
    if (!control) return;
    control.checked = preference === 'system';
    document.getElementById('follow-system-theme-hint').textContent = control.checked
      ? `当前${resolved() === 'dark' ? '深色' : '浅色'} · 手动切换明暗后关闭跟随`
      : '开启后随 Windows / Linux 系统自动切换';
  }
  function apply() {
    const theme = resolved();
    document.documentElement.dataset.theme = theme;
    document.documentElement.dataset.themePreference = preference;
    document.documentElement.style.colorScheme = theme;
    reflectSetting();
    window.dispatchEvent(new CustomEvent('cloudshell:theme', { detail: theme }));
  }
  function render(animate = true) {
    const theme = resolved();
    if (theme === document.documentElement.dataset.theme) { apply(); return; }
    const token = ++generation, root = document.documentElement;
    transition?.skipTransition(); fallbackAnimation?.cancel(); clearTimeout(transitionTimer);
    const cleanup = () => { if (token !== generation) return; root.classList.remove('theme-transitioning'); delete root.dataset.lightSwitch; };
    if (!animate || reducedMotion.matches || !document.body) { cleanup(); apply(); return; }
    const duration = theme === 'light' ? 820 : 740;
    root.dataset.lightSwitch = theme === 'light' ? 'on' : 'off';
    root.style.setProperty('--light-switch-duration', duration + 'ms');
    // System changes use the same gentle room-light transition as the header button.
    if (typeof document.startViewTransition === 'function') {
      const next = document.startViewTransition(() => { if (token === generation) apply(); }); transition = next;
      next.ready.catch(() => {}); next.updateCallbackDone.catch(() => {});
      next.finished.catch(() => {}).finally(() => { if (transition === next) transition = null; cleanup(); });
    } else {
      root.classList.add('theme-transitioning'); apply();
      if (root.animate) fallbackAnimation = root.animate(theme === 'light'
        ? [{filter:'brightness(.8) saturate(.88)'},{filter:'brightness(1) saturate(1)'}]
        : [{filter:'brightness(1.12)'},{filter:'brightness(.94)',offset:.65},{filter:'brightness(1)'}],
        {duration,easing:'cubic-bezier(.22,.65,.3,1)'});
      transitionTimer = setTimeout(cleanup, duration + 30);
    }
  }
  function set(theme) {
    if (!validPreference(theme) || preference === theme) return;
    preference = theme;
    try { localStorage.setItem(key, JSON.stringify(theme)); } catch {}
    render();
    // Save the user's choice only; an OS appearance notification is not a setting edit.
    window.dispatchEvent(new CustomEvent('cloudshell:theme-preference', { detail: theme }));
    refreshNativeTheme();
  }
  async function refreshNativeTheme() {
    clearTimeout(nativeTimer);
    if (nativePending || preference !== 'system' || document.hidden || window.DengShellWindowHidden) return;
    const desktop = window.go?.main?.Desktop;
    if (!desktop?.SystemTheme) { nativeTimer = setTimeout(refreshNativeTheme, 2000); return; }
    nativePending = true;
    try {
      // Read the OS directly: forcing a WebView/GTK app theme can feed back into matchMedia.
      const value = await desktop.SystemTheme();
      if (preference === 'system') {
        const next = valid(value) ? value : '';
        if (next !== nativeTheme) { nativeTheme = next; render(); }
      }
    } catch { /* Retain the last OS reading; browser media queries remain the fallback. */ }
    finally { nativePending = false; if (preference === 'system') nativeTimer = setTimeout(refreshNativeTheme, 2000); }
  }
  window.CloudShellTheme = Object.freeze({
    get current() { return document.documentElement.dataset.theme; },
    get preference() { return preference; },
    set,
    refreshSystem: refreshNativeTheme,
    toggle() { set(this.current === 'dark' ? 'light' : 'dark'); },
  });
  system.addEventListener('change', () => { if (preference === 'system') { if (!nativeTheme) render(); refreshNativeTheme(); } });
  window.addEventListener('storage', event => {
    if (typeof bootTheme === 'string') return;
    if (event.key === key || event.key === null) { preference = read(); render(); refreshNativeTheme(); }
  });
  window.addEventListener('focus', refreshNativeTheme);
  document.addEventListener('visibilitychange', refreshNativeTheme);
  document.addEventListener('DOMContentLoaded', () => {
    const menu = document.getElementById('settings-menu');
    if (menu) {
      const row = document.createElement('label'); row.className = 'system-theme-setting'; row.htmlFor = 'follow-system-theme';
      row.innerHTML = '<span>明暗跟随系统<small id="follow-system-theme-hint"></small></span><input id="follow-system-theme" type="checkbox" role="switch" aria-describedby="follow-system-theme-hint">';
      const first = menu.querySelector('.startup-animation-setting'); if (first) first.after(row); else menu.prepend(row);
      row.querySelector('input').addEventListener('change', event => set(event.target.checked ? 'system' : resolved()));
      reflectSetting();
    }
    window.runtime?.EventsOn?.('dengshell:window-visibility', value => { if (!value.hidden) setTimeout(refreshNativeTheme, 0); });
    refreshNativeTheme();
  }, { once: true });
  apply();
})();
