import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import vm from 'node:vm';
const source=readFileSync(new URL('../web/workspace-refinements.js',import.meta.url),'utf8');
const history=source.slice(source.indexOf('function historyKey('),source.indexOf('async function copyText('));
const cache=new Map(), stored=new Map(), operations=new Set();
let all=[],revision=0,loseClear=false,rendered=0;
const context=vm.createContext({crypto:{randomUUID},sessions:new Map(),historySession:null,Promise,Map,
  window:{DengPortablePreferences:{cache:(key,value)=>cache.set(key,value)}},
  readSaved:(key,fallback)=>cache.get(key)||fallback,$:()=>({open:true}),renderCommandHistory:()=>rendered++,toast(){},
  api:async(path,options={})=>{
    const global=()=>({entries:[...all],revision});
    if(path==='/api/history'){
      if(options.method==='DELETE'){
        const op=JSON.parse(options.body).operation;
        if(!operations.has(op)){operations.add(op);all=[];revision++;}
        if(loseClear){loseClear=false;throw new Error('lost clear acknowledgement');}
      }
      return global();
    }
    const id=path.split('/')[3];let per=stored.get(id)||{entries:[],revision:0};
    if(options.method){const op=JSON.parse(options.body);if(!operations.has(op.operation)){operations.add(op.operation);per={entries:[...per.entries,op.command],revision:per.revision+1};stored.set(id,per);all.push(op.command);revision++;}}
    return {...per,global:global()};
  },
});
vm.runInContext(history+'\nthis.snapshot=()=>structuredClone(globalCommandHistory);',context);
// structuredClone is intentionally supplied only for the assertion snapshot.
context.structuredClone=structuredClone;
const first={profileId:'first'},second={profileId:'second'};
context.recordCommand(first,'echo FIRST');context.recordCommand(second,'echo SECOND');
await context.window.DengCommandHistory.flush();
assert.deepEqual(context.snapshot().entries,['echo FIRST','echo SECOND']);
assert.equal(rendered>0,true);
context.acceptGlobalCommandHistory({entries:['stale'],revision:0});
assert.deepEqual(context.snapshot().entries,['echo FIRST','echo SECOND']);
loseClear=true;await assert.rejects(context.clearGlobalCommandHistory(),/lost clear/);
context.recordCommand(second,'echo AFTER_CLEAR');await context.window.DengCommandHistory.flush();
await context.clearGlobalCommandHistory();
assert.deepEqual(context.snapshot().entries,['echo AFTER_CLEAR']);
assert.deepEqual(Array.from(cache.get('dengshell.history.first')),['echo FIRST'],'global display does not replace individual terminal navigation');
console.log('PASS: all VPS command events appear in one list, stale snapshots are rejected, and retrying a clear preserves newer commands.');
