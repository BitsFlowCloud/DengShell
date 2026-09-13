import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import assert from 'node:assert/strict';

// Exercise the actual terminal protocol consumer with hostile framing and
// directory names; no browser, server, or user configuration is involved.
const source = readFileSync(new URL('../web/terminal-integration.js', import.meta.url), 'utf8');
let handler;
const commands = [], directories = [];
const context = vm.createContext({
  Uint8Array, TextDecoder, atob,
  recordCommand: (state, command) => commands.push([state.id, command]),
  navigate: async directory => directories.push(directory), toast: () => {},
});
vm.runInContext(source, context);
context.state = {id:'fish-fixture', ready:true, connected:true, cwd:'/home/fixture', follow:false,
  term:{parser:{registerOscHandler: (code, callback) => {assert.equal(code, 777);handler=callback;}}}};
vm.runInContext('bindShellIntegration(state)', context);
context.metadata = {shell:'fish', nonce:'a'.repeat(48)};
vm.runInContext('acceptShellIntegration(state, metadata)', context);
const nonce = context.metadata.nonce;
const emit = (kind, payload='', token=nonce) => handler(`DengShell;${kind};${token}${payload ? ';'+payload : ''}`);
const encode = value => Buffer.from(value).toString('base64');
emit('ready', 'fish');emit('prompt');
assert.equal(context.state.shellIntegration.ready, true);
assert.equal(context.state.shellIntegration.atPrompt, true);
emit('busy');assert.equal(context.state.shellIntegration.atPrompt, false);
emit('command', encode('echo ignored'), 'b'.repeat(48));
emit('command', '!invalid!');emit('command', encode('bad\0command'));
assert.equal(commands.length, 0);
const command = `printf '%s\n' '中文\n第二行'`;
emit('command', encode(command));assert.deepEqual(commands, [['fish-fixture',command]]);
const directory = '/home/fixture/中文 space%#?\nline';
emit('cwd', encode(directory));assert.equal(context.state.terminalDirectory, directory);
assert.equal(directories.length, 0);
context.state.follow=true;
emit('cwd', encode(directory));assert.deepEqual(directories,[directory]);
emit('cwd', encode('relative'));emit('cwd',encode('/wrong'),'b'.repeat(48));
assert.equal(directories.length,1);
assert.equal(commands.length,1);
console.log('Shell UI protocol: history, UTF-8/multiline cwd, opt-in follow, invalid framing and nonce isolation passed.');
