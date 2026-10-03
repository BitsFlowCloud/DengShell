import { copyFile, mkdir, readFile, writeFile } from 'node:fs/promises';
await mkdir('web/vendor', {recursive:true});
for (const [source, target] of [
 ['@xterm/xterm/lib/xterm.js', 'xterm.js'], ['@xterm/xterm/css/xterm.css','xterm.css'], ['@xterm/xterm/LICENSE','xterm-LICENSE'],
 ['@xterm/addon-serialize/lib/addon-serialize.js','addon-serialize.js'],
 ['@xterm/addon-fit/lib/addon-fit.js','addon-fit.js'], ['@xterm/addon-fit/LICENSE','addon-fit-LICENSE'],
 ['markdown-it/dist/browser/markdown-it.umd.min.js','markdown-it.js'], ['markdown-it/LICENSE','markdown-it-LICENSE']
]) await copyFile('node_modules/'+source,'web/vendor/'+target);

// The Markdown browser bundle includes these libraries, so distribute their
// copyright notices alongside the parser's own license.
const notices = [];
for (const [name, license] of [
 ['entities', 'LICENSE'], ['linkify-it', 'LICENSE'], ['mdurl', 'LICENSE'],
 ['punycode.js', 'LICENSE-MIT.txt'], ['uc.micro', 'LICENSE.txt']
]) {
 const pkg = JSON.parse(await readFile('node_modules/'+name+'/package.json', 'utf8'));
 notices.push(name+' '+pkg.version+'\n\n'+await readFile('node_modules/'+name+'/'+license, 'utf8'));
}
await writeFile('web/vendor/markdown-it-dependencies-LICENSE', notices.join('\n\n---\n\n'));
