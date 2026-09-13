import fs from 'node:fs';
import vm from 'node:vm';
import assert from 'node:assert/strict';

// Exercise the shipped editor functions with controllable network responses
// and the real lossless text model, without a server or user configuration.
const source = fs.readFileSync(new URL('../web/file-tools.js', import.meta.url), 'utf8');
const pending = [];
const editor = { _generation: 1, open: true };
const context = {
  window: {}, editor,
  area: { disabled: false, value: '', setSelectionRange() {} },
  encodingSelect: { value: 'auto', disabled: false },
  saveButton: { disabled: true }, status: {},
  documentState: null, model: null,
  api: () => new Promise((resolve, reject) => pending.push({ resolve, reject })),
  post: () => { throw new Error('Save must remain disabled while decoding'); },
  toast() {}, refresh() {},
};
vm.createContext(context);
vm.runInContext(fs.readFileSync(new URL('../web/text-model.js', import.meta.url), 'utf8'), context);
context.DengTextModel = context.window.DengTextModel;
const loadFunctions = source.slice(source.indexOf(' function updateStatus('), source.indexOf('\n function makeEditor('));
const saveFunction = source.slice(source.indexOf(' async function saveText('), source.indexOf('\n async function openText('));
vm.runInContext(`${loadFunctions}\n${saveFunction}\nthis.load=loadText;this.save=saveText;`, context);
const state = { id: 'isolated-editor-fixture' };
const document = (text, encoding = 'utf-8') => ({ path: '/fixture.txt', text, encoding, sha256: text, bytes: text.length });

const earlier = context.load(state, '/fixture.txt', 'gbk');
const later = context.load(state, '/fixture.txt', 'utf-8');
assert.equal(context.area.disabled, true);
assert.equal(context.encodingSelect.disabled, true);
pending[1].resolve(document('current response'));
await later;
context.model.edit('unsaved user edit');
const model = context.model;
const undoLength = model.undoStack.length;
pending[0].resolve(document('stale response', 'gbk'));
await earlier;
assert.equal(context.model, model, 'stale response must not replace the text model');
assert.equal(context.model.raw, 'unsaved user edit');
assert.equal(context.model.undoStack.length, undoLength, 'undo history survives a delayed response');
assert.equal(context.encodingSelect.value, 'utf-8');
assert.equal(context.area.disabled, false);

const failed = context.load(state, '/fixture.txt', 'gbk');
await context.save(); // Ctrl+S is subject to the same loading guard as the button.
pending[2].reject(new Error('chosen encoding cannot decode this file'));
await assert.rejects(failed, /cannot decode/);
assert.equal(context.model, model, 'failed decoding preserves existing edits');
assert.equal(context.area.disabled, false, 'failed loads restore editing controls');
assert.equal(context.encodingSelect.disabled, false);
assert.equal(context.saveButton.disabled, false);
assert.equal(context.encodingSelect.value, 'utf-8');

const staleFailure = context.load(state, '/fixture.txt', 'gbk');
const winning = context.load(state, '/fixture.txt', 'utf-8');
pending[4].resolve(document('latest document'));
await winning;
context.model.edit('second unsaved edit');
pending[3].reject(new Error('stale failure'));
await staleFailure;
assert.equal(context.model.raw, 'second unsaved edit');
assert.equal(context.area.disabled, false);

const closed = context.load(state, '/fixture.txt');
editor.open = false;
editor._generation++;
pending[5].resolve(document('response for closed editor'));
await closed;
assert.equal(context.model.raw, 'second unsaved edit');
console.log('PASS: stale responses/failures preserve edits and undo; failed loads restore controls; loading blocks save; closing invalidates pending loads.');
