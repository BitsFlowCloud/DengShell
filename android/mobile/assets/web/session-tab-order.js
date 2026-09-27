/* Window-local order for SSH, process and RDP tabs. Never recreates a session. */
'use strict';
(() => {
  const stateKeys = new WeakMap();
  let order = [], sequence = 0;
  const key = tab => tab.dataset.tabKey;
  function sshKey(state) {
    if (Number.isFinite(state.tabOrder)) return `ssh:order:${state.tabOrder}`;
    if (!stateKeys.has(state)) stateKeys.set(state, `ssh:${++sequence}`);
    return stateKeys.get(state);
  }
  function identify(tab) {
    if (tab.classList.contains('rdp-tab')) return `rdp:${tab.dataset.sessionId}`;
    const id = tab.dataset.processSessionId || tab.dataset.sessionId;
    const state = sessions.get(id);
    if (!state) return null;
    return (tab.dataset.processSessionId ? 'process:' : '') + sshKey(state);
  }
  function apply(host) {
    const tabs = [...host.children];
    const seen = new Set();
    // Reconnect can briefly retain both the old view (while draining output)
    // and its replacement. The replacement owns the existing sort position.
    for (const tab of [...tabs].reverse()) {
      let id = identify(tab) || '';
      if (seen.has(id)) id += `:previous:${tab.dataset.sessionId || tab.dataset.processSessionId}`;
      seen.add(id); tab.dataset.tabKey = id;
    }
    const available = new Map(tabs.map(tab => [key(tab), tab]));
    order = order.filter(id => available.has(id));
    for (const tab of tabs) {
      const id = key(tab); if (!id || order.includes(id)) continue;
      const parent = id.startsWith('process:') ? order.indexOf(id.slice(8)) : -1;
      if (parent >= 0) order.splice(parent + 1, 0, id); else order.push(id);
    }
    // Runs after renderTabs has constructed every kind of tab. The same SSH
    // slot keeps its key when connecting/reconnecting replaces its backend ID.
    for (const id of order) host.append(available.get(id));
    for (const tab of tabs) if (!tab.dataset.dragBound) window.DengSessionWindows.bindTab(tab);
  }
  function location(host, x, y) {
    const bounds = host.getBoundingClientRect();
    if (x < bounds.left - 8 || x > bounds.right + 8 || y < bounds.top - 10 || y > bounds.bottom + 12) return null;
    const tabs = [...host.children].filter(tab => key(tab));
    const rows = [];
    for (const tab of tabs) {
      const rect = tab.getBoundingClientRect();
      if (rect.bottom <= bounds.top || rect.top >= bounds.bottom) continue;
      let row = rows.at(-1);
      if (!row || Math.abs(row.top - rect.top) > 2) { row = { top: rect.top, bottom: rect.bottom, items: [] }; rows.push(row); }
      row.items.push({ tab, rect });
    }
    if (!rows.length) return null;
    const distance = row => y < row.top ? row.top - y : y > row.bottom ? y - row.bottom : 0;
    const row = rows.reduce((best, next) => distance(next) < distance(best) ? next : best);
    const next = row.items.find(item => x < (item.rect.left + item.rect.right) / 2);
    const edge = next || row.items.at(-1);
    const before = next?.tab || tabs[tabs.indexOf(edge.tab) + 1] || null;
    return { before: before ? key(before) : null, x: next ? edge.rect.left - 4 : edge.rect.right + 4, y: Math.max(row.top, bounds.top), height: Math.min(row.bottom, bounds.bottom) - Math.max(row.top, bounds.top) };
  }
  function commit(tab, position) {
    if (!position || !tab.isConnected || !key(tab) || position.before === key(tab)) return;
    const host = tab.parentElement;
    const before = position.before ? [...host.children].find(item => key(item) === position.before) : null;
    if (position.before && !before) return;
    const focused = document.activeElement;
    host.insertBefore(tab, before);
    order = [...host.children].map(key).filter(Boolean);
    if (tab.contains(focused)) focused.focus({ preventScroll: true });
  }
  window.DengTabOrder = { apply, location, commit };
})();
