import fs from 'node:fs';
import vm from 'node:vm';
import assert from 'node:assert/strict';
const source = fs.readFileSync(new URL('../web/ai-window-titlebar.js', import.meta.url), 'utf8');
const settle = async () => { for (let i = 0; i < 16; i += 1) await Promise.resolve(); };
class Element {
  constructor(tag) {this.tagName = tag.toUpperCase(); this.children = []; this.attributes = new Map(); this.hidden = false; this.disabled = false; this.parentElement = null;}
  append(...elements) {for (const e of elements) {e.parentElement = this; this.children.push(e);}}
  prepend(element) {element.parentElement = this; this.children.unshift(element);}
  after(element) {element.parentElement = this.parentElement; this.parentElement.children.splice(this.parentElement.children.indexOf(this) + 1, 0, element);}
  replaceChildren(...elements) {this.children = []; this.append(...elements);}
  setAttribute(name, value) {this.attributes.set(name, String(value));}
  getAttribute(name) {return this.attributes.get(name) ?? null;}
  closest(selector) {for (let e = this; e; e = e.parentElement) if (selector === 'button' && e.tagName === 'BUTTON') return e; return null;}
}
function fixture({native = true, bridge = true, frameless = true} = {}) {
  const elements = [], listeners = new Map(), timers = [], events = [], actions = [], pendingPins = [];
  const document = {body: new Element('body'), createElement(tag) {const e = new Element(tag); elements.push(e); return e;}, createElementNS(_, tag) {return this.createElement(tag);}};
  let maximised = false, closed = 0;
  const desktop = {WindowState: async () => ({frameless, maximised}), WindowAction: async action => {actions.push(action); if (action === 'toggle-maximise') maximised = !maximised; return {frameless, maximised};}, SetAIWindowAlwaysOnTop() {}};
  const window = {go: bridge ? {main: {Desktop: desktop}} : undefined, DengAIWindowTransport: {pin(value) {return new Promise((resolve, reject) => pendingPins.push({value, resolve, reject}));}, close() {closed += 1;}}, addEventListener(name, fn) {listeners.set(name, fn);}, dispatchEvent(event) {events.push(event.type); listeners.get(event.type)?.(event);}};
  const context = vm.createContext({window, document, location: {protocol: native ? 'wails:' : 'http:', hostname: native ? 'wails.localhost' : '127.0.0.1'}, setTimeout(fn) {timers.push(fn);}, Date, CustomEvent: class {constructor(type) {this.type = type;}}, getComputedStyle(e) {return {getPropertyValue: () => e.closest('button') ? 'no-drag' : 'drag'};}});
  vm.runInContext(source, context);
  return {window, document, elements, listeners, timers, events, actions, pendingPins, desktop, get(id) {return elements.find(e => e.id === id);}, get closed() {return closed;}, maximised(value) {maximised = value;}};
}
{
  const f = fixture({native: false}); await settle();
  assert.equal(f.elements.length, 0); assert.equal(f.window.DengAIWindowTitlebar.ready, false);
}
{
  const f = fixture(); await settle();
  const header = f.get('ai-window-titlebar'), pin = f.get('ai-pin'), min = f.get('ai-window-minimise'), max = f.get('ai-window-maximise'), close = f.get('ai-window-close');
  assert.equal(header.hidden, false); assert.equal(f.window.DengAIWindowTitlebar.ready, true);
  assert.deepEqual(pin.parentElement.children.map(e => e.id), ['ai-pin', 'ai-window-minimise', 'ai-window-maximise', 'ai-window-close']);
  assert.equal(pin.children[0].tagName, 'SVG'); assert.equal(pin.getAttribute('aria-pressed'), 'false'); assert.equal(pin.title, '置顶窗口');
  assert.ok(f.events.includes('dengshell:ai-window-controls'));
  const first = pin.onclick(); const repeated = pin.onclick(); await repeated;
  assert.equal(f.pendingPins.length, 1); assert.equal(f.pendingPins[0].value, true); assert.equal(pin.disabled, true);
  f.pendingPins[0].resolve(true); await first;
  assert.equal(pin.getAttribute('aria-pressed'), 'true'); assert.equal(pin.getAttribute('aria-label'), '取消置顶'); assert.equal(pin.disabled, false);
  const failed = pin.onclick(); f.pendingPins[1].reject(new Error('native failed')); await failed;
  assert.equal(pin.getAttribute('aria-pressed'), 'true'); assert.equal(pin.disabled, false);
  const third = pin.onclick(); assert.equal(f.pendingPins[2].value, false); f.pendingPins[2].resolve(false); await third;
  assert.equal(pin.getAttribute('aria-pressed'), 'false'); assert.equal(pin.getAttribute('aria-label'), '置顶窗口');
  await min.onclick(); assert.equal(f.actions.at(-1), 'minimise');
  await max.onclick(); assert.equal(max.title, '还原窗口'); await max.onclick(); assert.equal(max.title, '最大化');
  f.maximised(true); await f.listeners.get('resize')(); assert.equal(max.title, '还原窗口');
  const count = f.actions.length; header.ondblclick({target: pin.children[0]}); await settle(); assert.equal(f.actions.length, count);
  header.ondblclick({target: header.children[0]}); await settle(); assert.equal(f.actions.at(-1), 'toggle-maximise');
  close.onclick(); assert.equal(f.closed, 1);
}
{
  const f = fixture({bridge: false}); await settle(); assert.equal(f.get('ai-window-titlebar').hidden, true);
  f.window.go = {main: {Desktop: f.desktop}}; f.timers.shift()(); await settle();
  assert.equal(f.window.DengAIWindowTitlebar.ready, true); assert.equal(f.get('ai-window-titlebar').hidden, false);
}
{
  const f = fixture({frameless: false}); await settle();
  assert.equal(f.window.DengAIWindowTitlebar.ready, false); assert.equal(f.get('ai-window-titlebar').hidden, true);
}
console.log('PASS: browser has no native controls; icon pin/minimize adjacency; async native readiness; truthful pin success/error/single-flight state; maximize/restore and resize labels; non-button drag double-click; native close.');
