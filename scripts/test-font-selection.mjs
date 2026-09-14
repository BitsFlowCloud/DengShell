import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
const pause=()=>new Promise(r=>setImmediate(r));
const base='builtin:ui-ibm-plex-sans-sc',slow='custom-slow';
let release;const gate=new Promise(r=>release=r),saved=[];
const ctx=vm.createContext({window:{},managedAssets:[{id:slow,kind:'ui-font',name:'slow'}],
 staticJSON:async()=>({fonts:[{id:base,name:'base',family:base}]}),assetURL:async f=>f.id,
 FontFace:class{constructor(family){this.family=family}async load(){if(this.family.includes(slow))await gate;return this}},
 document:{fonts:{add(){},delete(){}}},api:async()=>({availableIds:[slow,base],activeId:base}),
 chooseAppearance:async p=>saved.push(p.uiFontId),URL,location:{href:'http://localhost/'}});
vm.runInContext(fs.readFileSync(new URL('../web/ui-appearance.js',import.meta.url),'utf8'),ctx);
await ctx.window.DengUIAppearance.acceptConfig();
const pending=ctx.window.DengUIAppearance.useFont(slow);await pause();
await ctx.window.DengUIAppearance.useFont(base);release();await pending;
assert.equal(saved.at(-1),base,'Earlier UI font preparation must not overwrite a later choice');

let finish;let desired=true;const fontGate=new Promise(r=>finish=r),selections=[];
const terminal=vm.createContext({window:{},managedAssets:[],appearance:{fontId:'builtin:jetbrains-mono'},
 allFonts:()=>[{id:'downloaded'}],loadFace:async()=>fontGate,alignedTerminalFontFamily:async()=>{},
 chooseAppearance:async p=>selections.push(p.fontId)});
const source=fs.readFileSync(new URL('../web/font-library.js',import.meta.url),'utf8');
// Expose an internal event handler only inside this isolated test context.
vm.runInContext(source.replace('return { open, selectionChanged, reflect };','return { open, selectionChanged, reflect, useFont };'),terminal);
const applying=terminal.window.DengFontLibrary.useFont({kind:'font'},'downloaded',()=>desired);await pause();desired=false;finish();await applying;
assert.equal(selections.length,0,'Download completion must recheck selection after FontFace loading');
console.log('PASS: UI last-choice-wins and downloaded font cancellation during actual preparation await.');
