'use strict';

let terminalMenuSession = null, historySession = null, quitListenerReady = false;
let diagnosticView = { generation: 0, id: '', sessionID: '', direction: 'local', target: '', report: null, timer: null };
function historyKey(profileID) { return `dengshell.history.${profileID}`; }
const commandHistoryQueues = new Map(), commandHistoryVersions = new Map();
function getCommandHistory(profileID) { const entries = readSaved(historyKey(profileID), []); return Array.isArray(entries) ? entries.filter(item => typeof item === 'string').slice(-200) : []; }
function showCommandHistory(profileID, entries) {
  window.DengPortablePreferences.cache(historyKey(profileID), entries);
  for (const state of sessions.values()) if (state.profileId === profileID) { state.history = entries; state.historyIndex = entries.length; }
  if (historySession?.profileId === profileID) { historySession.history = entries; historySession.historyIndex = entries.length; }
  if ($('#history-dialog').open && historySession?.profileId === profileID) renderCommandHistory();
}
function acceptCommandHistory(profileID, result) {
  if (result.global) acceptGlobalCommandHistory(result.global);
  if (result.revision < (commandHistoryVersions.get(profileID) || 0)) return;
  commandHistoryVersions.set(profileID, result.revision);
  let entries = [...result.entries];
  // Keep commands awaiting acknowledgement visible without ever posting this
  // reconstructed array to the server.
  for (const job of commandHistoryQueues.get(profileID)?.jobs || []) {
    if (job.clear) entries = []; else if (entries.at(-1) !== job.command) entries.push(job.command);
  }
  showCommandHistory(profileID, entries.slice(-200));
}
function pumpCommandHistory(profileID) {
  const queue = commandHistoryQueues.get(profileID);
  if (!queue || !queue.jobs.length) return Promise.resolve();
  if (queue.running) return queue.running;
  queue.running = (async () => {
    while (queue.jobs.length) {
      const job = queue.jobs[0];
      const result = await api('/api/profiles/' + encodeURIComponent(profileID) + '/history', { method: job.clear ? 'DELETE' : 'POST', body: JSON.stringify(job) });
      queue.jobs.shift(); acceptCommandHistory(profileID, result);
    }
  })().finally(() => { queue.running = null; });
  return queue.running;
}
function enqueueCommandHistory(profileID, command, clear = false) {
  let queue = commandHistoryQueues.get(profileID);
  if (!queue) { queue = { jobs: [], running: null }; commandHistoryQueues.set(profileID, queue); }
  if (clear) queue.jobs = queue.running && queue.jobs.length ? [queue.jobs[0]] : [];
  queue.jobs.push({ operation: crypto.randomUUID(), command, clear });
  let entries = clear ? [] : getCommandHistory(profileID);
  if (!clear && entries.at(-1) !== command) entries.push(command);
  showCommandHistory(profileID, entries.slice(-200));
  return pumpCommandHistory(profileID);
}
function recordCommand(state, body) {
  if (!state || !body.trim()) return;
  enqueueCommandHistory(state.profileId, body).catch(error => toast(`命令历史未能保存，移动或退出窗口时将重试：${error.message || error}`));
}
async function refreshCommandHistory(profileID) {
  await pumpCommandHistory(profileID);
  acceptCommandHistory(profileID, await api('/api/profiles/' + encodeURIComponent(profileID) + '/history'));
}
window.DengCommandHistory = {
  refresh: refreshCommandHistory,
  flush: async () => { await Promise.all([...commandHistoryQueues.keys()].map(pumpCommandHistory)); },
  acceptConfig: config => { if (config.commandHistory) acceptGlobalCommandHistory(config.commandHistory); },
};
let globalCommandHistory = { entries: [], revision: -1 }, globalHistoryRequest = null, historyClearOperation = null;
function acceptGlobalCommandHistory(result) {
  if (!result || !Array.isArray(result.entries) || result.revision < globalCommandHistory.revision) return;
  globalCommandHistory = { entries: [...result.entries], revision: result.revision };
  if ($('#history-dialog').open) renderCommandHistory();
}
function refreshGlobalCommandHistory() {
  if (globalHistoryRequest) return globalHistoryRequest;
  globalHistoryRequest = api('/api/history').then(acceptGlobalCommandHistory).finally(() => { globalHistoryRequest = null; });
  return globalHistoryRequest;
}
async function clearGlobalCommandHistory() {
  await window.DengCommandHistory.flush();
  historyClearOperation ||= crypto.randomUUID();
  const result = await api('/api/history', { method: 'DELETE', body: JSON.stringify({ operation: historyClearOperation }) });
  historyClearOperation = null; acceptGlobalCommandHistory(result);
}
async function copyText(text) { if (native()?.WriteClipboard) await native().WriteClipboard(text); else await navigator.clipboard.writeText(text); }
async function pasteClipboard(state) {
  if (!state?.ready) return;
  const text = native()?.ReadClipboard ? await native().ReadClipboard() : await navigator.clipboard.readText();
  if (!sessions.has(state.id) || !state.ready) return;
  pasteTerminalText(state, text);
}
async function copyTerminalSelection(state) {
  const selection = state?.term.getSelection(); if (!selection) return;
  await copyText(selection); toast('选中文字已复制');
}
function bindTerminalContext(state) {
  state.host.addEventListener('contextmenu', event => {
    event.preventDefault(); event.stopPropagation(); terminalMenuSession = state;
    const menu = $('#terminal-menu'); menu.hidden = false;
    $('#copy-terminal-selection').disabled = !state.term.hasSelection(); $('#paste-terminal-clipboard').disabled = !state.ready;
    menu.style.left = `${Math.max(8, Math.min(event.clientX / effectiveScale, logicalWidth() - menu.offsetWidth - 8))}px`;
    menu.style.top = `${Math.max(8, Math.min(event.clientY / effectiveScale, logicalHeight() - menu.offsetHeight - 8))}px`;
    (state.term.hasSelection() ? $('#copy-terminal-selection') : $('#paste-terminal-clipboard')).focus();
  });
}
function renderCommandHistory() {
  const state = historySession, query = $('#history-filter').value.trim().toLowerCase();
  const entries = [...globalCommandHistory.entries].reverse().filter(body => body.toLowerCase().includes(query));
  $('#history-target').textContent = state ? `所有连接的命令 · 执行目标：${profileFor(state)?.name || '当前会话'}` : '所有连接的命令 · 连接服务器后可填入或执行';
  $('#history-empty').hidden = entries.length > 0; $('#clear-history').disabled = !globalCommandHistory.entries.length;
  $('#history-list').replaceChildren(...entries.map(body => {
    const row = node('div', 'history-entry');
    const button = node('button', 'history-fill'); button.type = 'button'; button.title = `填入输入条\n${body}`; button.append(icon('history'), node('code', '', body));
    button.disabled = !state || !sessions.has(state.id);
    button.onclick = () => {
      if (!state || !sessions.has(state.id)) return toast('此会话已关闭');
      activate(state.id); $('#command-input').value = body; resizeCommandInput(); state.historyIndex = state.history.length;
      $('#history-dialog').close(); $('#command-input').focus();
    };
    const label = body.split('\n')[0].slice(0, 100);
    const copy = node('button', 'history-copy', '复制'); copy.type = 'button'; copy.title = '复制完整命令'; copy.setAttribute('aria-label', `复制历史命令：${label}`);
    copy.onclick = safe(async event => { event.stopPropagation(); await copyText(body); toast('命令已复制'); });
    const execute = node('button', 'history-execute', '执行'); execute.type = 'button'; execute.title = `在 ${profileFor(state)?.name || '原会话'} 中执行`; execute.setAttribute('aria-label', `执行历史命令：${label}`);
    execute.disabled = !state?.connected || !state?.ready || sessions.get(state.id) !== state;
    execute.onclick = event => {
      event.stopPropagation();
      if (!state?.connected || !state.ready || sessions.get(state.id) !== state) return toast('此会话已断开，请重新连接');
      $('#history-dialog').close(); activate(state.id); pasteTerminalText(state, body, { execute: true });
    };
    row.append(button, copy, execute); return row;
  }));
}
function resizeCommandInput() { const input = $('#command-input'); input.style.height = 'auto'; input.style.height = `${Math.min(76, input.scrollHeight)}px`; }
function openCommandHistory() { historySession = current(); $('#history-filter').value = ''; renderCommandHistory(); $('#history-dialog').showModal(); $('#history-filter').focus(); refreshGlobalCommandHistory().catch(error => toast(`命令历史刷新失败：${error.message || error}`)); }
function compactDiskSize(bytes) { const unit = bytes >= 1024 ** 4 ? 'T' : 'G'; return `${(bytes / 1024 ** (unit === 'T' ? 4 : 3)).toFixed(1)}${unit}`; }
function processMemoryText(process) {
  if (!process.memoryReady || process.memoryEstimated || !Number.isSafeInteger(process.memory) || process.memory < 0) return '—';
  for (const [power,unit] of [[4,'TiB'],[3,'GiB'],[2,'MiB'],[1,'KiB']]) if (process.memory >= 1024 ** power) return `${(process.memory / 1024 ** power).toFixed(2)} ${unit}`;
  return `${process.memory} B`;
}
function processOrder(state = current()) { const saved = state?.processSort || readSaved('dengshell.workspace', {}).processSort; return { key: saved?.key === 'memory' ? 'memory' : 'cpu', ascending: false }; }
function rememberProcessSample(state, stats) {
  const sample = stats?.processSample;
  // An unavailable response must not erase the last successful top-five sample.
  if (!state || !sample?.available || sample.paused) return;
  state.processSnapshot = { processes: [...(stats.processes || [])], memoryTop: [...(stats.processMemoryTop || stats.processes || [])], sample: { ...sample }, sampledAt: sample.sampledAt || stats.sampledAt };
}
function renderProcesses(stats) {
  const state = current();
  rememberProcessSample(state, stats);
  const snapshot = state?.processSnapshot, sample = snapshot?.sample;
  const order = processOrder(), items = [...(order.key === 'memory' ? snapshot?.memoryTop || snapshot?.processes || [] : snapshot?.processes || [])];
  const valid = (process, key) => key === 'cpu' ? process.cpuReady === true : process.memoryReady === true;
  items.sort((a, b) => {
    const key = order.key || 'cpu', av = valid(a,key), bv = valid(b,key);
    if (av !== bv) return av ? -1 : 1;
    const difference = av ? a[key] - b[key] : 0;
    if (difference) return (order.key && order.ascending ? 1 : -1) * difference;
    return (b.memory || 0) - (a.memory || 0) || (a.pid || 0) - (b.pid || 0);
  });
  items.splice(5); // Also bound old snapshots received from an existing window.
  const hasSample = !!snapshot, attempted = stats?.processSample;
  $('#process-count').textContent = hasSample ? `${attempted?.error && !attempted.available ? '采集失败，保留缓存 · ' : ''}${order.key === 'memory' ? '内存' : 'CPU'} 前 ${items.length}` : state?.connected ? attempted?.error && !attempted.paused ? '暂不可用' : '采样中…' : '等待连接';
  const sampledAt = snapshot?.sampledAt && Number.isFinite(Date.parse(snapshot.sampledAt)) ? new Date(snapshot.sampledAt).toLocaleString() : '';
  const interval = (attempted?.intervalMilliseconds || sample?.intervalMilliseconds || 5000) / 1000;
  $('#process-count').title = [
    `窗口可见时自动采集并常驻显示前 5 个；约每 ${interval} 秒更新一次`,
    sampledAt ? `最近采样：${sampledAt}` : '尚未采集进程列表',
    sample ? `已读取 ${sample.readable} / 可见 ${sample.visible} 个进程${sample.unreadable ? `；${sample.unreadable} 个无权限或已退出` : ''}${sample.elapsedSeconds ? `；CPU 采样间隔 ${sample.elapsedSeconds.toFixed(2)} 秒` : ''}` : '',
    '内存先用内核计数筛选候选，再独立精读 RSS；悬停数值可查看字节数及精读时间。候选排名不是全机同一时刻的精确内存快照。',
    attempted?.error || sample?.error || '',
  ].filter(Boolean).join('\n');
  $('#monitor-state').textContent = state?.connected ? '' : '待连接';
  $('#monitor-state').hidden = !!state?.connected;
  $('.process-details').dataset.sampleState = hasSample ? 'sampled' : state?.connected ? 'pending' : 'uncollected';
  for (const key of ['cpu','memory']) {
    const button = $(`#sort-process-${key}`), selected = order.key === key;
    button.querySelector('span').textContent = selected ? order.ascending ? '↑' : '↓' : '';
    button.closest('th').setAttribute('aria-sort', selected ? order.ascending ? 'ascending' : 'descending' : 'none');
  }
  const scroll = $('.process-table'), previousTop = scroll.scrollTop;
  $('#process-list').replaceChildren(...items.map(process => {
    const row = node('tr'), memory = node('td', '', processMemoryText(process)), cpu = node('td', '', process.cpuReady ? `${process.cpu.toFixed(2)}%` : '—'), name = node('td', '', process.name);
    row.dataset.pid = process.pid;
    memory.title = memory.textContent === '—' ? process.memoryError || '等待精确内存采样' : [
      `驻留内存 RSS：${process.memory.toLocaleString('en-US')} 字节`,
      `读取来源：${process.memorySource}`,
      process.memorySampledAt ? `采样时间：${new Date(process.memorySampledAt).toLocaleString()}` : '',
      process.memoryError || '',
    ].filter(Boolean).join('\n');
    cpu.title = process.cpuReady ? `${process.cpu.toFixed(4)}% · 根据内核累计 CPU 时间差计算\n单核满载为 100%，多线程可超过 100%；显示采样间隔内的平均占用` : '等待同一进程的下一次采样';
    name.title = `${process.name}${process.pid ? ` · PID ${process.pid}` : ''}`;
    row.append(memory,cpu,name); return row;
  }));
  scroll.scrollTop = previousTop;
}
function sortProcesses(key, reset = false) {
  const state = current(); if (!state) return;
  state.processSort = { key: reset ? 'cpu' : key, ascending:false };
  save('dengshell.workspace', { ...readSaved('dengshell.workspace', {}), processSort: state.processSort });
  $('.process-table').scrollTop = 0; renderProcesses(state.stats);
}
const trafficWindowMilliseconds = 60000;
function rememberNetworkSample(state, stats) {
  state.interfaceCharts ||= new Map();
  // Sampling belongs to the SSH session, not the selected UI tab. Merge the
  // backend's real rolling history when this tab becomes visible again.
  const frames = [...(Array.isArray(stats.history) ? stats.history : []), stats]
    .map(frame => ({ ...frame, time: Date.parse(frame.sampledAt) }))
    .filter(frame => Number.isFinite(frame.time) && frame.time > 0).sort((a,b) => a.time-b.time);
  if (!frames.length) return;
  let latest = frames.at(-1).time;
  const charts = new Map();
  for (const [name,samples] of state.interfaceCharts) {
    const records = new Map();
    for (const sample of samples) { const time = Date.parse(sample.sampledAt); if (Number.isFinite(time)) { records.set(time,sample); latest = Math.max(latest,time); } }
    charts.set(name,records);
  }
  for (const frame of frames) {
    const interfaces = new Map((frame.interfaces || []).map(iface => [iface.name,iface]));
    for (const name of new Set([...charts.keys(),...interfaces.keys()])) {
      const iface = interfaces.get(name), records = charts.get(name) || new Map();
      // Missing/error frames and removed interfaces are explicit gaps. Never
      // invent a zero rate or join across an unavailable remote observation.
      records.set(frame.time, { rx:iface?.ready ? iface.rx : null, tx:iface?.ready ? iface.tx : null, rxBytes:iface?.rxBytes, txBytes:iface?.txBytes, elapsedMilliseconds:frame.elapsedMilliseconds, sampledAt:frame.sampledAt });
      charts.set(name,records);
    }
  }
  for (const [name,records] of charts) {
    const fresh = [...records.entries()].filter(([time]) => time > latest-trafficWindowMilliseconds).sort((a,b) => a[0]-b[0]).slice(-90).map(([,sample]) => sample);
    if (fresh.some(sample => Number.isFinite(sample.rxBytes) || Number.isFinite(sample.txBytes) || Number.isFinite(sample.rx) || Number.isFinite(sample.tx))) state.interfaceCharts.set(name,fresh);
    else state.interfaceCharts.delete(name);
  }
}
function trafficChartModel(samples, now = Date.now(), scale = null) {
  const points = (samples || []).map(sample => ({...sample,time:Date.parse(sample.sampledAt)})).filter(sample => Number.isFinite(sample.time) && sample.time > now - trafficWindowMilliseconds && sample.time <= now).sort((a,b) => a.time-b.time);
  const rates = points.flatMap(sample => [sample.tx,sample.rx]).filter(value => Number.isFinite(value) && value >= 0);
  if (!rates.length) { if (scale) { scale.axis = null; scale.pending = null; } return {points,axis:null,now}; }
  const peak = Math.max(...rates), units = ['B/s','KiB/s','MiB/s','GiB/s'];
  const unitIndex = Math.min(units.length-1, Math.max(0, Math.floor(Math.log(Math.max(1,peak))/Math.log(1024))));
  const divisor = 1024 ** unitIndex, padded = Math.max(1,peak * 1.1) / divisor, base = 10 ** Math.floor(Math.log10(padded));
  const factor = [1,1.2,1.5,2,2.5,3,4,5,6,8,10].find(value => value * base >= padded) || 10, upper = factor * base;
  const format = value => Number(value.toPrecision(6)).toString();
  const candidate = {max:upper * divisor,unit:units[unitIndex],labels:[format(upper),format(upper/2),'0']};
  if (!scale) return {points,axis:candidate,now};
  if (!scale.axis || candidate.max > scale.axis.max) { scale.axis = candidate; scale.pending = null; }
  else if (candidate.max < scale.axis.max) {
    // Require five seconds below the old scale, retaining the largest candidate
    // seen during that interval. Changing low-rate buckets cannot pin an old MB scale.
    if (!scale.pending) scale.pending = {since:now,axis:candidate};
    else if (candidate.max > scale.pending.axis.max) scale.pending.axis = candidate;
    if (now - scale.pending.since >= 5000) { scale.axis = scale.pending.axis; scale.pending = null; }
  } else scale.pending = null;
  return {points,axis:scale.axis,now};
}
function trafficSamplesContinuous(previous, point) {
  const gap = point.time - previous.time;
  if (gap <= 0 || gap > 15500) return false;
  const elapsed = point.elapsedMilliseconds;
  if (!Number.isFinite(elapsed)) return gap <= 1800;
  if (elapsed <= 0 || elapsed > 15000) return false;
  // The backend calculates each rate from the preceding remote uptime/counter
  // pair. Matching those same counters proves no observation was skipped here,
  // without mistaking a slow SSH response for an absent measurement. Check both
  // directions, since either one may legitimately have a flat zero-byte period.
  const knownCounters = ['rx','tx'].every(key => Number.isFinite(point[key]) && point[key] >= 0 && Number.isFinite(previous[key+'Bytes']) && previous[key+'Bytes'] >= 0 && Number.isFinite(point[key+'Bytes']) && point[key+'Bytes'] >= 0);
  if (knownCounters) return ['rx','tx'].every(key => {
    const delta = point[key+'Bytes'] - previous[key+'Bytes'], expected = point[key] * elapsed / 1000;
    const precision = Math.max(previous[key+'Bytes'],point[key+'Bytes']) * Number.EPSILON * 2;
    return delta >= 0 && Math.abs(delta-expected) <= Math.max(2, Math.abs(expected) * 1e-6, precision);
  });
  // Older detached-window snapshots have no counters. Allow bounded delivery
  // jitter only around an explicitly measured interval; never bridge long gaps.
  return elapsed <= 3000 && gap <= elapsed + 500;
}
function trafficChartRuns(points, key, x, y) {
  const runs = []; let run = [], previous;
  for (const point of points) {
    if (!Number.isFinite(point[key]) || point[key] < 0 || previous && !trafficSamplesContinuous(previous,point)) { if (run.length) runs.push(run); run = []; }
    if (Number.isFinite(point[key]) && point[key] >= 0) run.push({x:x(point.time),y:y(point[key])});
    previous = point;
  }
  if (run.length) runs.push(run); return runs;
}
function renderTrafficChart(host, samples) {
  if (!host) return;
  host._trafficSamples = samples;
  if (!host._trafficObserver) {
    host._trafficObserver = new ResizeObserver(() => { cancelAnimationFrame(host._trafficFrame); host._trafficFrame = requestAnimationFrame(() => renderTrafficChart(host,host._trafficSamples)); });
    host._trafficObserver.observe(host);
  }
  const state = current(), key = `${state?.id || ''}/${state?.networkInterface || ''}`;
  if (host._trafficKey !== key) { host._trafficKey = key; host._trafficScale = {}; }
  const width = Math.max(100,host.clientWidth), height = 65, top = 19, bottom = 47, {points,axis,now} = trafficChartModel(samples,Date.now(),host._trafficScale ||= {});
  const labels = axis?.labels || ['—','—','—'], left = Math.max(35,...labels.map(label => label.length * 5.6 + 10)), right = width - 3, plotWidth = Math.max(1,right-left);
  const element = (tag,attrs = {},text) => { const node = document.createElementNS('http://www.w3.org/2000/svg',tag); for (const [key,value] of Object.entries(attrs)) node.setAttribute(key,String(value)); if (text != null) node.textContent = text; return node; };
  host.setAttribute('viewBox',`0 0 ${width} ${height}`); host.replaceChildren(); host.dataset.scaleMax = axis?.max ?? ''; host.dataset.trafficUnit = axis?.unit || '';
  host.setAttribute('aria-label',`最近60秒网卡流量，上行和下行使用各自设定的线条样式，两者使用同一刻度${axis ? `，零至${labels[0]} ${axis.unit}` : ''}；点击运行 MTR 路径诊断`);
  host.setAttribute('title','刻度单位 B/s、KiB/s、MiB/s、GiB/s（1024进位）；最近60秒真实采样；延迟返回的计数按实际间隔求平均，真实失败保留缺口；新峰立即扩大刻度，旧峰移出后稳定5秒再缩小。上传和下载共用零起点刻度；点击运行 MTR 路径诊断');
  const grid = element('g',{class:'traffic-grid'});
  grid.append(element('text',{x:left-8,y:9,'text-anchor':'end',class:'traffic-unit'},axis?.unit || 'B/s'));
  [top,(top+bottom)/2,bottom].forEach((y,index) => { grid.append(element('line',{x1:left,y1:y,x2:right,y2:y}),element('text',{x:left-8,y:y+3,'text-anchor':'end'},labels[index])); });
  grid.append(element('text',{x:left,y:61,class:'traffic-time'},'60秒前'),element('text',{x:right,y:61,'text-anchor':'end',class:'traffic-time'},'现在')); host.append(grid);
  if (!axis) { host.append(element('text',{x:left+plotWidth/2,y:33,'text-anchor':'middle',class:'traffic-empty'},state?.connected ? '等待流量采样' : '连接后显示流量')); return; }
  const x = time => left + (time - (now - trafficWindowMilliseconds)) / trafficWindowMilliseconds * plotWidth, y = value => bottom - value / axis.max * (bottom-top);
  for (const key of ['tx','rx']) {
    const series = element('g',{class:`traffic-series traffic-${key}`,'data-series':key});
    for (const runPoints of trafficChartRuns(points,key,x,y)) {
      const first = runPoints[0], last = runPoints.at(-1);
      if (runPoints.length === 1) { series.append(element('circle',{cx:first.x,cy:first.y,r:key === 'tx' ? 2.8 : 1.5,class:'traffic-point'})); continue; }
      const d = window.DengLatencyChart.curve(runPoints);
      series.append(element('path',{d:`${d}L${last.x} ${bottom}L${first.x} ${bottom}Z`,class:'traffic-area'}),element('path',{d,class:'traffic-line'}));
    }
    host.append(series);
  }
}
function currentNetworkInterfaces(state, stats = state?.stats) {
  const metadata = new Map((stats?.interfaces || []).map(iface => [iface.name,iface]));
  const live = state?.networkStats;
  if (!live) return [...metadata.values()].map(iface => ({...iface,ready:false}));
  const freshMetadata = Date.parse(live.metadataSampledAt) >= Date.parse(stats?.metadataSampledAt);
  return (live.interfaces || []).map(iface => {
    const detail = metadata.get(iface.name);
    return {...iface,...(!freshMetadata && detail ? detail : {}),name:iface.name,rx:iface.rx,tx:iface.tx,rxBytes:iface.rxBytes,txBytes:iface.txBytes,ready:iface.ready};
  });
}
function compactTrafficSize(bytes) {
  if (!Number.isFinite(bytes) || bytes < 0) return '—';
  for (const [power,unit] of [[3,'GiB/s'],[2,'MiB/s'],[1,'KiB/s']]) if (bytes >= 1024 ** power) { const value = bytes / 1024 ** power; return `${value.toFixed(value < 100 ? 1 : 0)} ${unit}`; }
  return `${Math.round(bytes)} B/s`;
}
function preferredNetworkInterface(interfaces) {
  const priority = iface => {
    if (iface.loopback || iface.name === 'lo') return 6;
    const ethernet = /^(?:en|eth)[a-z0-9]/i.test(iface.name);
    const down = ['down','lowerlayerdown','notpresent'].includes(iface.state);
    if (iface.up && !down) return ethernet ? 0 : 1;
    if (!down) return ethernet ? 2 : 3;
    return ethernet ? 4 : 5;
  };
  // Choose by link state and name, never by changing instantaneous traffic.
  // Default routes break ties within a class, including among Ethernet NICs.
  return [...interfaces].sort((a,b) => priority(a)-priority(b) || Number(!!b.default)-Number(!!a.default) || a.name.localeCompare(b.name,'en',{numeric:true}))[0];
}
function selectNetworkInterface(state, interfaces, saved = '') {
  if (!state || !interfaces.length) return;
  const manual = state.networkInterfaceManual && interfaces.find(iface => iface.name === state.networkInterface);
  const remembered = interfaces.find(iface => iface.name === saved);
  state.networkInterface = (manual || remembered || preferredNetworkInterface(interfaces))?.name || '';
  if (!manual) state.networkInterfaceManual = false;
  // Reconsider automatic choices when delayed metadata arrives. An early br0
  // sample must not permanently lock out an Ethernet NIC discovered later.
}
function renderNetwork(stats) {
  const state = current(), interfaces = currentNetworkInterfaces(state,stats), select = $('#network-interface');
  const saved = state ? readSaved(`dengshell.nic.${state.profileId}`, '') : '';
  selectNetworkInterface(state,interfaces,saved);
  const signature = JSON.stringify(interfaces.map(iface => [iface.name, iface.default, iface.state]));
  if (select.dataset.signature !== signature || (!interfaces.length && select.dataset.connected !== String(!!stats))) {
    select.dataset.signature = signature; select.dataset.connected = String(!!stats);
    select.replaceChildren(...(interfaces.length ? interfaces.map(iface => {
      const option = node('option', '', `${iface.name}${iface.default ? ' · 默认路由' : ''}${iface.state === 'down' ? ' · 关闭' : ''}`); option.value = iface.name; return option;
    }) : [Object.assign(node('option', '', stats ? '未发现网卡' : '未连接'), { value:'' })]));
  }
  select.value = state?.networkInterface || ''; select.disabled = !interfaces.length;
  const selected = interfaces.find(iface => iface.name === state?.networkInterface);
  select.title = selected ? `${selected.name}${selected.addresses?.length ? '\n' + selected.addresses.join('\n') : ''}` : '选择服务器网卡';
  const fresh = state?.connected && !state.networkError && Date.now() - Date.parse(state.networkStats?.sampledAt) < 2500;
  for (const [selector,key,label] of [['.upload-color','tx','上行'],['.download-color','rx','下行']]) { const ready = fresh && selected?.ready && Number.isFinite(selected[key]) && selected[key] >= 0; const value = ready ? compactTrafficSize(selected[key]) : '—'; $(selector).textContent = `${label} ${value}`; $(selector).title = `${label} · ${ready ? compactTrafficSize(selected[key]) : state?.networkError || '等待有效采样'}\n每秒尝试独立采样，按实际计数差和远端时间间隔计算${ready && state.networkStats?.elapsedMilliseconds ? `；本次为 ${(state.networkStats.elapsedMilliseconds / 1000).toFixed(2)} 秒内的平均速度` : ''}`; $(selector).setAttribute('aria-label', `${label} ${ready ? compactTrafficSize(selected[key]) : '暂无有效采样'}`); }
  $('#network-chart').setAttribute('aria-disabled', String(!state?.ready));
  drawCharts(selected ? state?.interfaceCharts?.get(selected.name) || [] : []);

}
function formatResponseTime(value) { return value === 0 ? '<1' : value < .01 ? '<0.01' : value.toFixed(2); }
function renderLatencyDetails() {
  const state = current(), ping = state?.connected ? state.latency?.ping : null;
  const measured = ping?.windowReady ?? (ping?.windowSent > 0);
  $('#ping-loss').textContent = measured ? Number(ping.windowLossPercent).toFixed(2) + '%' : '—';
  $('#ping-loss').classList.toggle('has-loss', measured && ping.windowLost > 0);
  renderPingBars(ping);
  const counts = measured ? `1 分钟内：收到 ${ping.windowReceived} / ${ping.windowSent}，丢失 ${ping.windowLost}，待回 ${ping.windowPending ?? ping.pending ?? 0}` : state?.connected ? '等待探测结果' : '等待连接';
  $('#ping-status').removeAttribute('title');
  $('.icmp-heading').title = [ping?.address ? `目标 ${ping.address}` : ping?.target || '', counts, ping?.routeNote || 'ICMP 遵循系统与 TUN 路由，不经应用内 SSH 代理。', ping?.error || '', '按样本时间统计最近 60 秒已完成的网络探测；本机错误和待回包不计入丢包分母。'].filter(Boolean).join('\n');
  $('#ping-detail').title = counts;
  const pingReady = !!ping?.ready && ping.status === 'reply' && Date.now() - Date.parse(ping.sampledAt) < 3500;
  $('#ping-latency').replaceChildren(document.createTextNode(pingReady ? `${formatResponseTime(ping.milliseconds)} ` : '— '), node('small', '', 'ms'));
  $('#ping-latency').title = pingReady ? `本机到 ${ping.address || ping.target} 的 ICMP 往返时间` : '当前没有有效的 ICMP 回包';
}
function renderPingBars(ping) { window.DengLatencyChart.render($('#ping-samples'), ping, current()?.id || ''); }
function stopDiagnosticPolling() { clearTimeout(diagnosticView.timer); diagnosticView.timer = null; }
function renderDiagnostic(report) {
  diagnosticView.report = report;
  $('#diagnostics-status').textContent = { running:'检测中…', done:'检测完成', failed:'检测失败', cancelled:'已停止' }[report.status] || '准备检测';
  $('#diagnostics-route').textContent = `${report.direction === 'remote' ? profileFor(sessions.get(diagnosticView.sessionID))?.name || '服务器' : '本机'} → ${report.target || diagnosticView.target || profileFor(sessions.get(diagnosticView.sessionID))?.host || '服务器'}`;
  const output = report.output || '';
  const area = $('#diagnostics-output'), follow = area.scrollTop + area.clientHeight >= area.scrollHeight - 24;
  area.textContent = output + (report.error ? `${output ? '\n\n' : ''}${report.error}` : output ? '' : '等待探测结果…');
  if (follow) area.scrollTop = area.scrollHeight;
  $('#cancel-diagnostics').disabled = report.status !== 'running'; $('#copy-diagnostics').disabled = !output && !report.error;
  $('#retry-diagnostics').disabled = report.status === 'running';
  updateMTRInstallNotice(report);
}
async function pollDiagnostic(generation) {
  if (generation !== diagnosticView.generation || !diagnosticView.id) return;
  try {
    const report = await api(`/api/diagnostics/${diagnosticView.id}`);
    if (generation !== diagnosticView.generation) return;
    renderDiagnostic(report);
    if (report.status === 'running') diagnosticView.timer = setTimeout(() => pollDiagnostic(generation), 500);
  } catch (error) { if (generation === diagnosticView.generation) renderDiagnostic({ ...diagnosticView.report, status:'failed', error:error.message }); }
}
async function startDiagnostic(direction = 'local', target = '') {
  const state = sessions.get(diagnosticView.sessionID) || current(); if (!state?.ready) return toast('请先连接服务器');
  const previousID = diagnosticView.id, generation = ++diagnosticView.generation; stopDiagnosticPolling();
  diagnosticView = { ...diagnosticView, id:'', sessionID:state.id, direction, target, report:null };
  if (previousID) await remove(`/api/diagnostics/${previousID}`).catch(() => {});
  if (generation !== diagnosticView.generation) return;
  renderDiagnostic({ direction, target:target || profileFor(state)?.host, status:'running', output:'正在发起探测…' });
  try {
    const report = await post(`/api/sessions/${state.id}/diagnostics`, { direction, target });
    if (generation !== diagnosticView.generation) { await remove(`/api/diagnostics/${report.id}`).catch(() => {}); return; }
    diagnosticView.id = report.id; renderDiagnostic(report); pollDiagnostic(generation);
  } catch (error) { if (generation === diagnosticView.generation) renderDiagnostic({ direction, target, status:'failed', output:'', error:error.message }); }
}
async function openDiagnostics() { const state = current(); if (!state?.ready) return toast('请先连接服务器'); diagnosticView.sessionID = state.id; $('#diagnostics-dialog').showModal(); await startDiagnostic(); }
async function cancelDiagnostic() {
  stopDiagnosticPolling(); const id = diagnosticView.id, generation = ++diagnosticView.generation;
  if (!id) {
    if ($('#diagnostics-dialog').open) renderDiagnostic({ ...diagnosticView.report, status:'cancelled' });
    return;
  }
  await remove(`/api/diagnostics/${id}`);
  // Cancellation is asynchronous: keep observing until the process/channel exits.
  if (generation === diagnosticView.generation && $('#diagnostics-dialog').open) await pollDiagnostic(generation);
}
function requestQuit() {
  if ($('#exit-dialog').open) return;
  if ([...sessions.values()].some(state=>state.detaching||state.handoffProvisional||state.ownershipUncertain)) { native()?.CancelQuit?.(); toast('正在交接终端，请待独立窗口打开后再关闭'); return; }
  if (window.DengTextEditors?.savingCount()) { native()?.CancelQuit?.(); toast('远程文件正在保存，请等待完成后退出'); return; }
  const unsaved = window.DengTextEditors?.unsavedCount() || 0;
  const count = [...sessions.values()].filter(state => state.connected).length;
  const transfers = [...localTasks.values()].filter(task => ['queued','uploading'].includes(task.status)).length;
  $('#exit-description').textContent = [count ? `将关闭当前窗口，断开其中 ${count} 个 SSH 会话。其他独立窗口会保持运行。` : '确认关闭当前窗口。其他独立窗口会保持运行。', transfers ? `${transfers} 个传输任务尚未完成。` : '', unsaved ? `${unsaved} 个远程文件有未保存的修改，退出会丢弃这些修改。` : ''].filter(Boolean).join(' ');
  $('#exit-dialog').showModal(); $('#cancel-exit').focus();
}
async function initializeQuitConfirmation() {
  if (quitListenerReady || !window.runtime?.EventsOn) return;
  window.runtime.EventsOn('dengshell:confirm-quit', requestQuit); quitListenerReady = true;
  if (native()?.WindowState && (await native().WindowState()).quitPending) requestQuit();
}
function initializeRefinements() {
  for (const key of ['cpu','memory']) {
    const button = $(`#sort-process-${key}`);
    button.onclick = event => { if (event.detail < 2) sortProcesses(key); };
    button.ondblclick = event => { event.preventDefault(); sortProcesses(key,true); };
  }

  $('#copy-terminal-selection').onclick = safe(async () => { $('#terminal-menu').hidden = true; await copyTerminalSelection(terminalMenuSession); });
  $('#paste-terminal-clipboard').onclick = safe(async () => { $('#terminal-menu').hidden = true; await pasteClipboard(terminalMenuSession); });
  document.addEventListener('pointerdown', event => { if (!event.target.closest('#terminal-menu')) $('#terminal-menu').hidden = true; });
  document.addEventListener('keydown', event => { if (event.key === 'Escape') $('#terminal-menu').hidden = true; });
  $('#command-input').addEventListener('input', resizeCommandInput);
  $('#command-history').onclick = openCommandHistory; $('#close-history').onclick = () => $('#history-dialog').close(); $('#history-filter').oninput = renderCommandHistory;
  $('#clear-history').onclick = safe(async () => { const button = $('#clear-history'); button.disabled = true; try { await clearGlobalCommandHistory(); } finally { renderCommandHistory(); } });
  setInterval(() => { if ($('#history-dialog').open && !document.hidden) refreshGlobalCommandHistory().catch(() => {}); }, 2000);
  initializeMTRInstallation();
  for (const button of document.querySelectorAll('[data-copy-address]')) button.onclick = safe(async () => { const value = document.getElementById(button.dataset.copyAddress).textContent; if (!button.disabled && value !== '-') { await copyText(value); toast('地址已复制'); } });
  $('#network-interface').onchange = event => { const state = current(); if (!state) return; state.networkInterface = event.target.value; state.networkInterfaceManual = true; save(`dengshell.nic.${state.profileId}`, state.networkInterface); renderNetwork(state.stats); };
  $('#network-chart').onclick = safe(openDiagnostics); $('#network-chart').onkeydown = safe(event => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); return openDiagnostics(); } });
  $('#close-diagnostics').onclick = () => $('#diagnostics-dialog').close();
  $('#diagnostics-dialog').addEventListener('close', () => cancelDiagnostic().catch(() => {}));
  $('#cancel-diagnostics').onclick = safe(cancelDiagnostic);
  $('#retry-diagnostics').onclick = safe(() => startDiagnostic(diagnosticView.direction, diagnosticView.target));
  $('#diagnostics-local').onclick = safe(() => startDiagnostic('local'));
  $('#diagnostics-remote').onclick = safe(async () => { const target = await ask({ title:'从服务器 MTR 到…', description:'输入目标 IPv4 或 IPv6 地址。探测从当前服务器发起。', input:true, confirm:'开始检测' }); if (target?.trim()) await startDiagnostic('remote', target.trim()); });
  $('#copy-diagnostics').onclick = safe(async () => { await copyText(`${$('#diagnostics-route').textContent}\n${$('#diagnostics-output').textContent}`); toast('诊断结果已复制'); });
  $('#cancel-exit').onclick = safe(async () => { await native()?.CancelQuit?.(); $('#exit-dialog').close(); });
  $('#exit-dialog').oncancel = safe(async event => { event.preventDefault(); await native()?.CancelQuit?.(); $('#exit-dialog').close(); });
  $('#exit-form').onsubmit = safe(async event => { event.preventDefault(); $('#confirm-exit').disabled = true; try { await window.DengPortablePreferences.flush(); if ([...sessions.values()].some(state=>state.detaching||state.handoffProvisional||state.ownershipUncertain)) throw new Error('终端正在交接，请稍后重试'); await Promise.all([...sessions.keys()].map(closeSession)); $('#exit-dialog').close(); await native()?.ConfirmQuit?.(); } catch (error) { $('#exit-description').textContent = `暂时无法退出：${error.message || error}`; } finally { $('#confirm-exit').disabled = false; } });
}
