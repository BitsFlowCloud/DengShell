import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const values = new Map(), loaded = [], gates = new Map(), errors = [];
const base = 'builtin:ui-ibm-plex-sans-sc', slow = 'builtin:ui-sarasa', fast = 'builtin:ui-wenkai';
let runtime = { availableIds: [], activeId: base };
const config = { fonts: [base, slow, fast].map(id => ({ id, name: id, family: id, file: id })) };
const root = { dataset: { theme: 'light' }, toggleAttribute() {}, style: { setProperty: (k, v) => values.set(k, v), removeProperty: k => values.delete(k) } };
const ctx = vm.createContext({
  window: {}, document: { documentElement: root, fonts: { add() {}, delete() {} } },
  FontFace: class { constructor(family) { this.family = family; } async load() { loaded.push(this.family); await gates.get(this.family); return this; } },
  managedAssets: [{ id: 'a'.repeat(48), kind: 'ui-font', name: '用户字体' }],
  api: async () => structuredClone(runtime), staticJSON: async () => config, assetURL: async f => f.file || f.id,
  toast: error => errors.push(error), structuredClone, URL, location: { href: 'http://localhost/' },
});
vm.runInContext(readFileSync(new URL('../web/ui-appearance.js', import.meta.url), 'utf8'), ctx);
const ui = ctx.window.DengUIAppearance;
await ui.acceptConfig();
await ui.apply({ uiFontId: base, uiTextColors: { light: '#123456', dark: '#FEDCBA' } });
assert.equal(root.dataset.uiFont, base);
assert.equal(values.get('--text'), '#123456');
root.dataset.theme = 'dark';
await ui.apply({ uiFontId: base, uiTextColors: { light: '#123456', dark: '#FEDCBA' } });
assert.equal(values.get('--text'), '#fedcba');

// Import and WebView refresh cannot make a newly registered font load.
runtime.pendingId = ctx.managedAssets[0].id;
await ui.acceptConfig();
await ui.apply({ uiFontId: runtime.pendingId, uiTextColors: {} });
assert.equal(root.dataset.uiFont, base);
assert.equal(loaded.some(name => name.includes('custom')), false);
assert.equal(values.has('--text'), false);
await ui.acceptConfig();
await ui.apply({ uiFontId: runtime.pendingId });
assert.equal(loaded.some(name => name.includes('custom')), false);

// Only the restarted backend adds the custom ID to the available set.
runtime = { availableIds: [ctx.managedAssets[0].id], activeId: ctx.managedAssets[0].id };
await ui.acceptConfig();
await ui.apply({ uiFontId: runtime.activeId });
assert.equal(root.dataset.uiFont, runtime.activeId);
assert.equal(loaded.filter(name => name.includes('custom')).length, 1);

// An earlier slow font cannot win over the user's later choice.
let release; gates.set(slow, new Promise(resolve => { release = resolve; }));
const first = ui.apply({ uiFontId: slow });
await new Promise(resolve => setImmediate(resolve));
await ui.apply({ uiFontId: fast });
release(); await first;
assert.equal(root.dataset.uiFont, fast);

// A color reset only patches that theme, preserving another window's dark edit.
const source = readFileSync(new URL('../web/appearance.js', import.meta.url), 'utf8');
const maps = vm.createContext({ structuredClone, appearanceMapFields: new Set(['uiTextColors']) });
vm.runInContext(source.slice(source.indexOf('function appearanceDifference('), source.indexOf('function persistAppearance(')), maps);
const before = { uiTextColors: { light: '#123456', dark: '#fedcba' } };
const diff = maps.appearanceDifference({ uiTextColors: { dark: '#fedcba' } }, before);
assert.deepEqual(Object.keys(diff.uiTextColors), ['light']);
assert.equal(diff.uiTextColors.light, null);
const merged = maps.mergeAppearanceChanges({ uiTextColors: { light: '#123456', dark: '#ffffff' } }, diff);
assert.equal(merged.uiTextColors.dark, '#ffffff');
assert.equal('light' in merged.uiTextColors, false);
assert.deepEqual(errors, []);
console.log('PASS: UI font restart boundary, asynchronous font switching, per-theme text colors and isolated reset.');
