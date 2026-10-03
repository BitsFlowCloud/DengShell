import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

const source = fs.readFileSync(new URL('../web/ai-assistant.js', import.meta.url), 'utf8');
const start = source.indexOf('  const MAX_ROUNDS'), end = source.indexOf('\n  const trigger =');
assert.ok(start > 0 && end > start);
const context = vm.createContext({});
vm.runInContext(source.slice(start, end) + '\nglobalThis.createRunner = createRunner;', context);
const pause = () => new Promise(resolve => setImmediate(resolve));
const assistant = (content = '', toolCalls = []) => ({message: {role: 'assistant', content, toolCalls}});
const invocation = (name, id = 'call-1', args = {}) => ({id, name, arguments: JSON.stringify(args)});
function fixture() {
  let enabled = true, token = 'first', id = 'session-1', locked = false, connected = true, transitioning = false, counter = 0, model = 'fixture-model-A', modelLookups = 0;
  const pending = [], cancelled = [], notifications = [], executed = [], definitions = ['read_terminal', 'send_terminal', 'select_session', 'connect_profile'].map(name => ({name}));
  const tools = {
    definitions,
    context() {return {sessionId: id, sessionToken: token, locked, sessions: [{sessionId: id, connected, transitioning}]};},
    async execute(name, args, guard) {guard(); executed.push({name, args}); return {ok: true};},
  };
  const runner = context.createRunner({
    tools, enabled: () => enabled, changed() {}, newID: () => `request-${++counter}`,
    notify(role, value, metadata) {notifications.push({role, value, metadata});},
    modelName() {modelLookups += 1; return model;},
    call(path, body) {if (path === 'cancel') {cancelled.push(body); return Promise.resolve({ok: true});} return new Promise((resolve, reject) => pending.push({path, body, resolve, reject}));},
  });
  return {runner, tools, pending, cancelled, notifications, executed,
    get modelLookups() {return modelLookups;},
    set(values) {if ('enabled' in values) enabled = values.enabled; if ('token' in values) token = values.token; if ('id' in values) id = values.id; if ('locked' in values) locked = values.locked; if ('connected' in values) connected = values.connected; if ('transitioning' in values) transitioning = values.transitioning; if ('model' in values) model = values.model;},
  };
}

// Actual round trips contain canonical tool results and resume automatically.
{
  const f = fixture(), run = f.runner.send('检查终端', 'provider');
  assert.equal(f.pending.length, 1);
  f.pending[0].resolve(assistant('正在检查', [invocation('read_terminal')])); await pause();
  assert.equal(f.executed.length, 1); assert.equal(f.pending.length, 2);
  const messages = f.pending[1].body.messages;
  assert.equal(messages.at(-1).role, 'tool'); assert.equal(messages.at(-1).toolCallId, 'call-1');
  f.pending[1].resolve(assistant('已检查')); assert.equal(await run, true); assert.equal(f.runner.busy, false);
}

// Stopping must cancel native IPC at the backend and invalidate late replies.
{
  const f = fixture(), run = f.runner.send('开始', 'provider');
  f.runner.stop(); assert.equal(f.cancelled[0].requestId, 'request-1'); assert.equal(f.runner.busy, false);
  f.pending[0].resolve(assistant('late', [invocation('send_terminal')])); await run;
  assert.equal(f.executed.length, 0); assert.equal(f.pending.length, 1);
  assert.ok(!f.notifications.some(item => item.value === 'late'));
}

// A new run cannot be stopped or overwritten by an old delayed completion.
{
  const f = fixture(), first = f.runner.send('first', 'provider');
  f.runner.stop(); const second = f.runner.send('second', 'provider');
  f.pending[0].resolve(assistant('old')); await first;
  assert.equal(f.runner.busy, true);
  f.pending[1].resolve(assistant('new')); await second;
  assert.ok(!f.notifications.some(item => item.value === 'old'));
}

// Session changes, replacement objects, locking, disconnect and handoff all
// reject a late tool batch before its first side effect.
for (const change of [{token: 'other', id: 'session-2'}, {token: 'replacement'}, {locked: true}, {enabled: false}, {connected: false}, {transitioning: true}]) {
  const f = fixture(), run = f.runner.send('开始', 'provider'); f.set(change);
  f.pending[0].resolve(assistant('', [invocation('send_terminal')])); await run;
  assert.equal(f.executed.length, 0, JSON.stringify(change));
}

// Explicit AI selection is the only way to authorize another session during a
// run. A forged AI event outside the matching tool cannot retarget a run.
{
  const f = fixture(); f.tools.execute = async (name, args, guard) => {
    guard(); f.set({token: 'second', id: 'session-2'});
    f.runner.targetChanged({source: 'ai', reason: name, sessionId: 'session-2'}); guard();
    return {targetChanged: true, sessionId: 'session-2'};
  };
  const run = f.runner.send('切换服务器', 'provider');
  f.pending[0].resolve(assistant('', [invocation('select_session')])); await pause();
  assert.equal(f.pending.length, 2); f.pending[1].resolve(assistant('完成')); assert.equal(await run, true);
}
{
  const f = fixture(), run = f.runner.send('开始', 'provider'); f.set({token: 'second', id: 'session-2'});
  f.runner.targetChanged({source: 'ai', reason: 'select_session', sessionId: 'session-2'});
  f.pending[0].resolve(assistant('', [invocation('send_terminal')])); await run; assert.equal(f.executed.length, 0);
}

