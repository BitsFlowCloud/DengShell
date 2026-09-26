// One bounded page per SSH session. A native bridge request cannot be aborted;
// serialize requests and discard stale replies instead of accumulating work.
(() => {
  const views = new Map();
  let active = null;
  const panel = node('section', 'process-view'); panel.id = 'process-view'; panel.hidden = true;
  panel.setAttribute('aria-label', '完整进程列表');
  panel.innerHTML = `<header class="process-view-toolbar"><h2 id="process-view-title">进程</h2><input id="process-view-search" type="search" maxlength="128" aria-label="搜索进程" placeholder="搜索 PID、进程名称或状态"><button id="process-view-pause" type="button">暂停刷新</button><button id="process-view-refresh" type="button">刷新</button></header>
    <div id="process-view-status" class="process-view-status" role="status"></div>
    <div class="process-view-table" tabindex="0" aria-label="进程列表，可横向滚动"><table><thead><tr><th data-sort="pid"><button>PID</button></th><th title="当前页进程的有效用户；无法解析名称时显示 UID">用户</th><th data-sort="memory" title="RSS：驻留物理内存。≈ 表示内核轻量计数的估算值，适用于大量进程；不逐个扫描内存页。"><button>内存 RSS</button></th><th data-sort="cpu" title="采样间隔内的 CPU 使用率，100% 表示占满一个 CPU 核心；首次采样显示 —"><button>CPU</button></th><th data-sort="state"><button>状态</button></th><th data-sort="name"><button>进程名称</button></th></tr></thead><tbody id="process-view-rows"></tbody></table><p id="process-view-empty" hidden>没有匹配的进程</p></div>
    <footer class="process-view-footer"><span id="process-view-count"></span><label>每页 <select id="process-view-size" aria-label="每页进程数量"><option>50</option><option selected>100</option><option>200</option></select></label><button id="process-view-prev" type="button">上一页</button><span id="process-view-page"></span><button id="process-view-next" type="button">下一页</button></footer>`;
  $('.main-panel').append(panel);
  const get = suffix => document.getElementById('process-view-' + suffix);
  const shown = view => active === view.id && views.get(view.id) === view;
  const visible = view => shown(view) && monitorVisible();
  const connected = view => !!sessions.get(view.id)?.connected;
  function create(id) {
    const view = { id, search: '', sort: 'cpu', desc: true, page: 1, size: 100, paused: false, generation: 0, loadedGeneration: -1, data: null, busy: false, queued: false, next: 0, error: '' };
    views.set(id, view); return view;
  }
  function render(view) {
    if (!shown(view)) return;
    const data = view.data, state = sessions.get(view.id);
    get('title').textContent = `进程 · ${profileFor(state)?.name || '服务器'}`;
    if (get('search').value !== view.search) get('search').value = view.search;
    get('size').value = String(view.size);
    get('pause').textContent = view.paused ? '恢复刷新' : '暂停刷新';
    get('pause').setAttribute('aria-pressed', String(view.paused));
    get('refresh').disabled = view.busy || !connected(view);
    const stale = data?.sample?.error;
    let status = !connected(view) ? '连接已断开，保留上次列表' : view.error || stale || (view.busy ? '正在读取进程…' : view.paused ? '已暂停自动刷新' : '自动刷新');
    if (data?.sample?.sampledAt && !data.sample.sampledAt.startsWith('0001-')) status += ` · 采样 ${new Date(data.sample.sampledAt).toLocaleTimeString()}`;
    if (data?.sample?.intervalMilliseconds) status += ` · 间隔 ${Math.ceil(data.sample.intervalMilliseconds / 1000)} 秒`;
    if ((view.error || stale) && data) status += ' · 显示上次成功数据';
    get('status').textContent = status;
    get('status').classList.toggle('has-error', !!(view.error || stale));
    get('count').textContent = data ? (view.search ? `匹配 ${data.matched} / ${data.total} 个进程` : `共 ${data.total} 个进程`) + (data.sample?.unreadable ? ` · ${data.sample.unreadable} 个不可读` : '') : '等待采样';
    get('page').textContent = `${data?.page || 1} / ${data?.pages || 1}`;
    get('prev').disabled = !data || data.page <= 1 || view.busy;
    get('next').disabled = !data || data.page >= data.pages || view.busy;
    for (const th of panel.querySelectorAll('th[data-sort]')) {
      const selected = th.dataset.sort === view.sort;
      th.setAttribute('aria-sort', selected ? (view.desc ? 'descending' : 'ascending') : 'none');
    }
    // Keep the scroll position and selection when only request status changes.
    if (get('rows')._data !== data) {
      get('rows')._data = data;
      get('rows').replaceChildren(...(data?.processes || []).map(p => {
        const row = node('tr'); row.dataset.pid = String(p.pid);
        const stateNames = { R: '运行', S: '睡眠', D: '等待 I/O', T: '停止', t: '跟踪停止', Z: '僵尸', I: '空闲', X: '已退出' };
        const memory = p.memoryReady ? `${p.memoryEstimated ? '≈ ' : ''}${prettySize(p.memory)}` : '—';
        const values = [p.pid, p.userReady ? p.user || String(p.uid) : '—', memory, p.cpuReady ? `${Number(p.cpu).toFixed(1)}%` : '—', `${p.state || '—'}${stateNames[p.state] ? ' · ' + stateNames[p.state] : ''}`, p.name || '—'];
        for (const value of values) { const cell = node('td', '', String(value)); cell.title = cell.textContent; row.append(cell); }
        row.cells[2].title = p.memoryEstimated ? 'RSS 估算值，来自内核轻量计数' : p.memoryReady ? 'RSS 驻留内存' : '内存数据不可用';
        return row;
      }));
    }
    get('empty').hidden = !data || data.processes.length > 0;
    panel.setAttribute('aria-busy', String(view.busy));
  }
  async function request(view) {
    if (!visible(view) || !connected(view)) return;
    if (view.busy) { view.queued = true; return; }
    view.busy = true; view.queued = false;
    const generation = view.generation;
    const params = new URLSearchParams({ page: view.page, pageSize: view.size, sort: view.sort, direction: view.desc ? 'desc' : 'asc', search: view.search });
    render(view);
    try {
      const data = await api(`/api/sessions/${encodeURIComponent(view.id)}/processes?${params}`);
      if (views.get(view.id) !== view || generation !== view.generation) return;
      view.data = data; view.loadedGeneration = generation; view.page = data.page; view.error = '';
      view.next = Date.now() + Math.max(1000, Number(data.nextSampleInMilliseconds) || Number(data.sample?.intervalMilliseconds) || 5000);
    } catch (error) {
      if (views.get(view.id) !== view || generation !== view.generation) return;
      view.error = error.message || '读取进程失败'; view.next = Date.now() + 30000;
    } finally {
      view.busy = false;
      if (views.get(view.id) === view) {
        render(view);
        if (view.queued && visible(view)) { view.queued = false; request(view); }
      }
    }
  }
  function change(view, action) {
    action(view); view.generation++; view.next = 0;
    request(view);
  }
  function open() {
    const state = current();
    if (!state || (!state.connected && !views.has(state.id))) return;
    if (!views.has(state.id)) create(state.id);
    activate(state.id, 'processes');
  }
  const card = $('.process-card'); card.title = '点击查看完整进程列表';
  $('.process-table').setAttribute('aria-label', '资源占用前五的进程，按 Enter 打开完整列表');
  card.addEventListener('click', event => { if (!event.target.closest('button')) open(); });
  card.addEventListener('keydown', event => { if (['Enter', ' '].includes(event.key) && !event.target.closest('button')) { event.preventDefault(); open(); } });
  let searchTimer;
  get('search').oninput = () => {
    clearTimeout(searchTimer);
    const view = views.get(active); if (!view) return;
    view.search = get('search').value; view.page = 1; view.generation++;
    searchTimer = setTimeout(() => request(view), 250);
  };
  get('size').onchange = () => { const view = views.get(active); if (view) change(view, v => { v.size = Number(get('size').value); v.page = 1; }); };
  for (const th of panel.querySelectorAll('th[data-sort]')) th.querySelector('button').onclick = () => {
    const view = views.get(active); if (view) change(view, v => { v.desc = v.sort === th.dataset.sort ? !v.desc : ['cpu', 'memory'].includes(th.dataset.sort); v.sort = th.dataset.sort; v.page = 1; });
  };
  get('pause').onclick = () => {
    const view = views.get(active); if (!view) return;
    view.paused = !view.paused; render(view); if (!view.paused) request(view);
  };
  get('refresh').onclick = () => { const view = views.get(active); if (view) request(view); };
  get('prev').onclick = () => { const view = views.get(active); if (view) change(view, v => { v.page = Math.max(1, v.page - 1); }); };
  get('next').onclick = () => { const view = views.get(active); if (view) change(view, v => { v.page++; }); };
  window.DengProcessView = {
    active: () => active,
    open,
    activate(id, kind) {
      clearTimeout(searchTimer);
      active = kind === 'processes' && views.has(id) ? id : null;
      panel.hidden = !active; $('#workspace').hidden = !!active;
      const view = views.get(active); if (view) { render(view); if (!view.data || view.loadedGeneration !== view.generation || !view.paused) request(view); }
    },
    reflect() { const view = views.get(active); if (view) render(view); },
    drop(id) { views.delete(id); if (active === id) { active = null; panel.hidden = true; $('#workspace').hidden = false; } },
    tabs(state) {
      if (!views.has(state.id)) return [];
      const selected = active === state.id;
      const tab = node('div', `session-tab process-session-tab${selected ? ' active' : ''}`); tab.dataset.processSessionId = state.id;
      const button = node('button'); button.type = 'button'; button.setAttribute('role', 'tab'); button.setAttribute('aria-selected', String(selected));
      const name = `进程 · ${profileFor(state)?.name || '服务器'}`; button.title = name;
      button.append(node('span', 'session-tab-label', name)); button.onclick = () => activate(state.id, 'processes');
      const close = node('button', 'tab-close'); close.type = 'button'; close.append(icon('close')); close.title = '关闭进程列表'; close.setAttribute('aria-label', `关闭 ${name}`);
      close.onclick = () => { const wasActive = active === state.id; this.drop(state.id); if (wasActive) activate(state.id); else renderTabs(); };
      tab.append(button, close); return [tab];
    },
  };
  setInterval(() => { const view = views.get(active); if (view && !view.paused && !view.busy && visible(view) && Date.now() >= view.next) request(view); }, 1000);
})();
