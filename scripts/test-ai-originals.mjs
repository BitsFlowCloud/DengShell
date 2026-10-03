import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

const source = fs.readFileSync(new URL('../web/ai-assistant.js', import.meta.url), 'utf8');
function section(start, end) {
  const a = source.indexOf(start), b = source.indexOf(end, a);
  assert(a >= 0 && b > a, start); return source.slice(a, b);
}
function fixture() {
  let serial = 0, locked = false;
  const context = vm.createContext({
    TextEncoder, CHILD: false, opened: true, logEvents: [], logRevision: 0,
    crypto: {randomUUID: () => 'reply-' + (++serial)},
    window: {DengShellAIWindowOwner: {publish() {}}},
    locked: () => locked,
  });
  vm.runInContext(section('  const originalReplies =', '  if (!trigger) return;') +
    section('  function addMessage(', '\n  function render(') +
    section('  async function dispatch(', '\n  function applySnapshot(') +
    '\nglobalThis.cacheStats=()=>({bytes:originalReplyBytes,size:originalReplies.size,events:logEvents});', context);
  return {context, setLocked(value) {locked = value;}};
}

// Read the actual owner cache via its actual typed dispatch entry point. A
// surrogate pair placed at the first boundary must survive separate messages.
{
  const {context: c, setLocked} = fixture();
  const original = 'x'.repeat(63999) + '😀' + '中'.repeat(70000) + '\r\n\n';
  c.addMessage('assistant', original, {model: 'frozen-model'});
  const event = c.logEvents[0];
  assert.equal(event.value, original.slice(0, 16000)); assert.equal(event.model, 'frozen-model');
  assert.equal(event.sourceLength, original.length); assert.equal(event.truncated, true); assert.equal(event.sourceAvailable, true);
  assert.equal(c.cacheStats().bytes, Buffer.byteLength(original));
  const chunks = []; let offset = 0;
  while (offset < original.length) {
    const value = await c.dispatch('get_original', {sourceId: event.sourceId, offset});
    assert(value.text.length > 0 && value.text.length <= 64000);
    assert.equal(value.next, offset + value.text.length); assert.equal(value.total, original.length);
    // Each chunk must independently survive UTF-8, as it does through Go JSON.
    assert.equal(Buffer.from(value.text).toString('utf8'), value.text);
    chunks.push(value.text); offset = value.next;
    assert.equal(value.done, offset === original.length);
  }
  assert.equal(chunks[0].length, 63999); assert.equal(chunks.join(''), original);
  for (const invalid of [-1, 0.5, original.length + 1, NaN, '0']) {
    await assert.rejects(c.dispatch('get_original', {sourceId: event.sourceId, offset: invalid}), /位置无效/);
  }
  await assert.rejects(c.dispatch('get_original', {sourceId: 'missing', offset: 0}), /缓存移除/);
  setLocked(true); await assert.rejects(c.dispatch('get_original', {sourceId: event.sourceId, offset: 0}), /不可用/);
  setLocked(false); c.opened = false; await assert.rejects(c.dispatch('get_original', {sourceId: event.sourceId, offset: 0}), /不可用/);
}

