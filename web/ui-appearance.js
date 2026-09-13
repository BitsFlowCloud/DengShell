'use strict';
window.DengUIAppearance = (() => {
  const defaultID = 'builtin:ui-noto-sans';
  const defaults = { light: '#243b4e', dark: '#d4dfe8' };
  const roles = ['--text', '--text-secondary', '--muted', '--text-faint'];
  const validColor = value => typeof value === 'string' && /^#[\da-f]{6}$/i.test(value);
  let catalog, catalogPromise, dialog, licenseDialog, operation = 0, activeID = defaultID;
  let runtime = window.CLOUDSHELL?.uiFontRuntime || { availableIds: [], activeId: defaultID };
  const faces = new Map();
  const available = id => id.startsWith('builtin:ui-') || runtime.availableIds.includes(id);
  const fonts = () => [...(catalog?.fonts || []), ...managedAssets.filter(a => a.kind === 'ui-font').map(a => ({ ...a, family: `Deng UI custom ${a.id}` }))];
  const resolveID = desired => available(desired) ? desired : available(activeID) ? activeID : defaultID;
  async function initCatalog() {
    if (!catalogPromise) catalogPromise = staticJSON('assets/ui-fonts/catalog.json').then(value => (catalog = value)).catch(error => { catalogPromise = null; throw error; });
    return catalogPromise;
  }
  async function faceFor(font, preview = false) {
    const key = font.id + (preview ? ':preview' : '');
    if (!faces.has(key)) faces.set(key, (async () => {
      const url = preview ? new URL(font.preview, location.href).href : await assetURL(font);
      const face = new FontFace(font.family + (preview ? ' Preview' : ''), `url(${JSON.stringify(url)})`, { weight: '400', display: 'swap' });
      await face.load(); document.fonts.add(face); return face;
    })().catch(error => { faces.delete(key); throw new Error(`界面字体「${font.name}」无法加载：${error.message}`); }));
    return faces.get(key);
  }
  function applyColors(value = appearance) {
    const root = document.documentElement, color = value.uiTextColors?.[root.dataset.theme];
    root.toggleAttribute('data-ui-text-custom', validColor(color));
    for (const role of roles) {
      if (validColor(color)) root.style.setProperty(role, color.toLowerCase()); else root.style.removeProperty(role);
    }
  }
  async function apply(value) {
    const generation = ++operation, desired = value.uiFontId || defaultID;
    applyColors(value);
    try {
      await initCatalog();
      const id = resolveID(desired), font = fonts().find(f => f.id === id) || catalog.fonts[0];
      await Promise.all([faceFor(catalog.fonts[0]), faceFor(font)]);
      if (generation !== operation) return;
      activeID = font.id;
      document.documentElement.style.setProperty('--font-ui', `${JSON.stringify(font.family)}, "Deng UI Sans", "Deng CJK", system-ui, sans-serif`);
      document.documentElement.dataset.uiFont = activeID;
    } catch (error) {
      // Keep the usable face and the saved selection when a resource fails.
      if (generation === operation) toast(error.message);
    }
    if (generation === operation) renderStatus();
  }
  async function acceptConfig() {
    runtime = await api('/api/ui-fonts/runtime');
    activeID = runtime.activeId;
    const remaining = new Set(managedAssets.filter(a => a.kind === 'ui-font').map(a => a.id));
    for (const [id, promise] of faces) if (!id.startsWith('builtin:') && !remaining.has(id)) {
      faces.delete(id); promise.then(face => document.fonts.delete(face)).catch(() => {});
    }
    if (dialog?.open) await render();
  }
  function renderStatus() {
    if (!dialog) return;
    const desired = appearance.uiFontId || defaultID, pending = !available(desired);
    dialog.querySelector('#ui-font-status').textContent = pending
      ? '已保存字体选择。请关闭并重新启动 DengShell 后使用；当前界面继续使用原字体。'
      : `当前界面字体：${fonts().find(f => f.id === activeID)?.name || 'Noto 黑体'}。选择内置字体立即生效并保存。`;
    dialog.querySelector('#ui-font-status').classList.toggle('pending', pending);
    for (const card of dialog.querySelectorAll('[data-ui-font-id]')) {
      const selected = card.dataset.uiFontId === desired;
      card.querySelector('.ui-font-choice').setAttribute('aria-pressed', String(selected));
      card.classList.toggle('selected', selected);
      card.querySelector('.ui-font-state').textContent = selected ? pending ? '已选择 · 重启后生效' : '已选择 ✓' : available(card.dataset.uiFontId) ? '点击使用' : '需重启';
    }
  }
  async function selectFont(font) {
    // A startup-registered face is validated before saving the selection.
    if (available(font.id)) await faceFor(font);
    await chooseAppearance({ uiFontId: font.id });
    runtime = await api('/api/ui-fonts/runtime');
    renderStatus();
  }
  async function showLicense(font) {
    const response = await fetch(font.license);
    if (!response.ok) throw new Error('无法读取字体许可');
    licenseDialog.querySelector('h2').textContent = font.name + ' · 字体许可';
    licenseDialog.querySelector('p').textContent = `来源：${font.upstream}（${font.version}）。${font.licenseName}。程序内字体转换为 WOFF2 并使用 Deng UI 名称，完整保留字形。`;
    licenseDialog.querySelector('pre').textContent = await response.text();
    licenseDialog.showModal();
  }
  async function render() {
    await initCatalog();
    const list = dialog.querySelector('#ui-font-list'); list.replaceChildren();
    for (const font of fonts()) {
      const card = node('article', 'ui-font-card'); card.dataset.uiFontId = font.id;
      const choice = node('button', 'ui-font-choice'); choice.type = 'button';
      const title = node('strong', '', font.name), detail = node('span', 'ui-font-detail', font.description || '导入的界面字体');
      const preview = node('span', 'ui-font-sample', font.preview ? catalog.previewText : '简体中文 · 繁體中文 · English 012345');
      if (font.preview) faceFor(font, true).then(() => { preview.style.fontFamily = `${JSON.stringify(font.family + ' Preview')}, "Deng UI Sans", sans-serif`; }).catch(() => {});
      else if (available(font.id)) preview.style.fontFamily = `${JSON.stringify(font.family)}, "Deng UI Sans", sans-serif`;
      const state = node('span', 'ui-font-state'); choice.append(title, detail, preview, state);
      choice.onclick = safe(async () => { choice.disabled = true; try { await selectFont(font); } finally { choice.disabled = false; } });
      const actions = node('div', 'ui-font-actions');
      if (font.license) {
        const license = node('button', 'text-button', '开源许可 · OFL 1.1'); license.onclick = safe(() => showLicense(font)); actions.append(license);
      } else {
        const rename = node('button', 'text-button', '重命名');
        rename.onclick = safe(async () => {
          const name = await ask({ title: '修改界面字体名称', input: true, value: font.name });
          if (!name?.trim()) return;
          await post(`/api/assets/${font.id}`, { name }); await loadProfiles();
        });
        const del = node('button', 'text-button', '删除');
        del.onclick = safe(async () => {
          if (!await ask({ title: `删除「${font.name}」？`, description: '删除导入的本机副本，原始文件不受影响。', confirm: '删除' })) return;
          await remove(`/api/assets/${font.id}`); await loadProfiles();
        });
        actions.append(rename, del);
      }
      card.append(choice, actions); list.append(card);
    }
    fillColors(); renderStatus();
  }
  function fillColors() {
    if (!dialog) return;
    for (const theme of ['light', 'dark']) {
      const row = dialog.querySelector(`[data-ui-color-theme="${theme}"]`), color = appearance.uiTextColors?.[theme] || defaults[theme];
      row.querySelector('input[type=color]').value = color;
      row.querySelector('input[type=text]').value = color;
      row.querySelector('.ui-color-state').textContent = appearance.uiTextColors?.[theme] ? '已保存自定义颜色' : '使用默认颜色';
      row.querySelector('.ui-color-sample').style.color = color;
    }
  }
  async function saveColor(theme, color) {
    if (color !== null && !validColor(color)) throw new Error('请输入 #RRGGBB 格式的颜色，例如 #243b4e');
    const colors = { ...appearance.uiTextColors };
    if (color === null) delete colors[theme]; else colors[theme] = color.toLowerCase();
    await chooseAppearance({ uiTextColors: colors }); fillColors();
  }
  async function finishImport(asset) {
    if (!asset) return;
    await loadProfiles(); await selectFont({ ...asset, family: `Deng UI custom ${asset.id}` });
    await render(); toast('界面字体已导入并保存，重启 DengShell 后生效');
  }
  async function importFont() {
    const desktop = native();
    if (!desktop?.ChooseAsset) { dialog.querySelector('#ui-font-picker').click(); return; }
    const button = dialog.querySelector('#import-ui-font'); button.disabled = true;
    try { await finishImport(await desktop.ChooseAsset('ui-font')); } finally { button.disabled = false; }
  }
  function initialize() {
    dialog = document.createElement('dialog'); dialog.id = 'ui-appearance-dialog'; dialog.setAttribute('aria-labelledby', 'ui-appearance-title');
    dialog.innerHTML = `<div class="dialog-heading"><div><h2 id="ui-appearance-title">界面字体与文字颜色</h2><p>调整软件界面的标题、正文和说明文字。</p></div><button type="button" class="icon-button" id="close-ui-appearance" aria-label="关闭界面外观设置"><svg><use href="#i-close"/></svg></button></div>
      <section class="ui-text-settings" aria-labelledby="ui-text-title"><h3 id="ui-text-title">文字颜色</h3><p>浅色与深色模式分别保存。每行可预览颜色，点击应用后生效。</p><div id="ui-text-colors"></div></section>
      <section aria-labelledby="ui-font-title"><div class="ui-font-heading"><h3 id="ui-font-title">界面字体</h3><button type="button" class="upload-button" id="import-ui-font">导入字体…</button></div><p>内置 5 套开源字体，支持英文、简体与繁体中文。导入支持 TTF / OTF / WOFF / WOFF2（最大 64 MiB），重启软件后启用。</p><p id="ui-font-status" role="status"></p><div id="ui-font-list"></div><input type="file" id="ui-font-picker" accept=".ttf,.otf,.woff,.woff2" hidden></section>`;
    document.body.append(dialog);
    licenseDialog = document.createElement('dialog'); licenseDialog.id = 'ui-font-license';
    licenseDialog.innerHTML = '<div class="dialog-heading"><h2>字体许可</h2><button type="button" class="icon-button" aria-label="关闭字体许可"><svg><use href="#i-close"/></svg></button></div><p></p><pre tabindex="0"></pre>';
    licenseDialog.querySelector('button').onclick = () => licenseDialog.close(); document.body.append(licenseDialog);
    for (const theme of ['light', 'dark']) {
      const label = theme === 'light' ? '浅色模式' : '深色模式', row = node('div', 'ui-color-row'); row.dataset.uiColorTheme = theme;
      row.innerHTML = `<div class="ui-color-label"><strong>${label}</strong><span class="ui-color-state"></span></div><label class="ui-color-input"><span class="sr-only">${label}文字颜色</span><input type="color" aria-label="${label}文字颜色"></label><input type="text" maxlength="7" spellcheck="false" aria-label="${label}十六进制文字颜色"><button type="button" class="upload-button ui-color-save">应用</button><button type="button" class="text-button ui-color-reset">恢复默认</button><div class="ui-color-sample ${theme}">简体中文 · 繁體中文 · English 012345</div>`;
      const picker = row.querySelector('input[type=color]'), hex = row.querySelector('input[type=text]'), sample = row.querySelector('.ui-color-sample');
      picker.oninput = () => { hex.value = picker.value; sample.style.color = picker.value; };
      hex.oninput = () => { if (validColor(hex.value)) { picker.value = hex.value; sample.style.color = hex.value; } };
      row.querySelector('.ui-color-save').onclick = safe(() => saveColor(theme, hex.value));
      row.querySelector('.ui-color-reset').onclick = safe(() => saveColor(theme, null));
      dialog.querySelector('#ui-text-colors').append(row);
    }
    dialog.querySelector('#close-ui-appearance').onclick = () => dialog.close();
    dialog.querySelector('#import-ui-font').onclick = safe(importFont);
    dialog.querySelector('#ui-font-picker').onchange = safe(async event => {
      const file = event.target.files[0]; event.target.value = ''; if (!file) return;
      if (file.size > 64 * 1024 * 1024) throw new Error('字体文件不能超过 64 MiB');
      const button = dialog.querySelector('#import-ui-font'); button.disabled = true;
      try {
        const data = await new Promise((resolve, reject) => { const reader = new FileReader(); reader.onload = () => resolve(reader.result.split(',')[1]); reader.onerror = () => reject(new Error('无法读取字体文件')); reader.readAsDataURL(file); });
        await finishImport(await post('/api/assets', { kind: 'ui-font', name: file.name.replace(/\.[^.]+$/, ''), filename: file.name, data }));
      } finally { button.disabled = false; }
    });
    document.querySelector('#manage-ui-appearance').onclick = safe(async () => { setSettingsMenu(false); await loadProfiles(); await render(); dialog.showModal(); });
    window.addEventListener('cloudshell:theme', () => applyColors());
  }
  return { initialize, apply, acceptConfig };
})();
