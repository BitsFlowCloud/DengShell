'use strict';
window.DengProfileNotes = (() => {
  let dialog, profile;
  const fields = new WeakMap(), maxLineWidth = 40, maxLines = 3;
  const limitHint = '正文最多 3 行，每行最多 20 个中文或 40 个英文字符';
  const normalize = value => value.replace(/\r\n?/g, '\n');
  const charWidth = char => char === '\t' ? 4 : char.codePointAt(0) <= 0x7f ? 1 : 2;
  const lineWidth = line => Array.from(line).reduce((sum, char) => sum + charWidth(char), 0);
  const measure = value => {
    const lines = normalize(value).split('\n');
    return { chars: Array.from(normalize(value)).length, lines: value ? lines.length : 0, widths: lines.map(lineWidth) };
  };
  const fits = value => { const n = measure(value); return n.lines <= maxLines && n.widths.every(width => width <= maxLineWidth); };
  function preview(value) {
    const lines = [];
    for (const source of normalize(value).trim().split('\n')) {
      let line = '', width = 0;
      for (const char of source.replace(/\t/g, '    ')) {
        const size = charWidth(char);
        if (width + size > maxLineWidth) { lines.push(line); line = ''; width = 0; }
        line += char; width += size;
      }
      lines.push(line);
    }
    if (lines.length > maxLines) {
      const last = Array.from(lines[maxLines - 1]);
      while (lineWidth(last.join('')) + 2 > maxLineWidth) last.pop();
      lines[maxLines - 1] = last.join('') + '…';
    }
    return lines.slice(0, maxLines).join('\n');
  }
  function reflect(field, rejected = false) {
    const state = fields.get(field), n = measure(field.value), legacy = !fits(state.original);
    const valid = fits(field.value) || field.value === state.original;
    field.setCustomValidity(valid ? '' : limitHint);
    state.hint.dataset.limitReached = String(rejected || !valid);
    const activeLine = normalize(field.value.slice(0, field.selectionStart)).split('\n').length - 1;
    state.hint.textContent = `${rejected ? '本次输入超出限制，未写入；可换行继续。' : ''}${limitHint}（中文计 2，英文计 1） · ${n.lines}/3 行 · 当前行 ${n.widths[activeLine] || 0}/40${legacy ? '；原备注已保留，修改后需符合限额' : ''}`;
    return valid;
  }
  function init(field, value = '') {
    let state = fields.get(field);
    if (!state) {
      const hint = document.getElementById(field.getAttribute('aria-describedby'));
      state = { hint, original: '', accepted: '', start: 0, end: 0, composing: false };
      fields.set(field, state);
      const accept = () => {
        if (state.composing) return;
        const next = measure(field.value), previous = measure(state.accepted);
        // Existing long notes can be shortened without discarding their content.
        const reducingLegacy = !fits(state.accepted) && next.chars <= previous.chars && next.lines <= previous.lines;
        const rejected = !fits(field.value) && !reducingLegacy;
        if (rejected) {
          field.value = state.accepted;
          field.setSelectionRange(state.start, state.end);
        } else state.accepted = field.value;
        reflect(field, rejected);
      };
      field.addEventListener('beforeinput', () => {
        if (!state.composing) { state.start = field.selectionStart; state.end = field.selectionEnd; }
      });
      field.addEventListener('input', accept);
      field.addEventListener('click', () => { if (!state.composing) reflect(field); });
      field.addEventListener('keyup', event => { if (!state.composing && ['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', 'Home', 'End'].includes(event.key)) reflect(field); });
      field.addEventListener('compositionstart', () => {
        state.start = field.selectionStart; state.end = field.selectionEnd; state.composing = true;
      });
      field.addEventListener('compositionend', () => { state.composing = false; accept(); });
    }
    field.value = value;
    state.original = field.value; state.accepted = field.value; state.composing = false;
    reflect(field);
  }
  function validate(field) {
    if (!reflect(field)) { field.reportValidity(); return false; }
    return true;
  }
  document.addEventListener('DOMContentLoaded', () => {
    dialog = node('dialog'); dialog.id = 'profile-notes-dialog'; dialog.setAttribute('aria-labelledby', 'profile-notes-title');
    dialog.innerHTML = '<div class="dialog-heading"><h2 id="profile-notes-title">服务器备注</h2><button type="button" class="icon-button" aria-label="关闭备注" id="close-profile-notes"><svg><use href="#i-close"/></svg></button></div><p id="profile-notes-target"></p><pre id="profile-notes-content"></pre><div class="profile-notes-actions"><button type="button" class="secondary-button" id="copy-profile-notes">复制备注</button><button type="button" class="primary-button" id="edit-profile-notes">编辑备注</button></div>';
    document.body.append(dialog);
    $('#close-profile-notes').onclick = () => dialog.close();
    $('#copy-profile-notes').onclick = safe(async () => { await copyText(profile?.notes || ''); toast('备注已复制'); });
    $('#edit-profile-notes').onclick = () => { const latest = profiles.find(p => p.id === profile?.id); if (!latest) { toast('此连接已删除或不可用'); return; } dialog.close(); showConnectionForm(latest); $('#connection-notes').focus(); };
    dialog.addEventListener('close', () => { profile = null; $('#profile-notes-content').textContent = ''; });
  }, { once: true });
  function open(value) {
    if (!dialog || !value) return;
    profile = value;
    $('#profile-notes-target').textContent = `${value.name} · ${value.user}@${value.host}:${value.port}`;
    $('#profile-notes-content').textContent = value.notes || '暂无备注';
    $('#edit-profile-notes').hidden = !!value.deletedAt;
    dialog.showModal();
  }
  return { open, init, validate, preview };
})();
