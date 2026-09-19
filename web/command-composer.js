'use strict';

// Templates are text substitutions only; no expression or local script is run.
window.DengCommandComposer = (() => {
  const tokenPattern = () => /\[p#([1-5])(?:[ \t]+([^\]\r\n]+))?\]/g;
  const parameters = text => [...new Map([...text.matchAll(tokenPattern())].map(m => [m[1], { id: m[1], name: m[2]?.trim() || `参数${m[1]}` }])).values()];
  const resolve = (text, values) => text.replace(tokenPattern(), (token, id) => Object.hasOwn(values, id) ? values[id] : token);
  const usable = state => !!state?.connected && !!state.ready && !state.closed && !state.detaching && !state.restoring && !state.ownershipUncertain && sessions.get(state.id) === state;
  function prepare(text, appendCR, bracketed) {
    if (typeof text !== 'string' || !text.trim()) throw new Error('请填写命令内容');
    if (new TextEncoder().encode(text).length > 65536) throw new Error('命令最多 64 KB');
    if (/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f-\x9f]/.test(text)) throw new Error('命令中包含终端控制字符，请删除后重试');
    if (/\[p#/.test(text)) throw new Error('请填写全部参数，并检查参数占位符');
    const result = text.replace(/\r\n|\r/g, '\n').replace(/\n+$/, '');
    // Without bracketed paste, even an embedded newline submits a shell line.
    // Keep the draft local instead of pretending that omitting the final CR is safe.
    if (!appendCR && /[\n\t]/.test(result) && !bracketed) throw new Error('当前终端未开启括号粘贴，多行或制表符命令已保留在编辑区，未发送。请拆成单行，或确认后勾选末尾回车。');
    return result;
  }
  function send(state, text, appendCR = false) {
    if (!usable(state)) throw new Error('目标连接已断开或正在转移，请重新选择连接');
    const body = prepare(text, appendCR, state.term.modes?.bracketedPasteMode && !state.term.options?.ignoreBracketedPasteMode);
    if (current() !== state) activate(state.id);
    return pasteTerminalText(state, body, { execute: appendCR });
  }
  function insert(area, index) {
    const text = `[p#${index} 参数${index}]`, start = area.selectionStart;
    area.setRangeText(text, start, area.selectionEnd, 'end');
    area.dispatchEvent(new Event('input', { bubbles: true }));
    area.focus(); area.setSelectionRange(start + 5, start + text.length - 1);
  }
  function parameterButtons(area) {
    const row = node('div', 'command-parameter-buttons');
    for (let i = 1; i <= 5; i++) {
      const b = node('button', 'upload-button', `参数${i}`); b.type = 'button';
      b.title = `在光标处插入参数 ${i}，可修改参数名`; b.onclick = () => insert(area, i); row.append(b);
    }
    return row;
  }
  let panel, template, fields, preview, target, cr, sendButton, note, title, activeDraft, compact = false;
  let previousHeight = null, expandedHeight = null;
  const drafts = new Map();
  function collapse() {
    panel.hidden = true; $('#toggle-command-composer').setAttribute('aria-expanded', 'false');
    const style = document.documentElement.style;
    if (expandedHeight !== null && style.getPropertyValue('--files-height') === expandedHeight) {
      if (previousHeight) style.setProperty('--files-height', previousHeight); else style.removeProperty('--files-height');
    }
    previousHeight = expandedHeight = null;
  }
  function render() {
    if (!activeDraft) return;
    const d = activeDraft, params = parameters(d.body);
    const result = resolve(d.body, d.values);
    preview.value = result; preview.hidden = params.length === 0;
    const missing = params.some(p => !Object.hasOwn(d.values, p.id) || !d.values[p.id].trim());
    sendButton.textContent = cr.checked ? '发送并执行' : '仅填入终端';
    sendButton.disabled = missing || !d.body.trim() || !usable(sessions.get(d.targetID));
    note.textContent = missing ? '请填写全部参数；参数按原样替换，请确认空格和引号。' : cr.checked ? '末尾追加回车，发送后执行。编辑内容会保留，便于修改参数后再次使用。' : '不追加回车，末尾换行会移除；填入终端后可以继续补充参数，再手动回车。';
  }
  function renderParameters() {
    fields.replaceChildren(...parameters(activeDraft.body).map(p => {
      const label = node('label', '', `${p.name}（${p.id}）`), input = node('input');
      input.dataset.parameter = p.id; input.value = activeDraft.values[p.id] || ''; input.autocomplete = 'off'; input.spellcheck = false;
      input.oninput = () => { activeDraft.values[p.id] = input.value; render(); }; label.append(input); return label;
    }));
    render();
  }
  function reflect() {
    if (!target || !activeDraft) return;
    const id = activeDraft.targetID, options = [...sessions.values()].filter(s => !s.closed && !s.localOnly);
    const placeholder = node('option', '', id && !options.some(s => s.id === id) ? '原连接已关闭，请重新选择' : '请选择发送目标'); placeholder.value = '';
    target.replaceChildren(placeholder, ...options.map(s => {
      const p = profileFor(s), option = node('option', '', `${p?.name || '服务器'} · ${p?.user || ''}@${p?.host || ''}:${p?.port || 22}${usable(s) ? '' : ' · 不可发送'}`);
      option.value = s.id; return option;
    }));
    target.value = options.some(s => s.id === id) ? id : ''; window.DengSelect?.refresh(target); render();
  }
  function open(command = null, body = null, execute = false) {
    compact = execute; panel.classList.toggle('command-composer-compact', compact);
    const key = command?.id || 'scratch';
    const source = command ? JSON.stringify([command.body, command.appendCR]) : '';
    if (!drafts.has(key) || body !== null || command && drafts.get(key).source !== source) drafts.set(key, { source, body: body ?? command?.body ?? '', values: {}, appendCR: command?.appendCR === true, targetID: current()?.id || '' });
    activeDraft = drafts.get(key);
    title.textContent = command ? `${compact ? '执行' : '命令编辑区'} · ${command.name}` : '命令编辑区';
    template.value = activeDraft.body; cr.checked = activeDraft.appendCR;
    panel.hidden = false; $('#toggle-command-composer').setAttribute('aria-expanded', 'true');
    // The target is pinned in the draft. Switching a server tab cannot redirect it.
    renderParameters(); reflect();
    showPane('commands');
    // Expand the lower workspace just for this view; retain the user's saved
    // splitter dimensions and let subsequent drags continue to control it.
    const workspace = $('#files-panel').parentElement;
    const currentHeight = $('#files-panel').getBoundingClientRect().height / effectiveScale;
    const desired = Math.min(compact ? 330 : 600, workspace.getBoundingClientRect().height / effectiveScale - 190);
    if (desired > currentHeight) {
      if (expandedHeight === null) previousHeight = document.documentElement.style.getPropertyValue('--files-height');
      expandedHeight = `${desired}px`; document.documentElement.style.setProperty('--files-height', expandedHeight);
    }
    $('#commands-view .commands-content').scrollTop = 0;
    (fields.querySelector('input') || (compact ? target : template)).focus();
  }
  function run(command) {
    if (parameters(command.body).length || /\[p#/.test(command.body) || !usable(current())) { open(command, null, true); return; }
    try { send(current(), command.body, command.appendCR === true); }
    catch (error) { open(command, null, true); toast(error.message); }
  }
  function init() {
    const toggle = node('button', 'upload-button', '命令编辑区'); toggle.id = 'toggle-command-composer'; toggle.type = 'button'; toggle.setAttribute('aria-expanded', 'false');
    $('#new-command').before(toggle); toggle.onclick = () => { if (panel.hidden) open(); else collapse(); };
    panel = node('section', 'command-composer'); panel.id = 'command-composer'; panel.hidden = true;
    const head = node('div', 'command-composer-heading'); title = node('strong', '', '命令编辑区');
    const close = node('button', 'text-button', '收起'); close.onclick = collapse;
    head.append(title, close);
    const targetLabel = node('label', 'composer-target-label', '发送目标'); target = node('select'); target.id = 'composer-target'; target.onchange = () => { activeDraft.targetID = target.value; render(); }; targetLabel.append(target);
    const label = node('label', 'composer-template-label', '命令 / 参数模板'); template = node('textarea'); template.id = 'composer-body'; template.rows = 3; template.maxLength = 65536; template.spellcheck = false;
    template.oninput = () => { activeDraft.body = template.value; renderParameters(); }; label.append(template);
    fields = node('div', 'command-parameter-fields'); preview = node('textarea'); preview.id = 'composer-preview'; preview.readOnly = true; preview.rows = 2; preview.setAttribute('aria-label', '实际发送命令预览');
    const foot = node('div', 'command-composer-footer'), crLabel = node('label', 'checkbox-label'); cr = node('input'); cr.type = 'checkbox'; cr.id = 'composer-append-cr';
    cr.onchange = () => { activeDraft.appendCR = cr.checked; render(); }; crLabel.append(cr, document.createTextNode('末尾添加回车 CR'));
    sendButton = node('button', 'primary-button', '仅填入终端'); sendButton.id = 'composer-send';
    sendButton.onclick = safe(() => { render(); if (sendButton.disabled) return; send(sessions.get(activeDraft.targetID), resolve(activeDraft.body, activeDraft.values), cr.checked); });
    const saveAs = node('button', 'upload-button composer-save-as', '存为快捷命令'); saveAs.onclick = () => editCommand({ name: '', body: activeDraft.body, appendCR: cr.checked, group: commandGroup });
    foot.append(crLabel, saveAs, sendButton); note = node('p', 'command-composer-note'); note.setAttribute('role', 'status');
    panel.append(head, targetLabel, label, parameterButtons(template), fields, preview, foot, note);
    $('#commands-view .commands-content').prepend(panel);
    panel.addEventListener('contextmenu', e => e.stopPropagation());
    panel.addEventListener('keydown', e => { if ((e.ctrlKey || e.metaKey) && e.key === 'Enter' && !e.isComposing) { e.preventDefault(); sendButton.click(); } });
    const bodyArea = $('#quick-command-form').elements.body;
    bodyArea.parentElement.after(parameterButtons(bodyArea));
    const expand = node('button', 'terminal-tool'); expand.id = 'expand-command-editor'; expand.title = '在命令编辑区修改长命令与参数'; expand.setAttribute('aria-label', expand.title); expand.append(icon('edit'));
    expand.onclick = () => open(null, $('#command-input').value); $('#command-history').before(expand);
  }
  document.addEventListener('DOMContentLoaded', init, { once: true });
  return { parameters, resolve, prepare, send, run, open, reflect, insert, parameterButtons };
})();
