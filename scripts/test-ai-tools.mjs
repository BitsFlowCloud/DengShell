import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import { createHash } from 'node:crypto';

const production = name => fs.readFileSync(new URL('../web/' + name, import.meta.url), 'utf8');
const appSource = production('app.js');
const between = (start, end) => appSource.slice(appSource.indexOf(start), appSource.indexOf(end, appSource.indexOf(start)));
const hash = text => createHash('sha256').update(text).digest('hex');
function deferred() { let resolve, reject; const promise = new Promise((a, b) => { resolve = a; reject = b; }); return { promise, resolve, reject }; }
function fixture(environment = {}) {
  const events = [], frames = [], posts = [], requests = [], files = new Map([['/tmp/example.txt', 'original']]);
  let locked = false, enabled = true, selected = 'a', nextAPI, nextPost, connection;
  const profiles = [{ id: 'p-a', name: 'Server A', host: 'a.invalid', user: 'fixture', auth: 'password', secret: 'secret-never-export', proxy: { secret: 'proxy-secret-never-export' }, keyPath: '/private/key-never-export', keyId: 'key-never-export' }, { id: 'p-b', name: 'Server B', host: 'b.invalid', user: 'fixture' }, { id: 'p-rdp', protocol: 'rdp', name: 'RDP', host: 'rdp.invalid' }];
  const sessions = new Map();
  const listeners = new Map();
  const window = { DengSecurityLock: { isLocked: () => locked }, DengFileBrowser: { invalidate() {} }, DengTextEditors: { openText: async (state, path) => events.push({ editor: state.id, path }) }, DengProcessView: { open: () => events.push({ processes: selected }) }, dispatchEvent(event) { events.push(event); for (const handler of listeners.get(event.type) || []) handler(event); }, addEventListener(name, handler) { if (!listeners.has(name)) listeners.set(name, []); listeners.get(name).push(handler); } };
  const plainAPI = async path => {
    const url = new URL(path, 'https://fixture.invalid'), target = url.searchParams.get('path');
    if (url.pathname.endsWith('/file-content')) { if (!files.has(target)) throw new Error('missing file'); const text = files.get(target); return { path: target, text, sha256: hash(text), encoding: 'utf-8', bytes: text.length }; }
    if (url.pathname.endsWith('/files')) return { path: target, entries: [{ name: '~', kind: 'folder', size: 0, mode: 'drwxr-xr-x', modifiedAt: 1790985600000, secret: 'entry-secret-never-export' }] };
    if (url.pathname.endsWith('/stats')) return { memory: { total: 32 } };
    throw new Error('unexpected API ' + path);
  };
  const sandbox = { window, sessions, profiles, WebSocket: { OPEN: 1 }, CustomEvent: class { constructor(type, options) { this.type = type; this.detail = options.detail; } }, setTimeout, clearTimeout, Date, URL, URLSearchParams, console,
    current: () => sessions.get(selected), profileFor: state => profiles.find(item => item.id === state.profileId),
    activate: id => { selected = id; events.push({ active: id }); },
    filesUsable: state => state.connected && !state.sftpPending && state.sftpAvailable !== false,
    writeTerminalAndWait: async state => { if (state.drain) await state.drain.promise; },
    api: async path => { requests.push(path); if (nextAPI) { const action = nextAPI; nextAPI = null; return action.promise; } return plainAPI(path); },
    post: async (url, body) => {
      posts.push({ url, body: { ...body } });
      if (nextPost) { const action = nextPost; nextPost = null; return action.promise; }
      if (url.endsWith('/file-content')) {
        if (hash(files.get(body.path) || '') !== body.sha256) throw Object.assign(new Error('remote file changed'), { code: 'file_changed' });
        files.set(body.path, body.text); return { path: body.path, sha256: hash(body.text), bytes: body.text.length };
      }
      if (body.action === 'new-file') files.set(body.path + '/' + body.name, '');
      return { ok: true };
    },
    navigate: async (path, state) => { state.cwd = path; }, showPane: name => events.push({ pane: name }),
    connect: (id, force, options) => {
      events.push({ connect: id, force, options });
      const pending = makeState('pending-ai', id); Object.assign(pending, { pendingConnection: true, ready: false, connected: false, connectionAbort: { abort: () => events.push({ aborted: true }) } });
      sessions.set(pending.id, pending); if (!sandbox.current()) sandbox.activate(pending.id);
      connection = { pending, ...deferred() }; return connection.promise;
    },
    dropSessionView: (id, expected) => { const state = sessions.get(id); if (state !== expected) return; state.closed = true; sessions.delete(id); if (selected === id) selected = null; },
    ...environment,
  };
  const context = vm.createContext(sandbox);
  vm.runInContext(between('function normalizePath(', '\nconst parentPath'), context);
  vm.runInContext(between('function sendMessage(', '\nfunction fitActive('), context);
  vm.runInContext(production('terminal-integration.js'), context);
  vm.runInContext(production('ai-tools.js'), context);
  function makeState(id, profileId = 'p-' + id) {
    const state = { id, profileId, connected: true, ready: true, cwd: '/tmp', terminalDirectory: '/tmp', sftpAvailable: true, shellIntegration: { ready: true, atPrompt: true }, ws: { readyState: 1, send: data => frames.push({ sessionId: state.id, ...JSON.parse(data) }) } };
    const lines = ['old line', 'fixture shell', 'fixture@host:~$'];
    state.term = { cols: 80, rows: 3, options: {}, modes: { bracketedPasteMode: true, applicationCursorKeysMode: false }, buffer: { active: { type: 'normal', length: lines.length, baseY: 0, cursorX: 15, cursorY: 2, getLine: index => ({ translateToString: () => lines[index] }) } }, clearSelection() {}, focus() {}, paste: text => context.sendInput(state, state.term.modes.bracketedPasteMode ? '\x1b[200~' + text + '\x1b[201~' : text) };
    return state;
  }
  const a = makeState('a'), b = makeState('b'); sessions.set('a', a); sessions.set('b', b);
  const guard = () => { if (!enabled) throw new Error('AI stopped'); if (locked) throw new Error('AI locked'); return true; };
  return { tools: window.DengShellAITools, a, b, profiles, sessions, frames, posts, requests, events, files, guard, window, makeState,
    set locked(value) { locked = value; }, set enabled(value) { enabled = value; }, set selected(value) { selected = value; },
    set nextAPI(value) { nextAPI = value; }, set nextPost(value) { nextPost = value; }, get connection() { return connection; },
    run(name, args) { return window.DengShellAITools.execute(name, args, guard); },
  };
}

