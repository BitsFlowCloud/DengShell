(() => {
  'use strict';
  const nativePage = location.protocol === 'wails:' || location.hostname === 'wails.localhost';
  const status = window.DengAIWindowTitlebar = {ready: false};
  // Browser popups retain their browser frame and the assistant's close button.
  if (!nativePage) return;
  const desktop = () => window.go?.main?.Desktop;
  const make = (tag, className = '', text = '') => {const element = document.createElement(tag); element.className = className; element.textContent = text; return element;};
  const header = make('header', 'ai-window-titlebar'); header.id = 'ai-window-titlebar'; header.hidden = true;
  const title = make('div', 'ai-window-title');
  const logo = make('img'); logo.src = 'assets/dengshell.svg'; logo.alt = ''; logo.draggable = false;
  title.append(logo, make('span', '', 'DengShell AI'));
  const controls = make('div', 'ai-window-controls'); controls.setAttribute('role', 'group'); controls.setAttribute('aria-label', 'AI 窗口控制');
  const paths = {
    pin: 'M5.5 2.5h5M6 2.5v4l-2 2v1h8v-1l-2-2v-4M8 9.5v4',
    minimise: 'M3.5 8.5h9', maximise: 'M3.5 3.5h9v9h-9z',
    restore: 'M5.5 5.5v-3h8v8h-3M2.5 5.5h8v8h-8z',
    close: 'M4 4l8 8M12 4l-8 8',
  };
  function icon(button, name) {
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg'); svg.setAttribute('viewBox', '0 0 16 16'); svg.setAttribute('aria-hidden', 'true');
    const path = document.createElementNS('http://www.w3.org/2000/svg', 'path'); path.setAttribute('d', paths[name]); svg.append(path); button.replaceChildren(svg);
  }
  function label(button, value) {button.title = value; button.setAttribute('aria-label', value);}
  function button(id, text, name) {const value = make('button'); value.type = 'button'; value.id = id; label(value, text); icon(value, name); controls.append(value); return value;}
  const pin = button('ai-pin', '置顶窗口', 'pin'); pin.setAttribute('aria-pressed', 'false');
  const minimise = button('ai-window-minimise', '最小化', 'minimise');
  const maximise = button('ai-window-maximise', '最大化', 'maximise');
  const close = button('ai-window-close', '关闭 AI 窗口', 'close');
  header.append(title, controls);
  const error = make('p', 'ai-window-control-error'); error.hidden = true; error.setAttribute('role', 'status');
  document.body.prepend(header); header.after(error);
  function failed(cause) {error.textContent = cause?.message || String(cause); error.hidden = false;}
  function reflect(state) {const restored = !!state?.maximised; label(maximise, restored ? '还原窗口' : '最大化'); icon(maximise, restored ? 'restore' : 'maximise');}
  let pinBusy = false, actionBusy = false, refreshing = false;
  pin.onclick = async () => {
    if (!status.ready || pinBusy) return;
    pinBusy = true; pin.disabled = true; error.hidden = true;
    try {
      const value = await window.DengAIWindowTransport.pin(pin.getAttribute('aria-pressed') !== 'true');
      pin.setAttribute('aria-pressed', String(value)); label(pin, value ? '取消置顶' : '置顶窗口');
    } catch (cause) {failed(cause);}
    finally {pinBusy = false; pin.disabled = false;}
  };
  async function action(name) {
    if (!status.ready || actionBusy) return;
    actionBusy = true; error.hidden = true;
    try {reflect(await desktop().WindowAction(name));}
    catch (cause) {failed(cause);}
    finally {actionBusy = false;}
  }
  minimise.onclick = () => action('minimise');
  maximise.onclick = () => action('toggle-maximise');
  close.onclick = () => window.DengAIWindowTransport.close();
  header.ondblclick = event => {if (!event.target.closest('button') && getComputedStyle(event.target).getPropertyValue('--wails-draggable').trim() === 'drag') void action('toggle-maximise');};
  async function refresh() {
    if (!status.ready || refreshing) return;
    refreshing = true;
    try {reflect(await desktop().WindowState());} catch {} finally {refreshing = false;}
  }
  window.addEventListener('resize', refresh); window.addEventListener('focus', refresh);
  void (async () => {
    const until = Date.now() + 10000;
    while (!desktop()?.WindowState || !desktop()?.WindowAction || !desktop()?.SetAIWindowAlwaysOnTop) {
      if (Date.now() > until) {failed(new Error('窗口控制尚未就绪，请重新打开 AI 窗口。')); return;}
      await new Promise(resolve => setTimeout(resolve, 30));
    }
    try {
      const state = await desktop().WindowState();
      if (!state?.frameless) return;
      reflect(state); header.hidden = false; status.ready = true;
      window.dispatchEvent(new CustomEvent('dengshell:ai-window-controls'));
    } catch (cause) {failed(cause);}
  })();
})();
