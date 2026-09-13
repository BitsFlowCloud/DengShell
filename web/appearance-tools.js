'use strict';

// Shared color palettes are real dialogs: keyboard focus stays inside while the
// transparent backdrop leaves the live terminal visible. Dragging uses logical
// coordinates so application zoom and native WebKit share the same geometry.
const movableColorDialogs = new Set();
let fontColorCard = null, promptColorTimer = null, promptSavePending = false;
const promptSessionQueues = new Map();
function clampColorDialog(dialog) {
  if (!dialog.open) return;
  const position = dialog._position || { x: (logicalWidth() - dialog.offsetWidth) / 2, y: Math.max(12, (logicalHeight() - dialog.offsetHeight) / 3) };
  position.x = Math.max(8, Math.min(position.x, logicalWidth() - dialog.offsetWidth - 8));
  position.y = Math.max(8, Math.min(position.y, logicalHeight() - dialog.offsetHeight - 8));
  dialog._position = position; dialog.style.left = `${position.x}px`; dialog.style.top = `${position.y}px`;
}
function createColorDialog(id, title) {
  const dialog = node('dialog', 'movable-color-dialog'); dialog.id = id; dialog.setAttribute('aria-labelledby', `${id}-title`);
  const header = node('header', 'color-dialog-handle'), heading = node('h2', '', title), close = node('button', 'icon-button');
  heading.id = `${id}-title`; close.type = 'button'; close.append(icon('close')); close.setAttribute('aria-label', `关闭${title}`);
  header.tabIndex = 0; header.title = '拖动移动；方向键微调位置'; header.setAttribute('aria-label', `${title}位置，拖动或方向键移动`); header.append(heading, close);
  const body = node('div', 'color-dialog-body'); dialog.append(header, body); document.body.append(dialog); movableColorDialogs.add(dialog);
  close.onclick = () => dialog.close();
  let drag;
  header.addEventListener('pointerdown', event => {
    if (event.button !== 0 || event.target.closest('button')) return;
    const rect = dialog.getBoundingClientRect(); drag = { x: event.clientX, y: event.clientY, left: rect.left / effectiveScale, top: rect.top / effectiveScale };
    header.setPointerCapture(event.pointerId); dialog.classList.add('is-dragging'); event.preventDefault();
  });
  header.addEventListener('pointermove', event => {
    if (!drag) return;
    dialog._position = { x: drag.left + (event.clientX - drag.x) / effectiveScale, y: drag.top + (event.clientY - drag.y) / effectiveScale }; clampColorDialog(dialog);
  });
  const stop = () => { drag = null; dialog.classList.remove('is-dragging'); };
  header.addEventListener('pointerup', stop); header.addEventListener('pointercancel', stop); header.addEventListener('lostpointercapture', stop);
  header.addEventListener('keydown', event => {
    if (event.target !== header || !['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', 'Home'].includes(event.key)) return;
    event.preventDefault();
    if (event.key === 'Home') dialog._position = null;
    else { clampColorDialog(dialog); const p = dialog._position, step = event.shiftKey ? 30 : 10; p.x += event.key === 'ArrowRight' ? step : event.key === 'ArrowLeft' ? -step : 0; p.y += event.key === 'ArrowDown' ? step : event.key === 'ArrowUp' ? -step : 0; }
    clampColorDialog(dialog);
  });
  dialog.addEventListener('close', stop);
  dialog.addEventListener('keydown', event => { if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); dialog.close(); } });
  return { dialog, body, heading };
}
function openFontColorDialog(card) {
  let dialog = $('#font-color-dialog');
  if (!dialog) {
    const parts = createColorDialog('font-color-dialog', '字体颜色'); dialog = parts.dialog;
    const preview = node('pre', 'color-dialog-preview'); preview.id = 'font-color-preview'; parts.body.append(preview, node('div', 'font-color-editor-host'));
    dialog.addEventListener('close', () => { fontColorCard?._fontControls.colorButton.setAttribute('aria-expanded', 'false'); flushFontStyleSave().catch(error => toast(error.message)); fontColorCard?._fontControls.colorButton.focus(); });
  }
  fontColorCard?._fontControls.colorButton.setAttribute('aria-expanded', 'false'); fontColorCard = card;
  const controls = card._fontControls; controls.editor.hidden = false; controls.colorButton.setAttribute('aria-expanded', 'true');
  dialog.querySelector('.font-color-editor-host').replaceChildren(controls.editor);
  reflectFontColorDialog(card);
  if (!dialog.open) dialog.showModal(); clampColorDialog(dialog);
}
function reflectFontColorDialog(card) {
  if (fontColorCard !== card) return;
  const preview = $('#font-color-preview'); if (!preview) return;
  $('#font-color-dialog-title').textContent = `${card._asset.name} · 文字颜色`;
  preview.textContent = 'DengShell 012345\n中文目录 · 繁體終端'; preview.style.color = colorForFont(card.dataset.fontId);
  preview.style.fontFamily = `"${card._asset.family}", "Deng CJK", monospace`; preview.style.fontWeight = boldForFont(card.dataset.fontId) ? '700' : '400';
}
function applyWorkspacePlacement() {
  const root = document.documentElement;
  root.dataset.monitorSide = appearance.monitorSide === 'right' ? 'right' : 'left';
  root.dataset.filesPosition = appearance.filesPosition === 'top' ? 'top' : 'bottom';
  if ($('#monitor-side')) $('#monitor-side').value = root.dataset.monitorSide;
  if ($('#files-position')) $('#files-position').value = root.dataset.filesPosition;
  for (const dialog of movableColorDialogs) clampColorDialog(dialog);
}
function promptIdentity() {
  const state = current(), profile = state && profileFor(state);
  return { user: state?.promptUsername || state?.info?.promptUsername || profile?.user || 'user', host: state?.promptHostname || state?.info?.promptHostname || profile?.host || 'server', connected: !!state?.connected };
}
function reflectPromptPreview() {
  const preview = $('#prompt-color-preview'); if (!preview) return;
  const identity = promptIdentity();
  preview.querySelector('.prompt-user').textContent = identity.user; preview.querySelector('.prompt-host').textContent = identity.host;
  preview.querySelector('.prompt-user').style.color = appearance.promptUsernameColor || terminalDefaultColor;
  preview.querySelector('.prompt-host').style.color = appearance.promptHostnameColor || terminalDefaultColor;
  $('#prompt-color-identity').textContent = identity.connected ? '当前会话 · 设置将在下一次 Shell 提示符出现时生效' : '连接前预览 · 连接后使用真实用户名与主机名';
}
function createPromptColorField(title, key) {
  const section = node('section', 'prompt-color-field'), heading = node('div', 'font-color-heading'), reset = node('button', 'text-button', '保持原样'); reset.type = 'button';
  heading.append(node('strong', '', title), reset);
  const presets = node('div', 'font-color-presets'), custom = node('div', 'font-color-custom'), rgb = node('div', 'font-color-rgb');
  const pickerLabel = node('label', 'font-color-picker-label', '色盘'), picker = node('input'); picker.type = 'color'; picker.setAttribute('aria-label', `${title}色盘`); pickerLabel.append(picker);
  const hexLabel = node('label', 'font-color-hex-label', 'HEX'), hex = node('input'); hex.type = 'text'; hex.maxLength = 7; hex.placeholder = '原始颜色'; hex.spellcheck = false; hex.dataset.promptColor = key; hex.setAttribute('aria-label', `${title} HEX`); hexLabel.append(hex); custom.append(pickerLabel, hexLabel);
  const channels = [];
  function reflect(force = false) {
    const value = appearance[key] || '', color = value || terminalDefaultColor;
    if (force || document.activeElement !== hex) hex.value = value.toUpperCase();
    if (document.activeElement !== picker) picker.value = color;
    channels.forEach((input, index) => { if (force || document.activeElement !== input) input.value = parseInt(color.slice(1 + index * 2, 3 + index * 2), 16); });
    reset.setAttribute('aria-pressed', String(!value));
    for (const button of presets.children) button.setAttribute('aria-pressed', String(button.dataset.color === value));
  }
  function change(value) {
    if (value !== '' && !validFontColor(value)) return;
    appearance = { ...appearance, [key]: value.toLowerCase() }; reflect(); reflectPromptPreview(); schedulePromptColors();
  }
  for (const preset of fontColorPresets) {
    const button = node('button', 'font-color-swatch'); button.type = 'button'; button.dataset.color = preset.color; button.style.setProperty('--swatch-color', preset.color); button.title = `${title} · ${preset.name}`; button.setAttribute('aria-label', button.title); button.onclick = () => change(preset.color); presets.append(button);
  }
  ['R', 'G', 'B'].forEach(channel => {
    const label = node('label', '', channel), input = node('input'); input.type = 'number'; input.min = 0; input.max = 255; input.step = 1; input.setAttribute('aria-label', `${title} RGB ${channel}`); label.append(input); rgb.append(label); channels.push(input);
    input.oninput = () => { if (channels.some(item => !/^\d{1,3}$/.test(item.value) || +item.value > 255)) return; change('#' + channels.map(item => (+item.value).toString(16).padStart(2, '0')).join('')); }; input.onblur = () => reflect(true);
  });
  hex.oninput = () => { const value = hex.value.trim(); change(!value || value.startsWith('#') ? value : '#' + value); }; hex.onblur = () => reflect(true); picker.oninput = () => change(picker.value); reset.onclick = () => change('');
  section.append(heading, presets, custom, rgb); section._reflect = reflect; reflect(); return section;
}
function schedulePromptColors() {
  promptSavePending = true; clearTimeout(promptColorTimer); promptColorTimer = setTimeout(() => flushPromptColors().catch(error => { if ($('#prompt-color-status')) $('#prompt-color-status').textContent = error.message; }), 200);
}
async function flushPromptColors() {
  clearTimeout(promptColorTimer); promptColorTimer = null;
  if (!promptSavePending) return;
  promptSavePending = false;
  const style = { usernameColor: appearance.promptUsernameColor || '', hostnameColor: appearance.promptHostnameColor || '' };
  await persistAppearance({ promptUsernameColor: style.usernameColor, promptHostnameColor: style.hostnameColor });
  const targets = [...sessions.values()].filter(state => state.connected);
  const results = await Promise.allSettled(targets.map(state => {
    const id = state.id, before = promptSessionQueues.get(id) || Promise.resolve();
    const request = before.catch(() => {}).then(() => post(`/api/sessions/${encodeURIComponent(id)}/prompt-style`, style));
    promptSessionQueues.set(id, request); request.finally(() => { if (promptSessionQueues.get(id) === request) promptSessionQueues.delete(id); }).catch(() => {}); return request;
  }));
  const failed = results.find(result => result.status === 'rejected');
  const unsupported = results.some(result => result.status === 'fulfilled' && result.value?.supported === false);
  if ($('#prompt-color-status')) $('#prompt-color-status').textContent = failed ? `颜色已保存；当前会话暂未应用：${failed.reason.message}` : unsupported ? '颜色已保存；当前 Shell 不支持提示符配色，支持 Bash / Zsh / Fish。' : targets.length ? '已保存；在下一次 Bash / Zsh / Fish 提示符出现时应用。' : '已保存，连接服务器后应用。';
}
function openPromptColors() {
  setSettingsMenu(false); let dialog = $('#prompt-color-dialog');
  if (!dialog) {
    const parts = createColorDialog('prompt-color-dialog', '提示符颜色'); dialog = parts.dialog;
    const preview = node('pre', 'color-dialog-preview'); preview.id = 'prompt-color-preview'; preview.append(node('span', 'prompt-user'), document.createTextNode('@'), node('span', 'prompt-host'), document.createTextNode(':~$ '));
    const identity = node('p', 'color-dialog-note'); identity.id = 'prompt-color-identity';
    const fields = node('div', 'prompt-color-fields'); fields.append(createPromptColorField('用户名 · @ 前', 'promptUsernameColor'), createPromptColorField('主机名 · @ 后', 'promptHostnameColor'));
    const note = node('p', 'color-dialog-note', '标准提示符按用户名和主机名分段着色；复杂主题可能临时使用 user@host:路径 格式。两项都选择“保持原样”可恢复原提示符。支持 Bash / Zsh / Fish。'), status = node('p', 'color-dialog-note'); status.id = 'prompt-color-status'; status.setAttribute('role', 'status');
    parts.body.append(preview, identity, fields, note, status);
    dialog.addEventListener('close', () => { flushPromptColors().catch(error => toast(error.message)); $('#manage-prompt-colors').focus(); });
  }
  for (const field of dialog.querySelectorAll('.prompt-color-field')) field._reflect(true);
  reflectPromptPreview(); if (!dialog.open) dialog.showModal(); clampColorDialog(dialog);
}
function initializeAppearanceTools() {
  const anchor = $('#settings-menu .scale-setting');
  const promptButton = node('button'); promptButton.id = 'manage-prompt-colors'; promptButton.type = 'button'; promptButton.setAttribute('role', 'menuitem'); promptButton.append(icon('terminal'), document.createTextNode('提示符颜色')); promptButton.onclick = openPromptColors; anchor.before(promptButton);
  for (const [id, key, title, options] of [['monitor-side', 'monitorSide', '监控位置', [['left', '左侧'], ['right', '右侧']]], ['files-position', 'filesPosition', '文件面板', [['bottom', '终端下方'], ['top', '终端上方']]]]) {
    const row = node('div', 'scale-setting placement-setting'), label = node('label', '', title), select = node('select'); label.htmlFor = select.id = id; select.setAttribute('aria-label', title);
    for (const [value, text] of options) { const option = node('option', '', text); option.value = value; select.append(option); }
    select.onchange = safe(() => chooseAppearance({ [key]: select.value })); row.append(label, select); anchor.before(row);
  }
  applyWorkspacePlacement();
  window.addEventListener('resize', () => { for (const dialog of movableColorDialogs) clampColorDialog(dialog); });
  document.addEventListener('visibilitychange', () => { if (document.hidden) flushPromptColors().catch(() => {}); });
}
document.addEventListener('DOMContentLoaded', initializeAppearanceTools);