const checks = [];
{
  // Model hidden Chromium's one-second timer batches and intensive throttling
  // after five nested timer tasks. xterm parsing also schedules a timer; a
  // bounded five-second observation must not inherit a minute-long delay.
  let now = 0, nesting = 0, openPorts = 0, channels = 0;
  const timer = (callback, milliseconds = 0) => {
    const level = nesting + 1;
    return setImmediate(() => {
      nesting = level; now += Math.max(milliseconds, level >= 5 ? 60000 : 1000);
      callback();
    });
  };
  class TaskChannel {
    constructor() {
      channels++; openPorts += 2;
      const port = () => { let closed = false; return { close() { if (!closed) { closed = true; openPorts--; } } }; };
      this.port1 = port(); this.port2 = port();
      this.port2.postMessage = () => setImmediate(() => { nesting = 0; this.port1.onmessage?.({ data: null }); });
    }
  }
  const f = fixture({ setTimeout: timer, MessageChannel: TaskChannel, Date: { now: () => now }, writeTerminalAndWait: () => new Promise(resolve => timer(resolve)) });
  f.a.shellIntegration.atPrompt = false;
  const result = await f.run('read_terminal', { session_id: 'a', wait_ms: 5000 });
  assert.equal(result.sessionId, 'a'); assert.equal(result.busy, true);
  assert(now >= 5000 && now <= 8000, `five-second observation took ${now}ms under background timer throttling`);
  assert(channels > 0); assert.equal(openPorts, 0, 'completed observation leaked MessageChannel ports');
  const fallback = fixture();
  assert.equal((await fallback.run('read_terminal', { session_id: 'a', wait_ms: 1 })).sessionId, 'a');
  checks.push('Bounded terminal observations break background timer chains, close task ports, and retain a timer-only fallback');
}
{
  const f = fixture(), output = JSON.stringify(f.tools.context());
  for (const secret of ['secret-never-export', 'key-never-export', 'proxy-secret-never-export', 'fixture@host']) assert(!output.includes(secret));
  assert.equal(f.tools.context().sessionId, 'a');
  assert(f.tools.context().sessionToken);
  assert.equal(new Set(f.tools.definitions.map(d => d.name)).size, f.tools.definitions.length);
  for (const def of f.tools.definitions) { assert.equal(def.parameters.additionalProperties, false); for (const key of def.parameters.required) assert(def.parameters.properties[key]); }
  f.locked = true; assert.throws(() => f.tools.context(), /锁定/);
  checks.push('Context and schemas expose no credentials, key paths or terminal content; locked context fails closed');
}
{
  const f = fixture();
  for (const [name, args] of [['eval', { code: 'x' }], ['get_app_state', { extra: true }], ['read_terminal', {}], ['read_terminal', { session_id: 'a', max_lines: 401 }], ['read_terminal', { session_id: 'a', wait_ms: 0.5 }], ['terminal_key', { session_id: 'a', key: '__proto__' }], ['terminal_input', { session_id: 'a', text: '\x1b[2J' }], ['terminal_input', { session_id: 'a', text: 'x'.repeat(16001) }], ['list_files', { session_id: 'a', path: '~/secret' }], ['file_action', { session_id: 'a', action: 'rename', path: '/tmp/file' }], ['file_action', { session_id: 'a', action: 'mkdir', path: '/tmp', name: '../outside' }], ['show_view', { view: 'terminal' }], ['show_view', { view: 'commands', path: '/tmp' }]]) await assert.rejects(f.run(name, args));
  await assert.rejects(f.tools.execute('get_app_state', {}, () => false), /停止/);
  await assert.rejects(f.tools.execute('get_app_state', {}), /检查/);
  assert.equal(f.frames.length + f.posts.length + f.requests.length, 0);
  checks.push('Unknown tools, extra properties, control injection, nonliteral paths, invalid ranges and missing guards fail before side effects');
}
{
  const f = fixture(); f.selected = 'b';
  const result = await f.run('terminal_input', { session_id: 'a', text: 'printf test', execute: true, wait_ms: 0 });
  assert.equal(result.sessionId, 'a'); assert.equal(result.inputAccepted, true); assert.equal(result.observationOnly, true); assert(!('exitCode' in result));
  assert.deepEqual(f.frames.map(item => [item.sessionId, item.data]), [['a', '\x1b[200~printf test\x1b[201~'], ['a', '\r']]);
  assert.equal(result.atPrompt, false); assert.equal(result.busy, true);
  await assert.rejects(f.run('terminal_input', { session_id: 'a', text: 'second', execute: true, wait_ms: 0 }), /空闲/);
  f.a.term.buffer.active.type = 'alternate';
  await assert.rejects(f.run('terminal_input', { session_id: 'a', text: ':wq', execute: true, wait_ms: 0 }), /空闲/);
  await f.run('terminal_input', { session_id: 'a', text: 'TUI text', execute: false, wait_ms: 0 });
  f.a.term.modes.applicationCursorKeysMode = true;
  await f.run('terminal_key', { session_id: 'a', key: 'ArrowUp', wait_ms: 0 });
  assert.equal(f.frames.at(-1).data, '\x1bOA');
  await f.run('terminal_key', { session_id: 'a', key: 'CtrlC', wait_ms: 0 }); assert.equal(f.frames.at(-1).data, '\x03');
  const screen = await f.run('read_terminal', { session_id: 'a', max_lines: 2 });
  assert.equal(screen.buffer, 'alternate'); assert.equal(screen.output, 'fixture shell\nfixture@host:~$'); assert.equal(screen.untrusted, true);
  checks.push('Real paste/sendInput code binds the original session, honors bracketed paste, refuses busy command submission, and navigates a TUI without inventing exit codes');
}
{
  for (const kind of ['stop', 'lock', 'close', 'replace', 'handoff']) {
    const f = fixture(), response = deferred(); f.nextAPI = response;
    const pending = f.run('read_file', { session_id: 'a', path: '/tmp/example.txt' });
    if (kind === 'stop') f.enabled = false;
    if (kind === 'lock') f.locked = true;
    if (kind === 'close') f.a.closed = true;
    if (kind === 'replace') f.sessions.set('a', f.makeState('a'));
    if (kind === 'handoff') f.a.detaching = true;
    response.resolve({ path: '/tmp/example.txt', text: 'late sensitive text', sha256: hash('original'), encoding: 'utf-8', bytes: 8 });
    await assert.rejects(pending);
    assert.equal(f.posts.length + f.frames.length, 0);
  }
  const f = fixture(); f.a.drain = deferred();
  const pending = f.run('read_terminal', { session_id: 'a' }); f.enabled = false; f.a.drain.resolve(); await assert.rejects(pending, /stopped/);
  checks.push('Stop, lock, closed/replaced sessions and window handoff reject late API and terminal-drain results');
}
{
  const f = fixture();
  const listing = await f.run('list_files', { session_id: 'a', path: '/tmp/~' });
  assert.equal(listing.path, '/tmp/~'); assert(!JSON.stringify(listing).includes('entry-secret'));
  assert.equal(listing.entries[0].modifiedAt, 1790985600000); assert(!('modified' in listing.entries[0]));
  const body = { session_id: 'a', path: '/tmp/example.txt', text: 'replacement', sha256: hash('original'), encoding: 'utf-8' };
  await assert.rejects(f.run('write_file', body), /先/);
  const read = await f.run('read_file', { session_id: 'a', path: body.path }); assert.equal(read.sha256, body.sha256);
  const anotherGuard = () => true;
  await assert.rejects(f.tools.execute('write_file', body, anotherGuard), /本次任务/);
  f.files.set(body.path, 'changed outside');
  await assert.rejects(f.run('write_file', body), error => error.code === 'file_changed'); assert.equal(f.files.get(body.path), 'changed outside');
  const updated = await f.run('read_file', { session_id: 'a', path: body.path });
  const saved = await f.run('write_file', { ...body, sha256: updated.sha256 }); assert.equal(saved.saved, true); assert.equal(f.files.get(body.path), 'replacement');
  await assert.rejects(f.run('write_file', { ...body, sha256: saved.sha256 }), /先/);
  f.files.set(body.path, 'x'.repeat(131073)); const large = await f.run('read_file', { session_id: 'a', path: body.path }); assert(large.truncated);
  await assert.rejects(f.run('write_file', { ...body, sha256: large.sha256 }), /完整读取/);
  await assert.rejects(f.run('file_action', { session_id: 'a', action: 'delete', path: '/tmp/..' }), /根目录/);
  await f.run('file_action', { session_id: 'a', action: 'mkdir', path: '/tmp', name: '~' });
  assert.equal(f.posts.at(-1).body.path, '/tmp'); assert.equal(f.posts.at(-1).body.name, '~');
  checks.push('Literal tilde paths, read-before-write hashes, task-scoped reads, remote 409 conflicts, truncation limits and root-delete protection are enforced');
}
{
  const f = fixture(); let target = f.a;
  f.window.addEventListener('dengshell:ai-context-change', event => { assert.equal(event.detail.source, 'ai'); target = f.sessions.get(event.detail.sessionId); });
  const guard = () => { assert.equal(f.tools.context().sessionId, target.id); return true; };
  const result = await f.tools.execute('select_session', { session_id: 'b' }, guard);
  assert.equal(result.targetChanged, true); assert.equal(target, f.b);
  Object.defineProperty(guard, 'sessionId', { get: () => target.id });
  await assert.rejects(f.tools.execute('terminal_input', { session_id: 'a', text: 'wrong server', execute: true, wait_ms: 0 }, guard), /目标会话/);
  await assert.rejects(f.tools.execute('show_view', { view: 'terminal', session_id: 'a' }, guard), /目标会话/);
  await assert.rejects(f.run('show_view', { view: 'terminal', session_id: 'a' }), /select_session/);
  await f.tools.execute('show_view', { view: 'files', session_id: 'b', path: '/tmp/~' }, guard); assert.equal(f.b.cwd, '/tmp/~');
  await f.tools.execute('show_view', { view: 'processes', session_id: 'b' }, guard); assert(f.events.some(event => event.processes === 'b'));
  checks.push('Explicit AI selection updates the target event before the next guard; existing file and process views are reused');
}
{
  const f = fixture();
  await assert.rejects(f.run('connect_profile', { profile_id: 'p-rdp' }), /RDP/);
  const pending = f.run('connect_profile', { profile_id: 'p-b' });
  assert.equal(f.events.find(event => event.connect).options.background, true);
  const state = f.connection.pending; f.sessions.delete(state.id); state.id = 'connected-ai'; state.pendingConnection = false; state.connected = true; state.ready = true; f.sessions.set(state.id, state); f.connection.resolve(state);
  const result = await pending; assert.equal(result.sessionId, state.id); assert.equal(result.targetChanged, true);
  const stopped = fixture(), connection = stopped.run('connect_profile', { profile_id: 'p-b' });
  const ownPending = stopped.connection.pending; stopped.enabled = false;
  await assert.rejects(connection, /stopped/);
  assert(ownPending.closed); assert(stopped.events.some(event => event.aborted)); assert(stopped.sessions.has('a') && stopped.sessions.has('b'));
  stopped.connection.resolve(null);
  checks.push('Saved-profile connection retains the existing connector, rejects RDP, selects only the completed session, and cancels only its own pending connection when stopped');
}
console.log(JSON.stringify({ checks, passed: checks.length }, null, 2));