// Starting without a session remains useful: the model can connect a saved
// profile, accept its explicit new target, then inspect the new terminal.
{
  const f = fixture(); f.set({token: '', id: null, connected: false});
  f.tools.execute = async (name, args, guard) => {
    guard(); f.set({token: 'new', id: 'new-session', connected: true});
    f.runner.targetChanged({source: 'ai', reason: name, sessionId: 'new-session'}); guard(); return {ok: true};
  };
  const run = f.runner.send('连接我的服务器', 'provider');
  f.pending[0].resolve(assistant('', [invocation('connect_profile')])); await pause();
  assert.equal(f.pending.length, 2); f.pending[1].resolve(assistant('已连接')); await run;
}

// Tool errors are faithfully returned to the model, and unsupported tools never
// get delegated to the bridge. Model HTML is kept as literal text.
{
  const f = fixture(), run = f.runner.send('操作', 'provider');
  const hostile = '<img src=x onerror="window.exfiltrate()">';
  f.pending[0].resolve(assistant(hostile, [invocation('invented_tool')])); await pause();
  assert.equal(f.executed.length, 0);
  assert.match(f.pending[1].body.messages.at(-1).content, /不支持的操作/);
  assert.equal(f.notifications.find(item => item.role === 'assistant').value, hostile);
  f.pending[1].resolve(assistant('不能执行该操作')); await run;
}
assert.doesNotMatch(source, /\.innerHTML\s*=|insertAdjacentHTML|\beval\s*\(/);
// Markdown/text DOM behavior is exercised by the real browser conversation and
// renderer tests; runner notifications preserve the provider's original text.

// Model captions are frozen at run start across asynchronous replies and tool
// rounds, even if the configuration object is edited before those replies.
{
  const f = fixture(), first = f.runner.send('first', 'provider');
  f.set({model: 'fixture-model-B'});
  f.pending[0].resolve(assistant('first round', [invocation('read_terminal')])); await pause();
  f.set({model: 'fixture-model-C'});
  f.pending[1].resolve(assistant('second round')); assert.equal(await first, true);
  assert.equal(f.modelLookups, 1);
  assert.deepEqual(f.notifications.filter(item => item.role === 'assistant').map(item => item.metadata.model), ['fixture-model-A', 'fixture-model-A']);
  const next = f.runner.send('next', 'provider');
  f.pending[2].resolve(assistant('next run')); assert.equal(await next, true);
  assert.equal(f.modelLookups, 2);
  assert.equal(f.notifications.filter(item => item.role === 'assistant').at(-1).metadata.model, 'fixture-model-C');
}

// Stopping during an awaited tool prevents both the next action and the next
// model call, even when the tool transport itself cannot abort.
{
  const f = fixture(); let finish;
  f.tools.execute = async (_name, _args, guard) => {guard(); await new Promise(resolve => {finish = resolve;}); guard(); return {ok: true};};
  const run = f.runner.send('开始', 'provider');
  f.pending[0].resolve(assistant('', [invocation('read_terminal'), invocation('send_terminal', 'call-2')])); await pause();
  f.runner.stop(); finish(); await run; assert.equal(f.pending.length, 1);
}

// Provider switches/clear and user session switches between completed runs must
// not replay the previous provider's opaque protocol data or server context.
for (const reset of ['clear', 'session']) {
  const f = fixture(), first = f.runner.send('first', 'provider-a');
  const reply = assistant('first answer'); reply.message.providerData = {opaque: 'old-provider'};
  f.pending[0].resolve(reply); await first;
  if (reset === 'clear') f.runner.clear(); else {f.set({token: 'next', id: 'session-2'}); f.runner.check();}
  const second = f.runner.send('second', 'provider-b');
  assert.equal(f.pending[1].body.messages.length, 2);
  f.pending[1].resolve(assistant('second answer')); await second;
}

// A model which asks for tools forever is bounded and leaves no partial history.
{
  const f = fixture(), run = f.runner.send('bounded', 'provider');
  for (let round = 0; round < 24; round += 1) {
    assert.equal(f.pending.length, round + 1);
    f.pending[round].resolve(assistant('', [invocation('read_terminal', `call-${round}`)])); await pause();
  }
  assert.equal(await run, false); assert.equal(f.pending.length, 24); assert.equal(f.runner.busy, false);
  assert.ok(f.notifications.some(item => item.role === 'error' && /24/.test(item.value)));
}
console.log('PASS: AI automatic tool loop, explicit target changes, cancellation, stale responses, lock/handoff/session guards, protocol history isolation, tool rejection, literal model text and bounded runs.');
