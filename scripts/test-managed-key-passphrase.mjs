import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const appSource = readFileSync(new URL('../web/app.js', import.meta.url), 'utf8');
const toolsSource = readFileSync(new URL('../web/workspace-tools.js', import.meta.url), 'utf8');
const connectSource = appSource.slice(appSource.indexOf('async function connectProfile('), appSource.indexOf('\nfunction showConnectionProgress('));

async function connectCase({ keyId = 'shared', keyPath = '/fixture/external-key', hasSecret = false, failures = [], answer = 'typed-once' } = {}) {
  const prompts = [], requests = [], messages = [], forms = [];
  const profile = { id: 'profile', name: 'fixture', auth: 'key', keyId, keyPath, hasSecret };
  const context = {
    profiles: [profile], managedKeys: [{ id: 'shared', encrypted: true, hasPassphrase: false }],
    sessions: new Map(), connecting: new Set(), connectionAttempts: new Map(), credentials: new Map(),
    nextSessionOrder: 0, activeID: null, AbortController, window: {},
    makeSessionState: info => ({ ...info, host: { dataset: {} }, openTerminalSocket() {} }),
    current: () => null, createTerminal() {}, activate() {}, setDrawer() {}, renderTabs() {}, renderConnections() {},
    showConnectionForm(profile) { forms.push(profile.id); },
    showConnectionProgress(_state, message) { messages.push(message); }, toast() {}, updateStatus() {},
    navigate: async () => {}, closeSession: async () => {},
    ask: async options => { prompts.push(options); return answer; },
    api: async (_path, options) => {
      requests.push(JSON.parse(options.body));
      if (failures.length) throw Object.assign(new Error('fixture failure'), { code: failures.shift() });
      return { id: 'connected', home: '/fixture' };
    },
  };
  vm.createContext(context); vm.runInContext(connectSource, context);
  const session = await context.connectProfile('profile', false, { refreshHistory: false });
  return { session, prompts, requests, messages, forms, sessions: context.sessions.size };
}

// Even a detached window whose old metadata says "no saved passphrase" must
// try the backend's current managed key before asking the user for a secret.
let result = await connectCase();
assert.equal(result.session?.id, 'connected');
assert.equal(result.prompts.length, 0);
assert.equal(result.requests[0].secret, '');
result = await connectCase({ failures: ['ssh_key_passphrase_required'] });
assert.equal(result.prompts.length, 1);
assert.equal(result.requests.length, 2);
assert.equal(result.requests[1].secret, 'typed-once');
assert.match(result.prompts[0].title, /^私钥口令/);
assert.equal(result.session?.id, 'connected');
result = await connectCase({ failures: ['ssh_key_passphrase_required'], answer: null });
assert.equal(result.session, null);
assert.equal(result.requests.length, 1, 'cancel must not retry');
for (const code of ['ssh_authentication_failed', 'ssh_managed_key_invalid', 'ssh_private_key_invalid']) {
  result = await connectCase({ failures: [code] });
  assert.equal(result.session, null);
  assert.equal(result.prompts.length, 0, `${code} must not ask for an unrelated per-connection passphrase`);
}
result = await connectCase({ keyId: '' });
assert.equal(result.prompts.length, 1, 'external private key still uses connection credentials');
result = await connectCase({ keyId: '', keyPath: '' });
assert.deepEqual(result.forms, ['profile'], 'missing private key must open the connection editor');
assert.equal(result.prompts.length, 0, 'missing private key must not ask for a passphrase');
assert.equal(result.requests.length, 0, 'missing private key must not start SSH');
assert.equal(result.sessions, 0, 'missing private key must not create an empty terminal');

const nodes = new Map();
const element = selector => {
  if (!nodes.has(selector)) nodes.set(selector, { hidden: false, textContent: '', showModal() {} });
  return nodes.get(selector);
};
const form = { elements: Object.fromEntries(['id', 'auth', 'keyId'].map(name => [name, { value: '' }])) };
nodes.set('#connection-form', form);
form.elements.auth.value = 'key'; form.elements.keyId.value = 'shared';
const context = { $: element, profiles: [], managedKeys: [{ id: 'shared', encrypted: true, hasPassphrase: true }], native: () => null };
vm.createContext(context);
vm.runInContext(appSource.slice(appSource.indexOf('function updateAuthFields('), appSource.indexOf("\n$('#connection-form').elements.auth.onchange")), context);
context.updateAuthFields();
assert.equal(element('#secret-field').hidden, true);
assert.equal(element('#remember-secret-field').hidden, true);
assert.equal(element('#managed-key-secret-note').hidden, false);
context.managedKeys[0].hasPassphrase = false;
context.updateAuthFields();
assert.equal(element('#secret-field').hidden, false, 'legacy key keeps its existing fallback');
assert.equal(element('#managed-key-secret-note').hidden, true);
form.elements.auth.value = 'password';
context.updateAuthFields();
assert.equal(element('#secret-field').hidden, false);
assert.equal(element('#managed-key-secret-note').hidden, true);

const editor = {
  elements: Object.fromEntries(['id', 'name', 'passphrase'].map(name => [name, { value: '', focus() {} }])),
  reset() { for (const field of Object.values(this.elements)) field.value = ''; },
};
nodes.set('#key-editor-form', editor);
vm.runInContext(toolsSource.slice(toolsSource.indexOf("let keyEditorMode = 'import'"), toolsSource.indexOf('\nfunction updateProxyFields(')), context);
context.editKey('edit', { id: 'shared', name: 'old', encrypted: true, hasPassphrase: false });
assert.equal(element('#key-import-fields').hidden, true);
assert.equal(element('#key-passphrase-field').hidden, false);
assert.equal(editor.elements.passphrase.required, true);
context.editKey('edit', { id: 'shared', name: 'saved', encrypted: true, hasPassphrase: true });
assert.equal(editor.elements.passphrase.value, '', 'saved secret must never populate the form');
assert.equal(editor.elements.passphrase.required, false);
assert.match(editor.elements.passphrase.placeholder, /留空保留/);
context.editKey('edit', { id: 'plain', name: 'plain', encrypted: false });
assert.equal(element('#key-passphrase-field').hidden, true);
assert.equal(editor.elements.passphrase.required, false);
context.editKey('import');
assert.equal(editor.elements.passphrase.required, false, 'switching modes must clear legacy required state');
assert.match(element('#key-editor-note').textContent, /所有引用此密钥/);
console.log('PASS: managed key connections use backend credentials without prompts, including stale windows; legacy fallback/cancel/errors and key editor states remain correct.');
