'use strict';

window.DengFontLibrary = (() => {
  let catalogPromise, dialog, kind = 'ui-font', query = '', observer;
  const jobs = new Map(), previewLoads = new Map(), confirming = new Set(), intent = { 'ui-font': 0, font: 0 };
  const bytes = size => size >= 1048576 ? `${(size / 1048576).toFixed(2)} MiB` : `${Math.ceil(size / 1024)} KiB`;
  const installed = font => font.builtinId || managedAssets.filter(a => a.kind === font.kind && a.libraryId === font.id).sort((a,b) => Date.parse(b.createdAt) - Date.parse(a.createdAt))[0]?.id;
  const selected = font => installed(font) === (font.kind === 'ui-font' ? appearance.uiFontId : appearance.fontId);
  const catalog = () => catalogPromise ||= api('/api/font-library').catch(error => { catalogPromise = null; throw error; });
  const delay = ms => new Promise(resolve => setTimeout(resolve, ms));

  function selectionChanged(patch) {
    if (Object.hasOwn(patch, 'uiFontId')) intent['ui-font']++;
    if (Object.hasOwn(patch, 'fontId')) intent.font++;
  }

  async function useFont(font, id = installed(font), stillWanted = null) {
    if (!id) return download(font);
    if (!stillWanted) { const epoch = ++intent[font.kind]; stillWanted = () => intent[font.kind] === epoch; }
    if (font.kind === 'ui-font') {
      if (!await window.DengUIAppearance.useFont(id, stillWanted)) return false;
    }
    else {
      const face = allFonts().find(f => f.id === id);
      if (!face) throw new Error('字体副本不存在，请重新下载');
      await loadFace(face);
      await alignedTerminalFontFamily(face);
      if (!stillWanted()) return false;
      await chooseAppearance({ fontId: id });
    }
    reflect();
    return true;
  }

  async function download(font) {
    if (jobs.has(font.id) || confirming.has(font.id)) return;
    confirming.add(font.id);
    let confirmed;
    try { confirmed = await ask({ title: `下载并使用「${font.name}」？`, description: `${bytes(font.file.size)} · ${font.licenseName}。从 DengShell 官网下载，完成后保存到本机，可离线使用。${font.fallbackNote || ''}`, confirm: '下载并使用' }); }
    finally { confirming.delete(font.id); }
    if (!confirmed) return;
    const selectionKey = font.kind === 'ui-font' ? 'uiFontId' : 'fontId';
    const initialSelection = appearance[selectionKey];
    const epoch = ++intent[font.kind], state = { status: 'starting', received: 0, total: font.file.size, cancelRequested: false };
    jobs.set(font.id, state); reflect();
    try {
      Object.assign(state, await post('/api/font-library/downloads', { fontId: font.id }));
      if (state.cancelRequested) await remove(`/api/font-library/downloads/${state.id}`);
      while (state.status === 'downloading') {
        reflect(); await delay(400);
        Object.assign(state, await api(`/api/font-library/downloads/${state.id}`));
      }
      if (state.status === 'cancelled') { toast('已取消下载，原字体继续使用'); return; }
      if (state.status !== 'complete' || !state.asset?.id) throw new Error(state.error || '字体下载失败，请重试');
      await loadProfiles();
      const stillWanted = () => intent[font.kind] === epoch && appearance[selectionKey] === initialSelection && !state.cancelRequested;
      if (stillWanted() && await useFont(font, state.asset.id, stillWanted)) {
        toast(`已下载并使用「${font.name}」，以后无需重复下载`);
      } else toast(`「${font.name}」已下载，保留当前字体选择`);
    } catch (error) { toast(error.message); }
    finally { jobs.delete(font.id); if (dialog?.open) await render(); }
  }

  async function cancel(font) {
    const job = jobs.get(font.id); if (!job) return;
    job.cancelRequested = true; intent[font.kind]++;
    if (job.id) await remove(`/api/font-library/downloads/${job.id}`);
    reflect();
  }

  function reflect() {
    if (!dialog) return;
    for (const card of dialog.querySelectorAll('[data-library-font]')) {
      const font = card._font, job = jobs.get(font.id), button = card.querySelector('.library-use'), status = card.querySelector('.library-state');
      const cancelButton = card.querySelector('.library-cancel'), progress = card.querySelector('progress');
      card.classList.toggle('selected', selected(font));
      button.disabled = !!job || selected(font);
      button.textContent = job ? '正在下载…' : selected(font) ? '正在使用' : font.builtinId ? '使用内置字体' : installed(font) ? '使用已下载字体' : '下载并使用';
      button.setAttribute('aria-pressed', String(selected(font)));
      cancelButton.hidden = !job; cancelButton.disabled = !!job?.cancelRequested;
      progress.hidden = !job; progress.max = job?.total || 1; progress.value = job?.received || 0;
      status.textContent = job ? job.cancelRequested ? '正在取消…' : `${bytes(job.received || 0)} / ${bytes(job.total)}` : font.builtinId ? '已内置 · 无需下载' : installed(font) ? '已下载 · 可离线使用' : `${bytes(font.file.size)} · ${font.licenseName}`;
    }
  }

  async function preview(card) {
    const font = card._font, img = card.querySelector('img');
    try {
      if (!previewLoads.has(font.id)) previewLoads.set(font.id, api(`/api/font-library/previews/${encodeURIComponent(font.id)}`).catch(error => { previewLoads.delete(font.id); throw error; }));
      const { dataUrl } = await previewLoads.get(font.id);
      if (!card.isConnected) return;
      img.src = dataUrl; img.hidden = false; card.querySelector('.library-preview-message').hidden = true;
    } catch {
      if (!card.isConnected) return;
      const message = card.querySelector('.library-preview-message'); message.replaceChildren(document.createTextNode('预览暂不可用 '));
      const retry = node('button', 'text-button', '重试'); retry.onclick = () => preview(card); message.append(retry);
    }
  }

  function license(font) {
    let panel = document.getElementById('online-font-license');
    if (!panel) {
      panel = node('dialog', 'online-font-license'); panel.id = 'online-font-license';
      panel.innerHTML = '<div class="dialog-heading"><h2></h2><button type="button" class="icon-button" aria-label="关闭字体许可">×</button></div><p></p><a target="_blank" rel="noopener noreferrer">项目来源</a><pre tabindex="0"></pre>';
      panel.querySelector('button').onclick = () => panel.close(); document.body.append(panel);
    }
    panel.querySelector('h2').textContent = `${font.name} · ${font.licenseName}`;
    panel.querySelector('p').textContent = `${font.version} · ${font.fallbackNote}`;
    panel.querySelector('a').href = font.project;
    panel.querySelector('pre').textContent = font.licenseText;
    panel.showModal();
  }

  async function render() {
    const data = await catalog(), list = dialog.querySelector('.library-list');
    observer?.disconnect(); list.replaceChildren();
    const fonts = data.fonts.filter(f => f.kind === kind && `${f.name} ${f.description} ${f.style}`.toLowerCase().includes(query.toLowerCase()));
    dialog.querySelector('.library-count').textContent = `${fonts.length} 款${kind === 'ui-font' ? '界面' : 'Shell'}字体 · 先预览，确认后下载`;
    dialog.querySelectorAll('[data-library-kind]').forEach(button => button.setAttribute('aria-pressed', String(button.dataset.libraryKind === kind)));
    // Only a few small images enter the request queue at a time. Opening the
    // library never preloads entire remote font programs.
    const pending = []; let active = 0;
    const drain = () => { while (active < 2 && pending.length) { const card = pending.shift(); if (!card.isConnected) continue; active++; preview(card).finally(() => { active--; drain(); }); } };
    observer = new IntersectionObserver(entries => { for (const entry of entries) if (entry.isIntersecting) { observer.unobserve(entry.target); pending.push(entry.target); } drain(); }, { root: list, rootMargin: '160px' });
    for (const font of fonts) {
      const card = node('article', 'library-card'); card.dataset.libraryFont = font.id; card._font = font;
      const title = node('strong', '', font.name), detail = node('p', 'library-detail', font.description);
      const image = node('img'); image.alt = `${font.name} 的实际字体样张`; image.hidden = true; image.width = 800; image.height = 200;
      const previewBox = node('div', 'library-preview'), message = node('span', 'library-preview-message', '正在准备样张…'); previewBox.append(image, message);
      const note = node('p', 'library-note', font.fallbackNote), status = node('p', 'library-state');
      const progress = node('progress'); progress.hidden = true; progress.setAttribute('aria-label', `${font.name}下载进度`);
      const actions = node('div', 'library-actions'), use = node('button', 'upload-button library-use'), cancelButton = node('button', 'text-button library-cancel', '取消'), licenseButton = node('button', 'text-button library-license', '来源与许可');
      use.type = cancelButton.type = licenseButton.type = 'button';
      use.onclick = safe(() => useFont(font)); cancelButton.onclick = safe(() => cancel(font)); licenseButton.onclick = () => license(font);
      actions.append(use, cancelButton, licenseButton); card.append(title, detail, previewBox, note, status, progress, actions); list.append(card); observer.observe(card);
      if (installed(font) && !font.builtinId) {
        const repair = node('button', 'text-button', '检查或重新下载'); repair.type = 'button';
        repair.onclick = safe(() => download(font)); actions.append(repair);
      }
    }
    if (!fonts.length) list.append(node('p', 'library-empty', '没有找到匹配的字体，请换个关键词。'));
    reflect();
  }

  function initialize() {
    if (dialog) return;
    dialog = node('dialog', 'font-library-dialog'); dialog.id = 'font-library-dialog'; dialog.setAttribute('aria-labelledby', 'font-library-title');
    dialog.innerHTML = `<div class="dialog-heading"><div><h2 id="font-library-title">在线字体库</h2><p>预览样张从官网下载；完整字体仅在确认后下载并保存在本机。</p></div><button class="icon-button library-close" type="button" aria-label="关闭在线字体库">×</button></div><div class="library-toolbar"><nav class="font-kind-tabs" aria-label="字体类型"><button type="button" data-library-kind="ui-font" aria-pressed="true">界面字体 · 20</button><button type="button" data-library-kind="font" aria-pressed="false">Shell 字体 · 30</button></nav><input type="search" class="library-search" aria-label="搜索在线字体" placeholder="搜索字体名称或风格"></div><p class="library-count" role="status"></p><div class="library-list"></div><footer class="library-footer"><button type="button" class="text-button library-back">返回已安装字体</button><span>字体下载不影响 SSH 连接；离线时仍可使用已安装字体。</span></footer>`;
    dialog.querySelector('.library-close').onclick = () => dialog.close();
    dialog.querySelector('.library-back').onclick = safe(async () => { dialog.close(); if (kind === 'ui-font') await window.DengUIAppearance.open(); else await openAppearance('font'); });
    dialog.querySelectorAll('[data-library-kind]').forEach(button => button.onclick = safe(async () => { kind = button.dataset.libraryKind; await render(); }));
    dialog.querySelector('.library-search').oninput = safe(async event => { query = event.target.value.trim(); await render(); });
    dialog.addEventListener('close', () => observer?.disconnect()); document.body.append(dialog);
  }

  async function open(nextKind) {
    initialize(); kind = nextKind === 'font' ? 'font' : 'ui-font';
    document.getElementById('ui-appearance-dialog')?.close();
    if (document.getElementById('appearance-dialog')?.dataset.kind === 'font') document.getElementById('appearance-dialog').close();
    if (!dialog.open) dialog.showModal();
    try { await loadProfiles(); await render(); }
    catch (error) { dialog.querySelector('.library-count').textContent = `无法读取字体目录：${error.message}`; }
  }
  return { open, selectionChanged, reflect };
})();
