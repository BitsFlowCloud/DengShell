import fs from 'node:fs';
import vm from 'node:vm';
import assert from 'node:assert/strict';

const source = fs.readFileSync(new URL('../web/ai-assistant.js', import.meta.url), 'utf8');
const owner = fs.readFileSync(new URL('../web/ai-window-owner.js', import.meta.url), 'utf8');
function section(text, start, end) {
  const a = text.indexOf(start), b = text.indexOf(end, a);
  assert.ok(a >= 0 && b > a, start); return text.slice(a, b);
}
const redacted = section(source, '  function redactedSettings(', '\n  function snapshot(');
const dispatch = section(source, '  async function dispatch(', '\n  function applySnapshot(');
const pending = [], calls = [];
const context = vm.createContext({
  CHILD: false, opened: true, enabled: true, controllerSettingsBusy: false,
  settings: {provider: 'one', providers: [{id: 'one', model: 'model'}]},
  locked: () => false, render() {}, fillProviders() {}, clearConversation() {}, addMessage() {},
  errorText: error => error.message,
  runner: {busy: false, clear() {}, stop() {}, send(text, provider) {calls.push({text, provider}); return Promise.resolve();}},
  call: (path, body) => new Promise((resolve, reject) => pending.push({path, body, resolve, reject})),
});
vm.runInContext(redacted + dispatch, context);

// Same-capability windows still use a closed action set, never arbitrary API,
// script, terminal or tool requests supplied through the message broker.
for (const name of ['eval', 'execute', 'chat', '/api/sessions/one/input', 'terminal_input']) {
  await assert.rejects(context.dispatch(name, {text: 'no'}), /不支持/);
}
assert.equal(pending.length, 0); assert.equal(calls.length, 0);

// A save/model discovery operation blocks new agent work in the owner, even if
// a child sends a request before its next state snapshot disables the button.
for (const action of ['save_settings', 'get_models']) {
  context.opened = true; context.enabled = true;
  const operation = context.dispatch(action, {provider: 'one'});
  assert.equal(context.controllerSettingsBusy, true);
  await assert.rejects(context.dispatch('send', {text: 'must wait', provider: 'one'}), /尚未启用|正在运行|改变/);
  await assert.rejects(context.dispatch('save_settings', {}), /正在处理/);
  // Stop is always accepted, independent of a slow settings request.
  assert.deepEqual(JSON.parse(JSON.stringify(await context.dispatch('stop'))), {ok: true});
  context.opened = false; context.enabled = false;
  pending.at(-1).resolve(action === 'save_settings' ? {provider: 'one', providers: [{id: 'one', apiKey: 'private-key', hasKey: true}]} : {models: ['one']});
  const response = await operation;
  assert.equal(context.enabled, false); assert.equal(context.opened, false); assert.equal(context.controllerSettingsBusy, false);
  assert.doesNotMatch(JSON.stringify(response), /private-key|apiKey/);
}
assert.equal(calls.length, 0);
await assert.rejects(context.dispatch('set_enabled', {enabled: true}), /不可用/);

// Snapshot settings are built from a whitelist, including future accidentally
// added secret fields on providers or nested per-configuration proxies.
const sanitized = context.redactedSettings({provider: 'one', timeout: 120, providers: [{id: 'one', apiKey: 'key', nestedSecrets: {token: 'secret'}, hasKey: true, proxy: {type: 'http', host: 'proxy.example', port: 8080, user: 'alice', password: 'proxy-secret', hasPassword: true, nestedSecrets: {token: 'nested-proxy-secret'}}}], presets: [{name: 'preset', apiKey: 'other'}], token: 'root-secret'});
assert.doesNotMatch(JSON.stringify(sanitized), /apiKey|nestedSecrets|root-secret|other|proxy-secret/);
assert.equal(sanitized.providers[0].hasKey, true);
assert.equal(sanitized.providers[0].proxy.hasPassword, true);
assert.equal(sanitized.providers[0].proxy.type, 'http');
assert.equal(sanitized.providers[0].proxy.host, 'proxy.example');
assert.equal(sanitized.providers[0].proxy.password, undefined);
assert.equal(sanitized.presets, undefined);

// Multi-byte terminal logs remain within the byte budget and trimming never
// changes the original controller's in-memory log list.
const state = {type: 'state', events: Array.from({length: 100}, (_, i) => ({role: 'assistant', value: '中'.repeat(16000), id: i})), settings: sanitized};
const budget = vm.createContext({TextEncoder, window: {DengShellAIController: {snapshot: () => ({...state})}}});
vm.runInContext(section(owner, '  function prepareState(', '\n  function publish('), budget);
const bounded = budget.prepareState();
assert.ok(new TextEncoder().encode(JSON.stringify(bounded)).length <= 250000);
assert.equal(state.events.length, 100); assert.ok(bounded.events.length < 100);

const html = fs.readFileSync(new URL('../web/ai-window.html', import.meta.url), 'utf8');
assert.doesNotMatch(html, /src="(?:app|ai-tools|security-lock)\.js/);
assert.match(source, /const runner = CHILD \?/);
assert.match(source, /if \(entry\) \{ entry\.detail\.textContent/); // Orphaned operation results are ignored safely.
assert.doesNotMatch(source, /\.innerHTML\s*=|insertAdjacentHTML|\beval\s*\(/);
assert.doesNotMatch(owner, /\.innerHTML\s*=|insertAdjacentHTML|\beval\s*\(/);
assert.doesNotMatch(fs.readFileSync(new URL('../web/ai-assistant.css', import.meta.url), 'utf8'), /ai-assistant-open.*app-shell/);
console.log('PASS: owner typed actions, settings/send race, stop during pending settings, closed owner never re-enables, redacted snapshots, byte-bounded logs and isolated child scripts.');
