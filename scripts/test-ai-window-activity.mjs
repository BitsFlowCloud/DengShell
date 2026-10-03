import fs from 'node:fs';
import vm from 'node:vm';
import assert from 'node:assert/strict';
const source = fs.readFileSync(new URL('../web/ai-window.js', import.meta.url), 'utf8');
const tick = async () => {for (let i = 0; i < 12; i += 1) await Promise.resolve();};
function fixture(lock = {enabled: true, idleSeconds: 4, locked: false}) {
  let now = 10000, serial = 0;
  const handlers = new Map(), calls = [], timers = [], activityResponses = [], messages = [], closed = [];
  const document = {hidden: false};
  const window = {CLOUDSHELL: {aiWindowId: 'a'.repeat(48), token: 'b'.repeat(48), base: '', securityLock: lock}, addEventListener(name, handler, options) {handlers.set(name, {handler, options});}, close() {}};
  const context = vm.createContext({window, document, URLSearchParams, location: {protocol: 'http:', hostname: '127.0.0.1', search: '', hash: ''}, Date: {now: () => now}, crypto: {randomUUID: () => 'request-' + (++serial)},
    setTimeout(fn, delay) {timers.push({fn, delay}); return timers.length;}, clearTimeout() {},
    fetch(url, options = {}) {
      calls.push({url, options});
      if (url.endsWith('/activity')) return new Promise((resolve, reject) => activityResponses.push({resolve: value => resolve({ok: true, json: async () => value}), reject, locked423: () => resolve({ok: false, status: 423, json: async () => ({error: 'locked', code: 'DENGSHELL_LOCKED'})})}));
      return Promise.resolve({ok: true, json: async () => url.includes('?side=assistant') ? {closed: false, events: messages.splice(0)} : {ok: true}});
    },
  });
  vm.runInContext(source, context);
  window.DengAIWindowTransport.onClosed(reason => closed.push(reason));
  return {window, document, calls, timers, handlers, activityResponses, messages, closed, now(value) {now = value;}, emit(name, isTrusted = true) {return handlers.get(name).handler({isTrusted});}, get activityCalls() {return calls.filter(item => item.url.endsWith('/activity'));}};
}

// Only the documented genuine user inputs are registered. Broker keepalives and
// model progress never invoke the idle activity endpoint.
{
  const f = fixture();
  for (const name of ['pointerdown', 'keydown', 'wheel', 'touchstart']) {
    assert.ok(f.handlers.has(name)); assert.equal(f.handlers.get(name).options.passive, true);
    await f.emit(name, false);
  }
  assert.equal(f.activityCalls.length, 0);
  f.document.hidden = true; await f.emit('keydown'); assert.equal(f.activityCalls.length, 0);
  f.document.hidden = false; f.window.DengShellWindowHidden = true; await f.emit('pointerdown'); assert.equal(f.activityCalls.length, 0); f.window.DengShellWindowHidden = false;
  const ready = f.window.DengAIWindowTransport.ready().catch(() => {}); await tick();
  for (let index = 0; index < 3; index += 1) {
    f.messages.push({seq: index + 1, payload: {type: 'state', busy: true, events: [{role: 'assistant', value: 'automated progress'}]}});
    const timer = f.timers.filter(item => item.delay === 250).at(-1); assert.ok(timer); timer.fn(); await tick();
  }
  assert.equal(f.activityCalls.length, 0);
  f.window.DengAIWindowTransport.close(); await ready;
}

// Genuine input reports activity once; concurrent events are coalesced and the
// returned policy updates the same 100–1000 ms throttle used by the main UI.
{
  const f = fixture();
  const first = f.emit('keydown'); assert.equal(f.activityCalls.length, 1);
  assert.equal(f.activityCalls[0].options.method, 'POST'); assert.equal(f.activityCalls[0].options.body, '{}');
  f.now(10010); await f.emit('wheel'); assert.equal(f.activityCalls.length, 1);
  f.activityResponses[0].resolve({enabled: true, idleSeconds: 1, locked: false}); await first;
  f.now(10249); await f.emit('touchstart'); assert.equal(f.activityCalls.length, 1);
  f.now(10250); const second = f.emit('pointerdown'); assert.equal(f.activityCalls.length, 2);
  f.activityResponses[1].resolve({enabled: true, idleSeconds: 1, locked: false}); await second;
  f.now(11000); const denied = f.emit('keydown'); f.activityResponses[2].resolve({enabled: true, idleSeconds: 1, locked: true}); await denied;
  assert.equal(f.closed.length, 1); await assert.rejects(f.window.DengAIWindowTransport.request('send', {text: 'no'}), /关闭/);
  f.now(20000); await f.emit('keydown'); assert.equal(f.activityCalls.length, 3);
  assert.ok(!f.calls.some(item => /unlock|authorize/.test(item.url)));
}

// A policy enabled after this child opened must still see genuine input. An
// authoritative 423 or network failure always stops, never auto-unlocks.
for (const failure of ['locked423', 'reject']) {
  const f = fixture({enabled: false, idleSeconds: 0, locked: false});
  const action = f.emit('keydown'); assert.equal(f.activityCalls.length, 1);
  if (failure === 'locked423') f.activityResponses[0].locked423(); else f.activityResponses[0].reject(new Error('offline'));
  await action; assert.equal(f.closed.length, 1); assert.ok(!f.calls.some(item => /unlock|authorize/.test(item.url)));
}
console.log('PASS: trusted visible AI-window input activity, synthetic/hidden/native-hidden filtering, no polling/model idle extension, throttle/single-flight, changed policy and locked/423/network fail-closed.');