// The bound is UTF-8 bytes, not JS character count. Eviction preserves the
// preview and marks the unavailable original instead of fabricating a copy.
{
  const {context: c} = fixture();
  const original = '中'.repeat(3 * 1024 * 1024); // 9 MiB per original.
  c.addMessage('assistant', original, {model: 'one'});
  const first = c.logEvents[0].sourceId;
  c.addMessage('assistant', original, {model: 'two'});
  assert.equal(c.cacheStats().size, 1); assert.equal(c.cacheStats().bytes, 9 * 1024 * 1024);
  assert(c.cacheStats().bytes <= 16 * 1024 * 1024);
  assert.equal(c.logEvents[0].sourceAvailable, false); assert.equal(c.logEvents[0].value, original.slice(0, 16000));
  await assert.rejects(c.dispatch('get_original', {sourceId: first, offset: 0}), /缓存移除/);
  const retained = c.logEvents[1].sourceId;
  assert.equal((await c.dispatch('get_original', {sourceId: retained, offset: 0})).text, original.slice(0, 64000));
  for (let index = 0; index < 101; index += 1) c.addMessage('notice', 'event ' + index);
  assert.equal(c.cacheStats().size, 0); assert.equal(c.cacheStats().bytes, 0);
  await assert.rejects(c.dispatch('get_original', {sourceId: retained, offset: 0}), /缓存移除/);
}
{
  const {context: c} = fixture();
  c.addMessage('assistant', 'short\n', {model: 'short-model'});
  assert.equal(c.logEvents[0].value, 'short\n'); assert.equal(c.logEvents[0].truncated, false);
  assert.equal(c.logEvents[0].sourceId, undefined); assert.equal(c.cacheStats().size, 0);
}

// Exercise the production child transport too, so a missing get_original
// allowlist entry cannot be concealed by the browser test's fixture transport.
{
  const transport = fs.readFileSync(new URL('../web/ai-window.js', import.meta.url), 'utf8');
  const calls = [], timers = [];
  const window = {CLOUDSHELL: {aiWindowId: 'a'.repeat(48)}, addEventListener() {}, close() {}};
  const context = vm.createContext({window, document: {hidden: false}, URLSearchParams,
    location: {protocol: 'http:', hostname: 'localhost', search: '', hash: ''},
    crypto: {randomUUID: () => 'request-original'},
    setTimeout(fn) {timers.push(fn); return timers.length;}, clearTimeout() {},
    fetch(url, options) {calls.push({url, options}); return Promise.resolve({ok: true, json: async () => ({ok: true})});},
  });
  vm.runInContext(transport, context);
  const pending = window.DengAIWindowTransport.request('get_original', {sourceId: 'reply', offset: 63999});
  const rejected = pending.catch(error => error);
  for (let index = 0; index < 8; index += 1) await Promise.resolve();
  const message = calls.find(call => call.url.endsWith('/messages'));
  assert(message, 'get_original must pass the production child action allowlist');
  const body = JSON.parse(message.options.body);
  assert.equal(body.payload.action, 'get_original'); assert.deepEqual(body.payload.body, {sourceId: 'reply', offset: 63999});
  window.DengAIWindowTransport.close(); assert.match((await rejected).message, /关闭/);
}
// Closing/locking a transport must discard its cached snapshot, including
// reply previews; a later subscriber must never receive those closed data.
{
  const transport = fs.readFileSync(new URL('../web/ai-window.js', import.meta.url), 'utf8');
  const received = [], afterClose = [];
  const window = {CLOUDSHELL: {aiWindowId: 'b'.repeat(48)}, addEventListener() {}, close() {}};
  const context = vm.createContext({window, document: {hidden: false}, URLSearchParams,
    location: {protocol: 'http:', hostname: 'localhost', search: '', hash: ''},
    crypto: {randomUUID: () => 'hello-request'}, setTimeout() {return 1;}, clearTimeout() {},
    fetch(url, options) {return Promise.resolve({ok: true, json: async () => options.method === 'GET' ? {events: [
      {seq: 1, payload: {type: 'state', events: [{role: 'assistant', value: 'private preview'}]}},
      {seq: 2, payload: {type: 'result', id: 'hello-request', value: {ok: true}}},
    ]} : {ok: true}});},
  });
  vm.runInContext(transport, context);
  window.DengAIWindowTransport.subscribe(value => received.push(value));
  await window.DengAIWindowTransport.ready(); assert.equal(received[0].events[0].value, 'private preview');
  window.DengAIWindowTransport.close();
  window.DengAIWindowTransport.subscribe(value => afterClose.push(value));
  assert.equal(afterClose.length, 0);
}
console.log('PASS: AI original cache, UTF-8 byte cap, eviction, Unicode-safe chunks, owner lock/close guard, closed snapshot cleanup and production child original request allowlist.');
