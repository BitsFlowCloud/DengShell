import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

const source = fs.readFileSync(new URL('../web/ai-assistant.js', import.meta.url), 'utf8');
const start = source.indexOf('  const CONTEXT_BYTES'), end = source.indexOf('\n  const trigger =');
assert.ok(start > 0 && end > start);
const context = vm.createContext({TextEncoder});
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

function validExchanges(messages) {
  const pending = new Set();
  for (const message of messages) {
    if (message.role === 'tool') {assert(pending.delete(message.toolCallId), 'orphan tool result'); continue;}
    assert.equal(pending.size, 0, 'tool batch split by compaction');
    for (const call of message.toolCalls || []) pending.add(call.id);
  }
  assert.equal(pending.size, 0);
}

// Long upgrades exceed both previous ceilings and the backend's raw message
// count ceiling; context remains bounded and all tool batches stay complete.
{
  const f = fixture(), task = '从 Debian 12 升级到 Debian 13，完成后检查版本；不要重装系统', run = f.runner.send(task, 'provider');
  for (let round = 0; round < 180; round += 1) {
    assert.equal(f.pending.length, round + 1);
    const messages = f.pending[round].body.messages;
    validExchanges(messages); assert(messages.length <= 97);
    assert.equal(messages[1].content, task);
    f.pending[round].resolve(assistant('', [invocation('read_terminal', `call-${round}-a`), invocation('read_terminal', `call-${round}-b`)])); await pause();
  }
  assert.equal(f.executed.length, 360); assert.equal(f.runner.busy, true);
  f.pending[180].resolve(assistant('版本已检查，任务完成')); assert.equal(await run, true);
  assert(!f.notifications.some(item => item.role === 'error'));
}

// Unicode terminal output and opaque provider data must not grow without bound.
// A preserved assistant message is byte-for-byte untouched with its full result.
{
  const f = fixture(), run = f.runner.send('长输出任务', 'provider');
  f.tools.execute = async () => ({output: '中文升级输出🙂'.repeat(6000), untrusted: true});
  for (let round = 0; round < 100; round += 1) {
    const messages = f.pending[round].body.messages;
    assert(Buffer.byteLength(JSON.stringify(messages)) < 300000);
    validExchanges(messages);
    for (const message of messages.filter(item => item.providerData)) assert.equal(message.providerData.data.signature, 'opaque-signature');
    const reply = assistant('正在处理', [invocation('read_terminal', `long-${round}`)]);
    reply.message.providerData = {data: {signature: 'opaque-signature', thinking: '原生思考'.repeat(1000)}};
    f.pending[round].resolve(reply); await pause();
  }
  assert(f.pending.at(-1).body.messages.some(item => item.content?.includes('较早对话的截取记录')));
  f.runner.stop(); f.pending.at(-1).resolve(assistant('', [invocation('send_terminal')]));
  assert.equal(await run, false); assert.equal(f.executed.length, 0);
}

// One unusually large provider response is folded as a whole, never stripped of
// its signature while leaving a tool result behind.
{
  const f = fixture(), run = f.runner.send('保留目标', 'provider');
  const reply = assistant('x'.repeat(1000000), [invocation('read_terminal')]);
  reply.message.providerData = {data: {signature: 'giant', thinking: 'x'.repeat(1000000)}};
  f.pending[0].resolve(reply); await pause();
  const messages = f.pending[1].body.messages;
  validExchanges(messages); assert(Buffer.byteLength(JSON.stringify(messages)) < 300000);
  assert.equal(messages[1].content, '保留目标');
  f.pending[1].resolve(assistant('完成')); assert.equal(await run, true);
}
{
  const f = fixture(), text = '中文'.repeat(65000), run = f.runner.send('检查完整文件', 'provider');
  f.tools.execute = async () => ({text, sha256: 'f'.repeat(64), truncated: false});
  f.pending[0].resolve(assistant('', [invocation('read_terminal', 'large-file')])); await pause();
  const messages = f.pending[1].body.messages;
  validExchanges(messages);
  assert.equal(JSON.parse(messages.at(-1).content).text, text, 'latest large tool result must remain complete');
  assert(Buffer.byteLength(JSON.stringify(messages)) > 256000);
  f.pending[1].resolve(assistant('已完整读取')); assert.equal(await run, true);
}
{
  const f = fixture();
  const instructions = ['升级系统', '不要重启', '保留原有SSH端口', '不要删除用户数据', '最后检查服务', '保留当前内核', '不要关闭防火墙'];
  for (const instruction of instructions) {
    const run = f.runner.send(instruction, 'provider');
    f.pending.at(-1).resolve(assistant('收到')); assert.equal(await run, true);
  }
  const run = f.runner.send('继续处理', 'provider');
  for (let round = 0; round < 130; round++) {
    const messages = f.pending.at(-1).body.messages;
    validExchanges(messages);
    const userMessages = messages.filter(message => message.role === 'user').map(message => message.content).join('\n');
    for (const instruction of [...instructions, '继续处理']) assert(userMessages.includes(instruction), 'lost user constraint: ' + instruction);
    f.pending.at(-1).resolve(assistant('', [invocation('read_terminal', `constraints-${round}`)])); await pause();
  }
  f.pending.at(-1).resolve(assistant('完成')); assert.equal(await run, true);
}
console.log('PASS: AI long tasks (180 rounds / 360 actions), Unicode context compaction, complete signed tool exchanges, target changes, cancellation, stale responses, lock/handoff/session guards and protocol history isolation.');
