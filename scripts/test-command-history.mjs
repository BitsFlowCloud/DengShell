import { readFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import vm from 'node:vm';
import assert from 'node:assert/strict';
const read = name => readFileSync(new URL('../web/' + name, import.meta.url), 'utf8');
const app = read('app.js'), refinements = read('workspace-refinements.js'), windows = read('session-windows.js');
const makeState = app.slice(app.indexOf('function makeSessionState('), app.indexOf('// Desktop IPC'));
const history = refinements.slice(refinements.indexOf('function historyKey('), refinements.indexOf('async function copyText('));
const restore = windows.slice(windows.indexOf(' function installSnapshot('), windows.indexOf(' async function restore()'));
let entries = ['BEFORE'], revision = 0, loseResponse = null, handoff;
const profiles = new Set(['profile']); let knownHistory = true, liveSession = true;
const seen = new Set(), patches = [], notices = [];
async function api(path, options = {}) {
  if (path.includes('/api/windows/handoff/')) return structuredClone(handoff);
  assert.equal(path, '/api/profiles/profile/history');
  if (!profiles.has('profile') && !knownHistory && !liveSession) throw new Error('unknown profile');
  if (options.method) {
    const operation = JSON.parse(options.body);
    if (!seen.has(operation.operation)) {
      seen.add(operation.operation);
      if (options.method === 'DELETE') entries = [];
      else if (entries.at(-1) !== operation.command) entries.push(operation.command);
      entries = entries.slice(-200); revision++; knownHistory = true;
    }
    if (loseResponse === operation.command) { loseResponse = null; throw new Error('fixture response lost after commit'); }
  }
  return structuredClone({ entries, revision });
}
function createWindow() {
  const context = vm.createContext({ console, structuredClone, Map, Set, Promise, crypto: { randomUUID }, clearTimeout, setTimeout, api, initial: () => ({ 'dengshell.history.profile': ['BEFORE'] }), patch: value => patches.push(value), notice: value => notices.push(value) });
  vm.runInContext(`
    const window={CLOUDSHELL:{uiPreferences:{}},addEventListener(){},focus(){},DengWindowTransfers:{changed(){}},DengWindowPreview:{arrive(){}}};
    const localStorage={length:0}, appearanceSave=Promise.resolve(), persistAppearance=async value=>patch(value), toast=notice, native=()=>null;
    ${read('portable-preferences.js')}
    const readSaved=(key,fallback)=>window.DengPortablePreferences.read(key,fallback);
    const sessions=new Map();let nextSessionOrder=0,activeID=null,historySession=null;const $=()=>({open:false});
    const dropSessionView=id=>sessions.delete(id),createTerminal=state=>{state.ready=true;state.connected=true},activate=id=>{activeID=id},setDrawer=()=>{},navigate=async()=>{},pollStats=()=>{},pollLatency=()=>{};
    ${history}\n${makeState}\n${restore}
    window.DengPortablePreferences.accept({appearance:{layout:initial()}});
    globalThis.fixture={makeSessionState,recordCommand,receive,sessions,clear:()=>enqueueCommandHistory('profile','',true),flush:()=>window.DengPortablePreferences.flush()};
  `, context);
  return context.fixture;
}
const main = createWindow(), child = createWindow();
const session = { id: 'same-ssh-session', profileId: 'profile', home: '/' };
const childState = child.makeSessionState(session); child.sessions.set(session.id, childState);
child.recordCommand(childState, 'CHILD_NEW'); await child.flush();
handoff = { session, clientState: {}, cwd: '/', follow: false };
const restored = await main.receive('fixture-handoff', 'fixture-view');
assert.deepEqual(Array.from(restored.history), ['BEFORE', 'CHILD_NEW']);
main.recordCommand(restored, 'AFTER_MERGE'); await main.flush();
assert.deepEqual(entries, ['BEFORE', 'CHILD_NEW', 'AFTER_MERGE']);
for (let i = 0; i < 30; i++) { main.recordCommand(restored, 'MAIN_' + i); child.recordCommand(childState, 'CHILD_' + i); }
await Promise.all([main.flush(), child.flush()]);
for (let i = 0; i < 30; i++) { assert(entries.includes('MAIN_' + i)); assert(entries.includes('CHILD_' + i)); }
assert.equal(entries.length, 63);
loseResponse = 'RETRY'; main.recordCommand(restored, 'RETRY');
await new Promise(resolve => setImmediate(resolve));
child.recordCommand(childState, 'OTHER_WINDOW'); await child.flush();
await main.flush();
assert.equal(entries.filter(command => command === 'RETRY').length, 1);
assert(entries.includes('OTHER_WINDOW')); assert(notices.some(value => value.includes('fixture response lost')));
// A lost clear acknowledgement may be retried after another window appends.
loseResponse = '';
await assert.rejects(main.clear(), /fixture response lost/);
child.recordCommand(childState, 'AFTER_CLEAR'); await child.flush(); await main.flush();
assert.deepEqual(entries, ['AFTER_CLEAR']);
// Delete/purge the configuration while an existing window still has its state.
// Exercise the real recordCommand -> queue -> portable flush -> clear flow.
profiles.delete('profile'); knownHistory = false; entries = [];
main.recordCommand(restored, 'LIVE_AFTER_PROFILE_DELETE'); await main.flush();
assert.deepEqual(entries, ['LIVE_AFTER_PROFILE_DELETE']);
liveSession = false;
child.recordCommand(childState, 'PENDING_AFTER_SESSION_CLOSE'); await child.flush();
assert.deepEqual(entries, ['LIVE_AFTER_PROFILE_DELETE', 'PENDING_AFTER_SESSION_CLOSE']);
await main.clear(); await main.flush(); assert.deepEqual(entries, []);
assert(patches.every(patch => !Object.keys(patch.layout || {}).some(key => key.startsWith('dengshell.history.'))));
console.log('Command history: real window receive restores current history; concurrent append, pending flush, lost-response retry, clear, and deleted-profile flush pass.');
