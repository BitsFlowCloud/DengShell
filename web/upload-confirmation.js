/* One upload decision per selection/drop, before any remote file is changed. */
'use strict';
(() => {
  function prepare(state, selection) {
    const result = dialogQueue.then(() => new Promise((resolve, reject) => {
      const dialog = node('dialog', 'upload-confirmation');
      dialog.id = 'upload-confirmation-dialog';
      dialog.setAttribute('aria-labelledby', 'upload-confirmation-title');
      const header = node('div', 'dialog-heading');
      const title = node('h2', '', '正在检查同名文件…'); title.id = 'upload-confirmation-title';
      const close = node('button', 'icon-button'); close.type = 'button'; close.setAttribute('aria-label', '取消上传'); close.append(icon('close'));
      header.append(title, close);
      const destination = node('p', 'upload-confirmation-destination', `${profileFor(state)?.name || state.name || '服务器'} · ${selection.directory || state.cwd || '/'}`);
      const description = node('p', 'upload-confirmation-description', '正在检查目标位置，确认后开始上传。');
      description.setAttribute('role', 'status');
      const list = node('ul', 'upload-conflict-list'); list.hidden = true;
      const more = node('p', 'upload-confirmation-more'); more.hidden = true;
      const buttons = node('div', 'dialog-buttons');
      const cancel = node('button', '', '取消上传');
      const skip = node('button', '', '跳过同名文件'); skip.hidden = true;
      const overwrite = node('button', 'primary-button', '覆盖并上传'); overwrite.hidden = true;
      for (const button of [cancel, skip, overwrite]) button.type = 'button';
      buttons.append(cancel, skip, overwrite); dialog.append(header, destination, description, list, more, buttons);
      document.body.append(dialog);
      let finished = false;
      const controller = new AbortController();
      const finish = (answer, error) => {
        if (finished) return;
        finished = true; controller.abort(); window.removeEventListener('dengshell:locked', locked);
        dialog.close();
        // Toasts move inside the top modal for visibility. Keep the shared
        // live region when disposing this per-batch dialog.
        const notice = dialog.querySelector('#toast');
        if (notice) { notice.hidePopover?.(); document.body.append(notice); }
        dialog.remove(); if (notice) placeToast(true);
        if (answer && (!state.connected || sessions.get(state.id) !== state)) error = new Error('连接已断开，请重新连接后再次选择文件');
        if (error) reject(error); else resolve(answer);
      };
      const locked = () => finish(null);
      close.onclick = cancel.onclick = () => finish(null);
      dialog.oncancel = event => { event.preventDefault(); finish(null); };
      window.addEventListener('dengshell:locked', locked, { once: true });
      if (window.DengSecurityLock?.isLocked()) { finish(null); return; }
      dialog.showModal(); cancel.focus();
      api(`/api/sessions/${state.id}/upload-check`, { method: 'POST', body: JSON.stringify(selection), signal: controller.signal }).then(result => {
        if (finished) return;
        if (!state.connected || sessions.get(state.id) !== state) { finish(null, new Error('连接已断开，请重新连接后再次选择文件')); return; }
        const conflicts = result.conflicts || [];
        if (!conflicts.length) { finish({ overwriteTargets: [], skipTargets: [] }); return; }
        const paths = conflicts.map(item => item.path);
        const blocked = conflicts.some(item => !item.canOverwrite);
        title.textContent = `发现 ${conflicts.length} 个同名项目`;
        description.textContent = blocked ? '目标中包含目录、符号链接或类型不同的项目，不能直接覆盖。可跳过这些同名项目，上传其余文件。' : '以下文件已存在。覆盖会替换远程文件内容，本次选择统一处理。';
        list.hidden = false;
        list.replaceChildren(...conflicts.slice(0, 100).map(item => {
          const row = node('li'); row.append(icon('file'), node('span', '', item.path));
          if (!item.canOverwrite) row.append(node('small', '', '不可覆盖'));
          return row;
        }));
        if (conflicts.length > 100) { more.hidden = false; more.textContent = `仅列出前 100 项，本次选择对全部 ${conflicts.length} 个同名项目生效。`; }
        skip.hidden = false; overwrite.hidden = blocked;
        skip.onclick = () => finish({ overwriteTargets: [], skipTargets: paths });
        overwrite.onclick = () => finish({ overwriteTargets: paths, skipTargets: [] });
        // Do not focus the destructive choice: pressing Enter must not approve it by accident.
        cancel.focus();
      }).catch(error => { if (!finished) finish(null, error); });
    }));
    dialogQueue = result.catch(() => undefined);
    return result;
  }
  const skipped = (target, paths) => {
    for (;;) {
      if (paths.has(target)) return true;
      if (target === '/') return false;
      target = target.slice(0, target.lastIndexOf('/')) || '/';
    }
  };
  window.DengUploadConfirmation = { prepare, skipped };
})();
