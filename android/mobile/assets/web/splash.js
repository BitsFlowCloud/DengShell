// Play on each opening, independently of the desktop/API and update checks.
(() => {
  'use strict';
  const preferenceKey = 'dengshell.startupAnimation';
  const boot = window.CLOUDSHELL || {}, duration = 3500;
  const read = (storage, key) => { try { return JSON.parse(window[storage].getItem(key)); } catch { return null; } };
  const write = (storage, key, value) => { try { window[storage].setItem(key, JSON.stringify(value)); } catch {} };
  let enabled = typeof boot.startupAnimation === 'boolean' ? boot.startupAnimation : read('localStorage', preferenceKey) !== false;
  let overlay = null, finishTimer = null, deadline = 0, prepared = false, started = false, settled = false, resolveFinished;
  const finished = new Promise(resolve => { resolveFinished = resolve; });
  const skippedKeys = new Set();
  function finish(reason = 'complete') {
    clearTimeout(finishTimer); finishTimer = null;
    overlay?.remove(); overlay = null;
    document.documentElement.classList.remove('startup-playing');
    if (!settled) {
      settled = true; resolveFinished();
      window.dispatchEvent(new CustomEvent('dengshell:splash-finished', { detail:{ reason:typeof reason === 'string' ? reason : 'complete' } }));
    }
  }
  function reflectSetting() {
    const control = document.getElementById('startup-animation');
    if (control) control.checked = enabled;
    const state = document.getElementById('startup-animation-state');
    if (state) state.textContent = enabled ? '开' : '关';
  }
  function accept(value) {
    enabled = value !== false; write('localStorage', preferenceKey, enabled);
    reflectSetting();
    if (!enabled) finish('disabled');
  }
  function startWhenVisible() {
    if (!overlay || !prepared || started || document.visibilityState === 'hidden') return;
    // Let the native window paint before spending the short introduction's time.
    requestAnimationFrame(() => requestAnimationFrame(() => {
      if (!overlay || started || document.visibilityState === 'hidden') return;
      started = true; overlay.classList.add('is-running');
      deadline = performance.now() + duration;
      finishTimer = setTimeout(() => finish(), duration);
    }));
  }
  window.DengShellSplash = Object.freeze({ accept, finish, finished, get enabled() { return enabled; }, get active() { return !!overlay; } });
  accept(enabled);
  if (enabled) {
    overlay = document.createElement('div'); overlay.className = 'startup-splash'; overlay.id = 'startup-splash'; overlay.setAttribute('aria-hidden', 'true');
    const panel = document.createElement('div'); panel.className = 'startup-splash-panel';
    const brand = document.createElement('div'); brand.className = 'splash-brand-stage';
    const icon = new Image(); icon.className = 'splash-app-icon'; icon.src = 'assets/dengshell.svg'; icon.alt = '';
    const name = document.createElement('div'); name.className = 'splash-app-name'; name.textContent = 'DengShell'; brand.append(icon, name);
    const powered = document.createElement('div'); powered.className = 'splash-powered-stage';
    const label = document.createElement('span'); label.className = 'splash-powered-label'; label.textContent = 'Powered By';
    const logo = new Image(); logo.className = 'splash-powered-logo'; logo.src = 'assets/bitsflow-powered.png'; logo.alt = 'Bitsflow';
    const hint = document.createElement('span'); hint.className = 'splash-settings-hint'; hint.textContent = '此动画可在设置中关闭'; powered.append(label, logo, hint);
    panel.append(brand, powered); overlay.append(panel); document.body.append(overlay);
    document.documentElement.classList.add('startup-playing');
    overlay.addEventListener('animationend', event => { if (event.target === overlay) finish(); });
    const ready = document.readyState === 'loading' ? new Promise(resolve => document.addEventListener('DOMContentLoaded', resolve, { once:true })) : Promise.resolve();
    ready.then(async () => {
      await Promise.race([Promise.allSettled([icon,logo].map(image => typeof image.decode === 'function' ? image.decode() : Promise.resolve())), new Promise(resolve => setTimeout(resolve, 250))]);
      prepared = true;
      startWhenVisible();
    });
    // Keep the overlay until click, so the matching pointer-up cannot hit a
    // control that happened to sit underneath the introduction.
    overlay.addEventListener('pointerdown', event => { event.preventDefault(); event.stopPropagation(); });
    overlay.addEventListener('click', event => { event.preventDefault(); event.stopPropagation(); finish('interaction'); });
  }
  // A suspended/background window must never resume an expired introduction.
  document.addEventListener('visibilitychange', () => { if (!started) startWhenVisible(); else if (overlay && performance.now() >= deadline) finish(); });
  // A top-layer native-close dialog stays usable, but the first key used to
  // skip the animation must never become terminal input or a button action.
  document.addEventListener('pointerdown', event => { if (overlay && event.target.closest('dialog[open]')) finish('interaction'); }, true);
  document.addEventListener('keydown', event => {
    if (!overlay && !skippedKeys.has(event.code || event.key)) return;
    skippedKeys.add(event.code || event.key);
    event.preventDefault(); event.stopImmediatePropagation(); finish('interaction');
  }, true);
  document.addEventListener('keyup', event => {
    if (!skippedKeys.delete(event.code || event.key)) return;
    event.preventDefault(); event.stopImmediatePropagation();
  }, true);
  window.addEventListener('blur', () => skippedKeys.clear());
  window.addEventListener('pagehide', () => finish('pagehide'), { once:true });
  document.addEventListener('DOMContentLoaded', () => {
    const control = document.getElementById('startup-animation');
    if (!control) return;
    reflectSetting();
    control.addEventListener('change', async () => {
      const previous = enabled, next = control.checked; control.disabled = true;
      try { await persistAppearance({ startupAnimation:next }); accept(next); }
      catch (error) { appearance.startupAnimation = previous; accept(previous); toast(`启动动画设置未能保存：${error.message || error}`); }
      finally { control.disabled = false; }
    });
  }, { once:true });
})();
