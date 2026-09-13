import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import vm from 'node:vm';

const source=readFileSync(new URL('../web/app.js',import.meta.url),'utf8');
const start=source.indexOf('let statsTimer = '), end=source.indexOf('\nfunction meter(',start);
assert(start>=0&&end>start,'monitor polling source is available');
const a={id:'slow',connected:true}, b={id:'fast',connected:true};
const pending=[], rendered=[], timers=new Map();let nextTimer=0;
const panel={open:false,classList:{remove(){},add(){}}};
const ctx={window:{},document:{hidden:false},sessions:new Map([[a.id,a],[b.id,b]]),activeID:a.id,
 current(){return ctx.sessions.get(ctx.activeID)},$:()=>panel,
 api(path){return new Promise((resolve,reject)=>pending.push({path,resolve,reject}))},
 rememberProcessSample(){},renderMonitor(stats){rendered.push(stats.marker)},
 setTimeout(fn,delay){const id=++nextTimer;timers.set(id,{fn,delay});return id},clearTimeout(id){timers.delete(id)}};
vm.createContext(ctx);vm.runInContext(source.slice(start,end)+'\npollNetwork=()=>{};',ctx);
const slow=ctx.pollStats();
assert.equal(pending.length,1);
ctx.activeID=b.id;
const fast=ctx.pollStats();
assert.equal(pending.length,2,'a stalled server does not block the newly selected server');
await ctx.pollStats();assert.equal(pending.length,2,'same-session polling remains bounded to one request');
pending[1].resolve({marker:'fast',nextSampleInMilliseconds:5000});await fast;
assert.deepEqual(rendered,['fast']);assert.equal(timers.size,1);
const [fastTimer]=timers.keys();
pending[0].resolve({marker:'slow',nextSampleInMilliseconds:0});await slow;
assert.deepEqual(rendered,['fast'],'late background response cannot repaint the selected server');
assert.deepEqual([...timers.keys()],[fastTimer],'late background response cannot replace its timer');
ctx.activeID=a.id;const retry=ctx.pollStats();
ctx.activeID=b.id;pending.at(-1).reject(new Error('stalled peer'));await retry;
assert.deepEqual(rendered,['fast']);
console.log('PASS: core monitoring remains independent across tabs, deduplicates per session, and ignores stale completions.');
