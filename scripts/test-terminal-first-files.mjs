import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

// Exercise the production status consumer, directory navigation, pending panel,
// and authenticated OSC protocol without a browser or any real credentials.
const appSource = readFileSync(new URL('../web/app.js', import.meta.url), 'utf8');
const protocolSource = readFileSync(new URL('../web/terminal-integration.js', import.meta.url), 'utf8');
function sourceBetween(start, end) {
  const first = appSource.indexOf(start), last = appSource.indexOf(end, first);
  assert.ok(first >= 0 && last > first, `missing production functions: ${start}`);
  return appSource.slice(first, last);
}
const filesSource = sourceBetween('function filesUsable(', '\nfunction selectedEntry(');
function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

function fixture() {
  const status = deferred(), requests = [], inputs = [], timers = [], notices = [], elements = new Map();
  let osc, renders = 0;
  const element = selector => {
    if (!elements.has(selector)) elements.set(selector, { value: '', textContent: '', hidden: false, disabled: false, replaceChildren() {} });
    return elements.get(selector);
  };
  const state = { id: 'terminal-first', connected: true, ready: true, sftpPending: true, sftpAvailable: false,
    home: '/', cwd: '/', entries: [], folders: new Map(), navGeneration: 0, follow: false,
    term: { parser: { registerOscHandler(code, handler) { assert.equal(code, 777); osc = handler; } },
      modes: { bracketedPasteMode: true }, options: {}, clearSelection() {}, paste() {}, focus() {} } };
  const context = vm.createContext({
    state, sessions: new Map([[state.id, state]]), activeID: state.id, current: () => state,
    Uint8Array, TextDecoder, atob, AbortController,
    $: element, window: {}, fileSortKey: 'name', ascending: true, selectedName: '',
    compareFileEntries: () => 0, normalizePath: path => path,
    DengFileBrowser: { invalidate() {} }, renderTree() {}, updateFileActions() {},
    renderSessionInfo() { renders++; },
    toast: message => notices.push(message), recordCommand() {}, sendInput: (_state, text) => inputs.push(text),
    setTimeout: (callback, delay) => { timers.push({ callback, delay }); return timers.length; },
    api: async path => {
      requests.push(path);
      if (path.endsWith('/file-status')) return status.promise;
      assert.match(path, /\/files\?path=/);
      return { path: new URL(path, 'https://fixture.invalid').searchParams.get('path'), entries: [] };
    },
  });
  vm.runInContext(filesSource + '\n' + protocolSource, context);
  context.bindShellIntegration(state);
  const nonce = 'a'.repeat(48);
  context.acceptShellIntegration(state, { nonce, shell: '' });
  const emit = (kind, payload = '', token = nonce) => osc(`DengShell;${kind};${token}${payload ? ';' + payload : ''}`);
  return { context, state, status, requests, inputs, timers, notices, element, emit, renders: () => renders };
}

// SFTP remains pending while the terminal accepts input. No unavailable warning
// or remote listing is permitted before the independent status request finishes.
let test = fixture();
test.context.renderFiles();
assert.equal(test.context.filesUsable(test.state), false);
assert.match(test.element('#file-empty').textContent, /正在加载远程文件/);
assert.doesNotMatch(test.element('#file-empty').textContent, /不可用/);
assert.equal(test.element('#refresh-files').disabled, true);
assert.equal(test.state.shellIntegration.shell, '', 'SSH metadata need not know the remote shell yet');
test.emit('ready', 'bash', 'b'.repeat(48));
assert.equal(test.state.shellIntegration.ready, false, 'another nonce cannot activate integration');
test.emit('ready', 'bash'); test.emit('prompt');
assert.equal(test.state.shellIntegration.ready, true);
assert.equal(test.state.shellIntegration.shell, 'bash');
assert.equal(test.state.shellIntegration.atPrompt, true);
assert.equal(test.context.pasteTerminalText(test.state, 'pwd', { execute: true }), true);
assert.deepEqual(test.inputs, ['\r'], 'pending files must not block terminal input');
let pending = test.context.refreshFileStatus(test.state, true);
assert.equal(test.context.refreshFileStatus(test.state, true), pending, 'one tab must share one status request');
await test.context.navigate('/premature', test.state);
assert.deepEqual(test.requests, ['/api/sessions/terminal-first/file-status']);
assert.equal(test.state.sftpPending, true);
test.status.resolve({ sftpPending: false, sftpAvailable: true, home: '/home/fixture' });
await pending;
assert.equal(test.state.cwd, '/home/fixture');
assert.equal(test.context.filesUsable(test.state), true);
assert.equal(test.state.connected, true);
assert.equal(test.state.ready, true);
assert.equal(test.element('#refresh-files').disabled, false);
assert.equal(test.requests.length, 2, 'ready status should automatically read the home directory once');
assert.match(test.requests[1], /path=%2Fhome%2Ffixture$/);

// A confirmed startup-directory change wins over the home path when follow is on.
test = fixture(); test.state.follow = true;
pending = test.context.refreshFileStatus(test.state, true);
const directory = '/srv/中文 project';
test.emit('cwd', Buffer.from(directory).toString('base64'));
assert.equal(test.state.terminalDirectory, directory);
assert.equal(test.requests.length, 1, 'OSC cwd while pending cannot begin a file request');
test.status.resolve({ sftpPending: false, sftpAvailable: true, home: '/home/fixture' });
await pending;
assert.equal(test.state.cwd, directory);

// Restored tabs retain their directory rather than jumping back to home.
test = fixture(); test.state.cwd = '/srv/restored';
pending = test.context.refreshFileStatus(test.state);
test.status.resolve({ sftpPending: false, sftpAvailable: true, home: '/home/fixture' });
await pending;
assert.equal(test.state.cwd, '/srv/restored');

// An unavailable file subsystem never changes SSH readiness or starts a listing.
test = fixture(); pending = test.context.refreshFileStatus(test.state, true);
test.status.resolve({ sftpPending: false, sftpAvailable: false, home: '/' });
await pending;
assert.equal(test.requests.length, 1);
assert.equal(test.state.connected, true); assert.equal(test.state.ready, true);
assert.equal(test.state.sftpPending, false);
assert.match(test.element('#file-empty').textContent, /SFTP 文件服务不可用/);
assert.equal(test.context.pasteTerminalText(test.state, 'pwd', { execute: true }), true);

// A late status reply cannot republish or populate a tab that has been closed.
test = fixture(); pending = test.context.refreshFileStatus(test.state, true);
test.state.closed = true; test.context.sessions.delete(test.state.id);
test.status.resolve({ sftpPending: false, sftpAvailable: true, home: '/late-result' });
await pending;
assert.equal(test.context.sessions.size, 0); assert.equal(test.requests.length, 1);
assert.equal(test.state.cwd, '/'); assert.equal(test.state.sftpPending, true);
assert.equal(test.renders(), 0);

// A status transport failure provides no evidence of remote SFTP unavailability.
test = fixture(); pending = test.context.refreshFileStatus(test.state, true);
test.status.reject(new Error('temporary local transport failure'));
await pending;
assert.equal(test.state.sftpPending, true); assert.equal(test.state.connected, true);
assert.equal(test.requests.length, 1); assert.equal(test.notices.length, 0);
assert.equal(test.timers.length, 1); assert.equal(test.timers[0].delay, 1000);
test.context.renderFiles();
assert.match(test.element('#file-empty').textContent, /正在加载远程文件/);

console.log('PASS: pending SFTP leaves SSH usable; file-ready home/follow/restored-directory loading; unavailable and closed-tab handling; retry without false unavailable; blank-shell metadata accepts authenticated OSC ready.');
