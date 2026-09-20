import fs from 'node:fs';
import vm from 'node:vm';
import assert from 'node:assert/strict';

const input = [], pasted = [];
const context = vm.createContext({ sendInput: (state, text) => input.push(text) });
vm.runInContext(fs.readFileSync(new URL('../web/terminal-integration.js', import.meta.url), 'utf8'), context);
const state = {
  connected: true, ready: true,
  shellIntegration: { ready: true, atPrompt: true },
  term: {
    buffer: { active: { type: 'normal' } }, modes: { bracketedPasteMode: true }, options: {},
    clearSelection() {}, focus() {},
    paste(text) { pasted.push(text); state.shellIntegration.atPrompt = false; },
  },
};
function paste(text, options) {
  input.length = pasted.length = 0; state.shellIntegration.atPrompt = true;
  const result = options ? context.pasteTerminalText(state, text, options) : context.pasteTerminalClipboard(state, text);
  return { result, text: [...pasted], input: [...input] };
}
const script = 'cat > /tmp/dengshell-paste-fixture <<\'EOF\'\n  exact heredoc contents\nEOF\n\ncat /tmp/dengshell-paste-fixture';
for (const text of [script, script + '\n', script.replaceAll('\n', '\r\n'), script.replaceAll('\n', '\r')]) {
  assert.deepEqual(paste(text), { result: true, text: [text], input: ['\r'] });
}
for (const text of ['pwd', 'pwd\n', '\n \r\n']) assert.equal(paste(text).input.length, 0);
assert.equal(paste(script, { execute: false }).input.length, 0, 'Composer insertion must remain non-executing');
state.term.modes.bracketedPasteMode = false;
assert.equal(paste(script).input.length, 1);
for (const ending of ['\n', '\r', '\r\n']) assert.equal(paste(script + ending).input.length, 0, 'Plain trailing newline must not submit twice');
state.term.modes.bracketedPasteMode = true;
state.term.options.ignoreBracketedPasteMode = true;
assert.equal(paste(script + '\n').input.length, 0);
state.term.options.ignoreBracketedPasteMode = false;
state.term.buffer.active.type = 'alternate';
assert.equal(paste(script).input.length, 0, 'A terminal editor must not receive an extra Enter');
state.term.buffer.active.type = 'normal';
input.length = 0; state.shellIntegration.atPrompt = false;
context.pasteTerminalClipboard(state, script);
assert.equal(input.length, 0, 'A running command must not receive an extra Enter');
state.shellIntegration.ready = false;
assert.equal(paste(script).input.length, 1, 'Shells without integration retain normal-screen support');
for (const key of ['closed', 'detaching', 'restoring', 'ownershipUncertain']) {
  state[key] = true;
  assert.deepEqual(paste(script), { result: false, text: [], input: [] });
  state[key] = false;
}
state.connected = false;
assert.equal(paste(script).result, false);
console.log('PASS: multiline/CRLF/heredoc integrity; one submission; explicit no-execute; editor/busy guards; unavailable-session rejection.');
