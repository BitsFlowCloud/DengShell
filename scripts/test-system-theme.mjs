import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
const source = readFileSync(new URL('../web/theme.js', import.meta.url), 'utf8');
const portable = readFileSync(new URL('../web/portable-preferences.js', import.meta.url), 'utf8');
function harness({ theme = 'system', dark = false, native } = {}) {
  const media = new EventTarget(); media.matches = dark;
  const window = new EventTarget(), document = new EventTarget(), storage = new Map(), timers = new Map(), saves = [];
  let timerID = 0;
  const hint = { textContent: '' }, control = new EventTarget();
  control.checked = false;
  const elements = new Map([['follow-system-theme', control], ['follow-system-theme-hint', hint]]);
  document.documentElement = { dataset: {}, style: { setProperty() {} }, classList: { remove() {}, add() {} } };
  document.hidden = false; document.body = {};
  document.getElementById = id => elements.get(id) || null;
  document.createElement = () => ({ querySelector: () => control });
  elements.set('settings-menu', { querySelector: () => ({ after() {} }) });
  const localStorage = { getItem: key => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, value), removeItem: key => storage.delete(key), get length() { return storage.size; }, key: i => [...storage.keys()][i] };
  window.CLOUDSHELL = { theme }; if (native) window.go = { main: { Desktop: { SystemTheme: native } } };
  const context = vm.createContext({ window, document, localStorage, Event, CustomEvent, structuredClone,
    matchMedia: query => query.includes('reduced') ? { matches: true } : media,
    setTimeout: (fn, delay) => { const id = ++timerID; timers.set(id, { fn, delay }); return id; },
    clearTimeout: id => timers.delete(id), console,
    persistAppearance: async value => { saves.push(value); }, appearanceSave: Promise.resolve(), toast: message => { throw new Error(message); },
  });
  vm.runInContext(source, context);
  vm.runInContext(portable, context); window.DengPortablePreferences.accept({ appearance: {} });
  return { window, document, media, storage, timers, saves, control, hint,
    start: () => document.dispatchEvent(new Event('DOMContentLoaded')),
    osDark(value) { media.matches = value; media.dispatchEvent(new Event('change')); },
    async settle() { for (let i = 0; i < 8; i++) await Promise.resolve(); },
  };
}
const h = harness();
assert.equal(h.window.CloudShellTheme.current, 'light');
assert.equal(h.window.CloudShellTheme.preference, 'system');
h.start(); assert.equal(h.control.checked, true);
h.osDark(true); assert.equal(h.window.CloudShellTheme.current, 'dark');
assert.equal(h.saves.length, 0, 'OS changes never rewrite portable settings');
h.window.CloudShellTheme.toggle();
assert.equal(h.window.CloudShellTheme.preference, 'light', 'manual toolbar switching leaves system mode');
assert.equal(h.control.checked, false); assert.equal(h.saves.at(-1).theme, 'light');
h.osDark(false); h.osDark(true);
assert.equal(h.window.CloudShellTheme.current, 'light', 'manual preference is stable across OS changes');
h.window.CloudShellTheme.set('system');
assert.equal(h.window.CloudShellTheme.current, 'dark'); assert.equal(h.saves.at(-1).theme, 'system');
h.window.CloudShellTheme.set('dark');
assert.equal(h.window.CloudShellTheme.current, 'dark'); assert.equal(h.saves.at(-1).theme, 'dark', 'same visible color still persists preference change');
assert.equal(h.control.checked, false);
h.window.CloudShellTheme.set('invalid'); assert.equal(h.window.CloudShellTheme.preference, 'dark');
assert.equal(harness({theme:'dark',dark:false}).window.CloudShellTheme.current, 'dark', 'saved manual setting wins at startup');
let os = 'dark', calls = 0;
const n = harness({ native: async () => { calls++; return os; } });
n.start(); await n.settle();
assert.equal(n.window.CloudShellTheme.current, 'dark', 'native OS preference overrides a forced-light WebView');
n.osDark(false); await n.settle(); assert.equal(n.window.CloudShellTheme.current, 'dark', 'WebView feedback cannot change native OS preference');
os = 'light'; await n.window.CloudShellTheme.refreshSystem();
assert.equal(n.window.CloudShellTheme.current, 'light', 'native system changes are applied dynamically');
assert.equal(n.saves.length, 0, 'native notifications do not save resolved light/dark over system mode');
n.document.hidden = true; const before = calls; await n.window.CloudShellTheme.refreshSystem();
assert.equal(calls, before, 'hidden windows avoid periodic OS reads');
os = 'dark'; n.document.hidden = false; n.window.dispatchEvent(new Event('focus')); await n.settle();
assert.equal(n.window.CloudShellTheme.current, 'dark', 'restoring focus refreshes current OS preference');
n.window.CloudShellTheme.set('light'); const after = calls; await n.window.CloudShellTheme.refreshSystem(); assert.equal(calls, after, 'manual mode stops OS polling');
let finish;
const race = harness({ native: () => new Promise(resolve => { finish = resolve; }) });
race.start(); race.window.CloudShellTheme.set('light'); finish('dark'); await race.settle();
assert.equal(race.window.CloudShellTheme.current, 'light', 'an in-flight native response cannot override a manual choice');
const absent = harness({ dark: true, native: async () => '' }); absent.start(); await absent.settle(); assert.equal(absent.window.CloudShellTheme.current, 'dark', 'unsupported native desktop uses matchMedia fallback');
console.log('System theme: startup, persistence, manual override, live native updates, feedback isolation, visibility, and in-flight race checks passed.');
