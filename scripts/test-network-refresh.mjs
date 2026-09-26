import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

const source = fs.readFileSync(process.env.DENG_NETWORK_POLL_SOURCE || new URL('../web/app.js', import.meta.url), 'utf8');
const start = source.indexOf('let statsTimer = 0, networkTimer = 0, networkPolledSession =');
const end = source.indexOf('async function pollStats()', start);
assert(start >= 0 && end > start);
const polling = source.slice(start, end);
function fixture() {
  const state = {id:'one', connected:true, terminalOutputStarted:true, stats:{}};
  const q = {time:2005, state, timers:[], rendered:[], remembered:[], calls:0};
  const context = {
    window:{}, document:{hidden:false}, sessions:new Map([[state.id,state]]), current:()=>q.state,
    setTimeout(fn,delay) { q.timers.push({fn,delay,at:q.time+delay}); return q.timers.length; }, clearTimeout() {},
    rememberNetworkSample(s,v) { q.remembered.push({id:s.id,...v}); },
    renderNetwork() { q.rendered.push({at:q.time,id:q.state.id,rx:q.state.networkStats?.interfaces?.[0]?.rx}); },
    api(path) { q.calls++; return q.read(path); },
  };
  vm.createContext(context);vm.runInContext(polling,context);q.context=context;
  q.poll=()=>context.pollNetwork();
  return q;
}
const response = (progress,rx=1,sampled=1300) => ({
  sampledAt:new Date(sampled).toISOString(), sampleInProgress:progress, nextSampleInMilliseconds:995,
  elapsedMilliseconds:1000, interfaces:[{name:'ens7',ready:true,rx:rx*1024**2,tx:0}],
});

// Reproduce the audited race: a 300 ms remote result lands after the UI's tick.
// Execute the actual polling function with a deterministic local clock.
const q=fixture();q.read=()=>response(q.time<2300,q.time<2300?1:5,q.time<2300?1300:2300);
await q.poll();
assert.equal(q.timers.at(-1).delay,100,'pending collection waited for another full sample tick');
while(q.rendered.at(-1).rx !== 5*1024**2 && q.time<4000) {
  const timer=q.timers.at(-1);q.time=timer.at;await timer.fn();
}
assert.equal(q.rendered.at(-1).rx,5*1024**2);
assert(q.time-2300<=100,'new backend data was not displayed on the next local retry');
assert.equal(q.timers.at(-1).delay,1000,'completed sample did not restore normal cadence');
console.log(`PASS: new rate shown ${q.time-2300} ms after completion (previous behavior waited until ~3005 ms).`);

for(const name of ['switch','close','hidden','locked']) {
  const x=fixture();let complete;x.read=()=>new Promise(resolve=>{complete=resolve});
  const pending=x.poll();await x.poll();assert.equal(x.calls,1,'overlapping cache request');
  if(name==='switch') { x.state={id:'two',connected:true,terminalOutputStarted:true};x.context.sessions.set('two',x.state); }
  if(name==='close') { x.state.connected=false;x.context.sessions.delete(x.state.id); }
  if(name==='hidden') x.context.document.hidden=true;
  if(name==='locked') x.context.window.DengSecurityLock={isLocked:()=>true};
  complete(response(true));await pending;
  assert.equal(x.timers.length,0,`${name} kept polling old/hidden session`);
  assert.equal(x.rendered.length,0,`${name} rendered a stale request over another view`);
  if(name==='switch') { x.read=()=>response(false,9);await x.poll();assert.equal(x.rendered.at(-1).id,'two'); }
}
const slow=fixture();slow.read=()=>response(true);
for(let n=0;n<100;n++) { await slow.poll();assert.equal(slow.timers.at(-1).delay,100);slow.time+=100; }
slow.read=()=>({...response(false,0),sampleError:'timeout',interfaces:[]});await slow.poll();
assert.equal(slow.state.networkError,'timeout');assert.equal(slow.timers.at(-1).delay,1000);
slow.read=()=>Promise.reject(new Error('local request failed'));await slow.poll();
assert.equal(slow.timers.at(-1).delay,1000);assert.equal(slow.state.networkError,'local request failed');
const legacy=fixture();legacy.read=()=>{const r=response(false);delete r.sampleInProgress;return r};await legacy.poll();
assert.equal(legacy.timers.at(-1).delay,1000);
console.log('PASS: tab switching, close/hide/lock, request deduplication, prolonged sampling, error backoff and older response compatibility.');
