'use strict';
window.DengChartStyles = (() => {
  const defaults = {
    upload: { color: '', width: 2.3, dashed: false },
    download: { color: '', width: 1.8, dashed: true },
    latency: { color: '', width: 1.65, dashed: false },
  };
  const labels = { upload: '上行网速', download: '下行网速', latency: '延迟' };
  const validColor = color => /^#[\da-f]{6}$/i.test(color);
  function resolved(id, styles = {}) { return { ...defaults[id], ...styles?.[id] }; }
  function apply(styles = {}) {
    const root = document.documentElement.style;
    for (const id of Object.keys(defaults)) {
      const style = resolved(id, styles), prefix = `--chart-${id}-`;
      if (validColor(style.color)) root.setProperty(prefix + 'color', style.color); else root.removeProperty(prefix + 'color');
      const width = Number(style.width);
      root.setProperty(prefix + 'width', `${Number.isFinite(width) && width >= .5 && width <= 6 ? width : defaults[id].width}px`);
      root.setProperty(prefix + 'dash', style.dashed && id !== 'latency' ? '5 4' : 'none');
      root.setProperty(prefix + 'line-style', style.dashed && id !== 'latency' ? 'dashed' : 'solid');
    }
  }
  function themeColor(id) {
    const dark = document.documentElement.dataset.theme === 'dark';
    return { upload: dark ? '#f5b36c' : '#ac5418', download: dark ? '#73d2ec' : '#087b99', latency: dark ? '#9bc4b3' : '#77a88f' }[id];
  }
  let dialog, form, initial, saving = false;
  function fields(id) {
    return Object.fromEntries(['color', 'width', 'dashed', 'theme'].map(field => [field, form.elements[`${id}-${field}`]]));
  }
  function fill(id, style) {
    const controls = fields(id);
    controls.color.value = style.color || themeColor(id); controls.theme.checked = !style.color;
    controls.color.disabled = controls.theme.checked; controls.width.value = style.width;
    if (controls.dashed) controls.dashed.value = style.dashed ? 'dashed' : 'solid';
  }
  function read(id) {
    const controls = fields(id);
    return { color: controls.theme.checked ? '' : controls.color.value, width: Number(controls.width.value), dashed: controls.dashed?.value === 'dashed' };
  }
  function preview() {
    for (const id of Object.keys(defaults)) fields(id).color.disabled = fields(id).theme.checked;
    apply(Object.fromEntries(Object.keys(defaults).map(id => [id, read(id)])));
  }
  async function open() {
    await loadProfiles();
    initial = structuredClone(appearance.chartStyles || {});
    for (const id of Object.keys(defaults)) fill(id, resolved(id, initial));
    dialog.querySelector('#chart-style-error').textContent = '';
    dialog.showModal();
  }
  function initialize() {
    dialog = document.createElement('dialog'); dialog.id = 'chart-style-dialog';
    dialog.innerHTML = `<form id="chart-style-form"><div class="dialog-heading"><h2>曲线样式</h2><button type="button" class="icon-button" id="close-chart-styles" aria-label="关闭曲线设置"><svg><use href="#i-close"/></svg></button></div><p>分别设置每条曲线，调整时预览，保存后重启仍有效。</p><div class="chart-style-rows"></div><p id="chart-style-error" role="alert"></p><div class="dialog-buttons"><button type="button" id="cancel-chart-styles">取消</button><button type="submit" class="primary-button" id="save-chart-styles">保存设置</button></div></form>`;
    document.body.append(dialog); form = dialog.querySelector('form');
    for (const id of Object.keys(defaults)) {
      const row = document.createElement('fieldset'); row.dataset.series = id;
      row.innerHTML = `<legend>${labels[id]}</legend><svg class="chart-style-preview" viewBox="0 0 260 30" aria-label="${labels[id]}线条预览"><path d="M3 24C28 24 28 8 53 8S78 22 103 22S128 5 153 5S188 24 213 24S238 12 257 12"/></svg><div class="chart-style-controls"><label>颜色<input type="color" name="${id}-color" aria-label="${labels[id]}颜色"></label><label>粗细（px）<input type="number" name="${id}-width" min="0.5" max="6" step="0.05" required aria-label="${labels[id]}粗细"></label>${id !== 'latency' ? `<label>线型<select name="${id}-dashed" aria-label="${labels[id]}线型"><option value="solid">实线</option><option value="dashed">虚线</option></select></label>` : ''}</div><div class="chart-style-options"><label class="checkbox-label"><input type="checkbox" name="${id}-theme">颜色跟随明暗主题</label><button type="button" class="text-button" data-reset="${id}">恢复默认</button></div>`;
      form.querySelector('.chart-style-rows').append(row);
      row.querySelector('[data-reset]').onclick = () => { fill(id, defaults[id]); preview(); };
    }
    form.oninput = preview; form.onchange = preview;
    const close = () => { if (!saving) { apply(appearance.chartStyles); dialog.close(); } };
    dialog.querySelector('#close-chart-styles').onclick = close;
    dialog.querySelector('#cancel-chart-styles').onclick = close;
    dialog.oncancel = event => { event.preventDefault(); close(); };
    dialog.addEventListener('close', () => apply(appearance.chartStyles));
    form.onsubmit = safe(async event => {
      event.preventDefault(); if (saving) return;
      const next = { ...(appearance.chartStyles || {}) };
      for (const id of Object.keys(defaults)) {
        const value = read(id);
        if (JSON.stringify(value) !== JSON.stringify(resolved(id, initial))) next[id] = value;
      }
      saving = true; for (const control of form.elements) control.disabled = true;
      try { await chooseAppearance({ chartStyles: next }); dialog.close(); toast('曲线样式已保存'); }
      catch (error) { dialog.querySelector('#chart-style-error').textContent = error.message || String(error); preview(); }
      finally { saving = false; for (const control of form.elements) control.disabled = false; for (const id of Object.keys(defaults)) fields(id).color.disabled = fields(id).theme.checked; }
    });
    document.querySelector('#manage-chart-styles').onclick = safe(open);
  }
  document.addEventListener('DOMContentLoaded', initialize, { once: true });
  return { apply, resolved, defaults };
})();
