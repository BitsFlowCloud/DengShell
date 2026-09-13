import { copyFile, mkdir } from 'node:fs/promises';
await mkdir('web/vendor', {recursive:true});
for (const [source, target] of [
 ['@xterm/xterm/lib/xterm.js', 'xterm.js'], ['@xterm/xterm/css/xterm.css','xterm.css'], ['@xterm/xterm/LICENSE','xterm-LICENSE'],
 ['@xterm/addon-serialize/lib/addon-serialize.js','addon-serialize.js'],
 ['@xterm/addon-fit/lib/addon-fit.js','addon-fit.js'], ['@xterm/addon-fit/LICENSE','addon-fit-LICENSE']
]) await copyFile('node_modules/'+source,'web/vendor/'+target);
