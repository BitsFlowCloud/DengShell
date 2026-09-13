import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import assert from 'node:assert/strict';

// Exercise the shipped queue/cancellation functions with controllable network
// completions: scheduling must depend on finished uploads, not elapsed time.
const source = readFileSync(new URL('../web/app.js', import.meta.url), 'utf8');
const pump = source.slice(source.indexOf('function pumpUploads()'), source.indexOf('async function uploadBrowser('));
const cancel = source.slice(source.indexOf('async function cancelTask('), source.indexOf('async function retryTask('));
assert.ok(pump && cancel, 'upload functions not found');
const tasks = new Map();
for (let i = 1; i <= 6; i++) tasks.set(String(i), { id: String(i), status: 'queued', file: {} });
tasks.set('native', { id: 'native', status: 'queued' });
const started = [], finish = new Map(), deleted = [];
let inFlight = 0, maximum = 0;
const context = vm.createContext({
  localTasks: tasks,
  renderTransfers() {},
  async remove(path) { deleted.push(path); },
  uploadBrowser(task) {
    started.push(task.id);
    maximum = Math.max(maximum, ++inFlight);
    return new Promise(resolve => finish.set(task.id, () => {
      task.status = 'done';
      inFlight--;
      resolve();
    }));
  },
});
vm.runInContext(`let activeUploads = 0; ${pump}\n${cancel}`, context);
context.pumpUploads();
assert.deepEqual(started, ['1', '2', '3', '4']);
assert.equal(inFlight, 4);
assert.equal(tasks.get('5').status, 'queued');
assert.equal(tasks.get('6').status, 'queued');
await context.cancelTask(tasks.get('6'));
assert.equal(tasks.get('6').status, 'cancelled');
assert.equal(deleted.length, 0, 'a browser-only queued file has no server request to cancel');
finish.get('1')();
await new Promise(resolve => setImmediate(resolve));
assert.deepEqual(started, ['1', '2', '3', '4', '5'], 'fifth file must fill the released slot');
assert.equal(inFlight, 4);
for (const id of ['2', '3', '4', '5']) finish.get(id)();
await new Promise(resolve => setImmediate(resolve));
assert.equal(maximum, 4);
assert.equal(inFlight, 0);
assert.equal(vm.runInContext('activeUploads', context), 0);
assert.equal(tasks.get('native').status, 'queued', 'browser queue must not start native tasks');
await context.cancelTask(tasks.get('native'));
assert.equal(tasks.get('native').status, 'cancelled');
assert.deepEqual(deleted, ['/api/transfers/native']);
let aborted = false;
const waitingHTTP = { id: 'waiting-http', file: {}, status: 'queued', xhr: { abort() { aborted = true; } } };
tasks.set(waitingHTTP.id, waitingHTTP);
context.pumpUploads();
assert.equal(started.includes(waitingHTTP.id), false, 'a server-queued active HTTP request must not be started twice');
await context.cancelTask(waitingHTTP);
assert.equal(aborted, true, 'cancel must abort an existing request that is queued in the backend');
assert.equal(waitingHTTP.status, 'cancelled');
assert.deepEqual(deleted, ['/api/transfers/native', '/api/transfers/waiting-http']);
console.log('Upload queue: four concurrent files, fifth waits/refills, queued cancellation, native isolation and server-queued HTTP cancellation passed');
