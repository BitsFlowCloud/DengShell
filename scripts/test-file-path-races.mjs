import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

const source = fs.readFileSync(new URL('../web/app.js', import.meta.url), 'utf8');
function between(start, end) {
  const first = source.indexOf(start), last = source.indexOf(end, first);
  assert.ok(first >= 0 && last > first, `missing production code: ${start}`);
  return source.slice(first, last);
}
const state = { id: 'original', connected: true, cwd: '/srv/files', home: '/home/original', entries: [], folders: new Map(), navGeneration: 0 };
const other = { id: 'other', connected: true, cwd: '/srv/other', home: '/home/other', folders: new Map(), navGeneration: 0 };
const nodes = new Map(), posts = [], prompts = [], checks = [], tasks = new Map(), reads = [];
const element = selector => { if (!nodes.has(selector)) nodes.set(selector, { value: '', textContent: '' }); return nodes.get(selector); };
let taskID = 0;
const context = vm.createContext({
  state, sessions: new Map([[state.id, state], [other.id, other]]), activeID: state.id,
  current: () => state, selectedEntries: owner => owner.entries, profileFor: () => ({ name: 'fixture' }),
  window: { DengUploadConfirmation: { prepare: async (_state, selection) => { checks.push(selection); return { overwriteTargets: [], skipTargets: [] }; }, skipped: () => false } },
  document: { addEventListener() {} }, AbortController,
  $: element, localTasks: tasks, crypto: { randomUUID: () => `task-${++taskID}` },
  renderTransfers() {}, pumpUploads() {}, toast() {}, updateFileActions() {}, reflectFileSelection() {},
  fileSelection: () => ({ names: new Set(['~']) }), DengFileBrowser: { invalidate() {} },
  renderFiles() { element('#path-input').value = state.cwd; element('#file-status-count').textContent = 'loaded'; },
  post: async (url, body) => { posts.push({ url, body }); }, ask: async options => { prompts.push(options); return true; },
  api: url => new Promise((resolve, reject) => reads.push({ url, resolve, reject })),
});
vm.runInContext([
  between('function normalizePath(', '\nconst parentPath'),
  between('function filesUsable(', '\n// The status request'),
  between('async function navigate(', '\nfunction renderFiles('),
  between('function selectedFileTargets(', '\nfunction selectedEntry('),
  between('async function queueFiles(', '\nfunction pumpUploads('),
].join('\n'), context);
vm.runInContext(fs.readFileSync(new URL('../web/file-tools.js', import.meta.url), 'utf8'), context);

// Literal remote names, including a directory named "~", must never select the
// home directory for destructive operations or relocate an upload outside cwd.
state.entries = [{ name: '~', kind: 'folder' }];
assert.equal(context.selectedFileTargets(state)[0].path, '/srv/files/~');
const productionNavigate = context.navigate;
context.navigate = async () => {};
await context.window.DengFileTools.removeFiles(state, context.selectedFileTargets(state));
assert.equal(posts.length, 1);
assert.equal(posts[0].body.action, 'delete');
assert.equal(posts[0].body.path, '/srv/files/~');
assert.match(prompts[0].description, /\/srv\/files\/~\n/);
assert.doesNotMatch(prompts[0].description, /\/home\/original/);
await context.queueFiles([
  { file: { name: '~', size: 1 }, relativePath: '~' },
  { file: { name: 'config', size: 2 }, relativePath: '~/config' },
], state, state.cwd);
assert.deepEqual([...checks[0].targets], ['/srv/files/~', '/srv/files/~/config']);
assert.deepEqual([...tasks.values()].map(task => task.target), ['/srv/files/~', '/srv/files/~/config']);
assert.equal(context.normalizePath('../sibling', state.cwd), '/srv/sibling');
assert.equal(context.normalizePath('/absolute/./file', state.cwd), '/absolute/file');
context.navigate = productionNavigate;

// Explicit typed home paths still expand, using the owning session even when
// another server is currently visible.
let operation = context.navigate('~/config', other);
assert.equal(new URL(reads.at(-1).url, 'https://fixture.invalid').searchParams.get('path'), '/home/other/config');
reads.at(-1).resolve({ path: '/home/other/config', entries: [] }); await operation;
operation = context.navigate('~', state);
assert.equal(new URL(reads.at(-1).url, 'https://fixture.invalid').searchParams.get('path'), state.home);
reads.at(-1).resolve({ path: state.home, entries: [] }); await operation;

// The desktop bridge cannot abort an in-flight IPC request. A late error from
// an older navigation must not replace the newer directory's successful UI.
const old = context.navigate('/missing', state), oldRead = reads.at(-1);
const latest = context.navigate('/latest', state), latestRead = reads.at(-1);
latestRead.resolve({ path: '/latest', entries: [] }); await latest;
oldRead.reject(new Error('old directory failed')); await old;
assert.equal(state.cwd, '/latest');
assert.equal(element('#file-status-count').textContent, 'loaded');
assert.equal(element('#path-input').value, '/latest');

// Responses from a closed/disconnected/replaced session cannot republish data.
for (const invalidate of [() => { state.connected = false; }, () => { state.closed = true; }, () => { context.sessions.set(state.id, { ...state }); }]) {
  state.connected = true; state.closed = false; context.sessions.set(state.id, state);
  const pending = context.navigate('/late', state); invalidate();
  reads.at(-1).resolve({ path: '/late', entries: [{ name: 'stale', kind: 'file' }] }); await pending;
  assert.equal(state.cwd, '/latest');
}
state.connected = true; state.closed = false; context.sessions.set(state.id, state);
const failure = context.navigate('/denied', state);
reads.at(-1).reject(new Error('permission denied'));
await assert.rejects(failure, /permission denied/);
assert.equal(element('#file-status-count').textContent, '目录读取失败');
console.log('PASS: literal tilde delete/upload targets; per-session home expansion; stale navigation success/failure and disconnected session isolation.');
