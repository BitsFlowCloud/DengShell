'use strict';

// Separate SFTP tree cache: browsing files never replaces the root directory tree.
// The application supplies current(), api(), navigate(), node(), icon() and safe().
window.DengFileBrowser = (() => {
  const trees = new WeakMap();
  const rootPath = '/';
  const joinPath = (parent, name) => `${parent === '/' ? '' : parent}/${name}`;
  const isAncestor = (parent, path) => parent === '/' ? path !== '/' : path.startsWith(parent + '/');
  const isCurrent = state => current() === state;

  function treeFor(state) {
    let tree = trees.get(state);
    if (!tree) {
      tree = { folders: new Map(), expanded: new Set([rootPath]), pending: new Map(), errors: new Map(), invalidated: new Set() };
      trees.set(state, tree);
    }
    return tree;
  }

  async function readDirectory(state, path, force = false) {
    if (!state?.connected) return;
    const tree = treeFor(state);
    if (tree.pending.has(path)) {
      if (force) tree.invalidated.add(path);
      return tree.pending.get(path);
    }
    if (!force && tree.folders.has(path)) return tree.folders.get(path);
    tree.errors.delete(path);
    const request = (async () => {
      try {
        const result = await api(`/api/sessions/${state.id}/files?path=${encodeURIComponent(path)}&directories=1`);
        const folders = (result.entries || []).filter(entry => entry.kind === 'folder').sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }));
        tree.folders.set(path, folders);
        return folders;
      } catch (error) {
        tree.errors.set(path, error?.message || String(error));
      } finally {
        tree.pending.delete(path);
        if (tree.invalidated.delete(path) && state.connected) void readDirectory(state, path, true);
        if (isCurrent(state)) render(state);
      }
    })();
    tree.pending.set(path, request);
    return request;
  }

  function loadVisible(state) {
    if (!state?.connected) return;
    const tree = treeFor(state);
    if (!tree.folders.has(rootPath) && !tree.pending.has(rootPath) && !tree.errors.has(rootPath)) void readDirectory(state, rootPath);
  }

  function invalidate(state, path) {
    if (!state) return;
    const tree = treeFor(state);
    if (tree.expanded.has(path) && tree.folders.has(path)) {
      void readDirectory(state, path, true);
    } else if (!tree.pending.has(path)) {
      tree.folders.delete(path);
      tree.errors.delete(path);
    } else {
      tree.invalidated.add(path);
    }
  }

  function render(state = current()) {
    const container = document.getElementById('directory-tree');
    if (!container) return;
    container.tabIndex = 0;
    container.setAttribute('aria-label', '服务器目录，独立滚动');
    if (!state) {
      container.replaceChildren(node('div', 'tree-placeholder', '连接后显示服务器目录'));
      delete container.dataset.session;
      return;
    }
    const tree = treeFor(state);
    loadVisible(state);
    const sameSession = container.dataset.session === state.id;
    const previousScroll = sameSession ? { top: container.scrollTop, left: container.scrollLeft } : { top: 0, left: 0 };
    const focused = container.contains(document.activeElement) ? document.activeElement?.dataset.treeFocus : '';
    const fragment = document.createDocumentFragment();

    function message(path, depth) {
      const error = tree.errors.get(path);
      const loading = tree.pending.has(path);
      if (!error && !loading) return;
      const line = node('div', 'tree-directory-status');
      line.style.setProperty('--depth', depth);
      if (error) {
        const retry = node('button', 'tree-retry', '读取失败 · 重试');
        retry.type = 'button';
        retry.title = error;
        retry.disabled = !state.connected;
        retry.dataset.treeFocus = `retry:${path}`;
        retry.onclick = () => { void readDirectory(state, path, true); if (isCurrent(state)) render(state); };
        line.append(retry);
      } else {
        line.textContent = '正在读取…';
        line.setAttribute('role', 'status');
      }
      fragment.append(line);
    }

    function addBranch(path, name, depth, link = false) {
      const expanded = tree.expanded.has(path);
      const selected = state.cwd === path;
      const containsCurrent = !selected && isAncestor(path, state.cwd || '/') && !expanded;
      const row = node('div', `tree-row${selected ? ' selected' : ''}${containsCurrent ? ' contains-current' : ''}`);
      row.style.setProperty('--depth', depth);
      row.dataset.path = path;
      const toggle = node('button', 'tree-toggle');
      toggle.type = 'button';
      toggle.disabled = !state.connected || depth >= 24;
      toggle.setAttribute('aria-expanded', String(expanded));
      toggle.setAttribute('aria-label', `${expanded ? '折叠' : '展开'} ${path}`);
      toggle.dataset.treeFocus = `toggle:${path}`;
      toggle.append(icon('chevron', `tree-chevron${expanded ? ' expanded' : ''}`));
      toggle.onclick = () => {
        if (tree.expanded.has(path)) tree.expanded.delete(path);
        else {
          tree.expanded.add(path);
          void readDirectory(state, path, tree.errors.has(path));
        }
        if (isCurrent(state)) render(state);
      };
      const button = node('button', 'tree-link');
      button.type = 'button';
      button.title = containsCurrent ? `${path}\n当前目录：${state.cwd}` : path;
      button.disabled = !state.connected;
      button.dataset.treeFocus = `link:${path}`;
      if (selected) button.setAttribute('aria-current', 'location');
      button.append(icon('folder', 'folder-icon'), node('span', '', name));
      if (link) button.append(node('span', 'tree-symlink', '↗'));
      button.onclick = safe(() => navigate(path, state));
      row.append(toggle, button);
      fragment.append(row);
      if (!expanded) return;
      message(path, depth + 1);
      const children = tree.folders.get(path);
      if (children?.length === 0 && !tree.pending.has(path) && !tree.errors.has(path)) {
        const empty = node('div', 'tree-directory-status', '无子目录');
        empty.style.setProperty('--depth', depth + 1);
        fragment.append(empty);
      }
      if (depth < 24) for (const child of children || []) addBranch(joinPath(path, child.name), child.name, depth + 1, child.link);
    }

    addBranch(rootPath, '/', 0);
    container.replaceChildren(fragment);
    container.dataset.session = state.id;
    if (focused) [...container.querySelectorAll('[data-tree-focus]')].find(el => el.dataset.treeFocus === focused)?.focus({ preventScroll: true });
    container.scrollTop = previousScroll.top;
    container.scrollLeft = previousScroll.left;
  }

  function initializeScrollRegions() {
    const parent = document.querySelector('.file-content');
    if (parent) {
      parent.removeAttribute('tabindex');
      parent.setAttribute('aria-label', '服务器目录与文件');
    }
    for (const element of document.querySelectorAll('.directory-tree, .file-table-wrap')) {
      element.tabIndex = 0;
      element.setAttribute('aria-label', element.classList.contains('directory-tree') ? '服务器目录，独立滚动' : '当前目录文件，独立滚动');
      let scrollTimer;
      element.addEventListener('scroll', () => {
        element.classList.add('is-scrolling');
        clearTimeout(scrollTimer);
        scrollTimer = setTimeout(() => element.classList.remove('is-scrolling'), 650);
      }, { passive: true });
    }
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', initializeScrollRegions, { once: true });
  else initializeScrollRegions();
  return { render, invalidate, prime: loadVisible };
})();
