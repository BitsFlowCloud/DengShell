import fs from 'node:fs';
import assert from 'node:assert/strict';
const [stage,modulePath]=process.argv.slice(2);
const {default:puppeteer}=await import(modulePath);
const url=fs.readFileSync(stage+'/browser-backend.log','utf8').match(/http:\/\/127\.0\.0\.1:\d+\/#token=[a-zA-Z0-9]+/)[0];
const origin=new URL(url).origin,token=new URL(url).hash.split('=')[1];
await fetch(origin+'/api/appearance/patch',{method:'POST',headers:{'Content-Type':'application/json','X-CloudShell-Token':token},body:JSON.stringify({onboardingCompleted:true,startupAnimation:false,uiScale:1})});
const browser=await puppeteer.launch({executablePath:'/usr/bin/google-chrome',headless:true,args:['--no-sandbox']});
const errors=[],results=[];
try {
 const page=await browser.newPage();page.on('pageerror',e=>errors.push(e.message));
 await page.setRequestInterception(true);page.on('request',r=>{const u=new URL(r.url());if(['http:','https:'].includes(u.protocol)&&u.origin!==origin)r.abort();else r.continue()});
 await page.setViewport({width:1600,height:1000});await page.goto(url,{waitUntil:'networkidle0'});
 await page.evaluate(()=>{
  document.querySelectorAll('dialog[open]').forEach(d=>d.close());setDrawer(false);
  window.qa={rdp:[],probes:0,disposed:0,cycle:0,toasts:[]};
  const realAPI=api;api=(path,options)=>path==='/api/rdp'?Promise.resolve({available:false,sessions:structuredClone(qa.rdp)}):path.includes('/processes?')?Promise.resolve({processes:[],page:1,pages:1,total:0,matched:0}):realAPI(path,options);
  const realToast=toast;toast=m=>{qa.toasts.push(m);realToast(m)};
  DengWindowTransfers.atPointer=async()=>{qa.probes++;return null};DengWindowTransfers.changed=()=>{};
  window.qaSetTabs=count=>{
   DengSessionWindows.cancelDrag();for(const id of sessions.keys())DengProcessView.drop(id);sessions.clear();profiles=[];activeID=null;qa.cycle++;
   for(let i=0;i<count;i++){
    const id='tab-'+qa.cycle+'-'+i;profiles.push({id,name:`S${i+1}`,host:'example.invalid',port:22,user:'qa'});
    const state=makeSessionState({id,profileId:id,home:'/'});
    Object.assign(state,{tabOrder:nextSessionOrder++,connected:false,ready:false,localOnly:true,term:{options:{},buffer:{active:null},focus(){},refresh(){},dispose(){qa.disposed++}},host:Object.assign(document.createElement('div'),{hidden:true})});
    sessions.set(id,state);
   }
   activeID=sessions.keys().next().value;renderTabs();return [...sessions.keys()];
  };
 });
 const order=()=>page.$$eval('#session-tabs > .session-tab',tabs=>tabs.map(t=>t.dataset.processSessionId?'p:'+t.dataset.processSessionId:t.dataset.sessionId));
 const selector=id=>id.startsWith('p:')?`[data-process-session-id="${id.slice(2)}"]`:`[data-session-id="${id}"]`;
 async function rect(id){return page.$eval(selector(id),e=>{const r=e.getBoundingClientRect();return {x:r.x,y:r.y,w:r.width,h:r.height}})}
 async function begin(id){const r=await rect(id);await page.mouse.move(r.x+Math.min(30,r.w/3),r.y+r.h/2);await page.mouse.down();return r;}
 async function moveTo(id,after=false){const r=await rect(id);await page.mouse.move(r.x+(after?r.w*.8:r.w*.2),r.y+r.h/2,{steps:8});}
 async function drop(){await page.mouse.up();await new Promise(r=>setTimeout(r,120));}
 async function drag(from,to,after=false){await begin(from);await moveTo(to,after);await drop()}
 let ids=await page.evaluate(()=>qaSetTabs(13));await new Promise(r=>setTimeout(r,120));
 // Disconnected/pending SSH tabs can be reordered across row boundaries.
 await drag(ids[0],ids[8],true);let expected=[...ids.slice(1,9),ids[0],...ids.slice(9)];assert.deepEqual(await order(),expected);
 assert.equal(await page.evaluate(()=>activeID),ids[0]);assert.equal(await page.evaluate(()=>qa.disposed),0);assert.equal(await page.evaluate(()=>qa.probes),0);
 await drag(ids[12],ids[1]);expected=[ids[12],...expected.filter(id=>id!==ids[12])];assert.deepEqual(await order(),expected);
 await page.evaluate(()=>renderTabs());assert.deepEqual(await order(),expected);
 // Background updates request a render while dragging. Keep capture and finish sorting.
 await begin(ids[1]);await moveTo(ids[8],true);
 await page.evaluate(()=>{renderTabs();renderTabs();renderTabs()});assert(await page.$('.session-tab-dragging'));
 await drop();expected=expected.filter(id=>id!==ids[1]);expected.splice(expected.indexOf(ids[8])+1,0,ids[1]);assert.deepEqual(await order(),expected);
 // Escape/capture loss cancel; ordinary clicks still activate, close does not drag.
 const original=await order();await begin(ids[2]);await moveTo(ids[7]);await page.keyboard.press('Escape');await drop();assert.deepEqual(await order(),original);assert.equal(await page.$('.session-drag-preview'),null);
 await begin(ids[3]);await moveTo(ids[7]);await page.evaluate(id=>document.querySelector(`[data-session-id="${id}"]`).dispatchEvent(new PointerEvent('pointercancel',{pointerId:1,bubbles:true})),ids[3]);await drop();assert.deepEqual(await order(),original);
 await page.click(selector(ids[4])+' > button:first-child');assert.equal(await page.evaluate(()=>activeID),ids[4]);
 await page.click(selector(ids[5])+' .tab-close');expected=original.filter(id=>id!==ids[5]);assert.deepEqual(await order(),expected);assert.equal(await page.evaluate(()=>qa.disposed),1);
 // A pending connection changes its ID on success but remains in its chosen slot.
 const beforeID=ids[1],afterID='connected-session';await page.evaluate(({beforeID,afterID})=>{const state=sessions.get(beforeID);sessions.delete(beforeID);state.id=afterID;sessions.set(afterID,state);renderTabs()},{beforeID,afterID});expected=expected.map(id=>id===beforeID?afterID:id);assert.deepEqual(await order(),expected);
 // Reconnect may briefly retain two objects with the same slot. Their keys
 // must be unique, and the replacement must keep the user's sorted position.
 await page.evaluate(id=>{const previous=sessions.get(id),replacement={...previous,id:'reconnecting-slot'};sessions.set(replacement.id,replacement);renderTabs()},afterID);
 assert.equal(await page.$$eval('#session-tabs > .session-tab',ts=>new Set(ts.map(t=>t.dataset.tabKey)).size),expected.length+1);
 await page.evaluate(id=>{sessions.delete(id);renderTabs()},afterID);expected=expected.map(id=>id===afterID?'reconnecting-slot':id);assert.deepEqual(await order(),expected);
 // Process tab is independently movable and stays put after status renders.
 await page.evaluate(id=>{const s=sessions.get(id);s.connected=true;activate(id);DengProcessView.open();s.connected=false;},ids[4]);await new Promise(r=>setTimeout(r,80));
 const processID='p:'+ids[4];assert((await order()).includes(processID));await drag(processID,expected[0]);expected=[processID,...expected];assert.deepEqual(await order(),expected);await page.evaluate(()=>renderTabs());assert.deepEqual(await order(),expected);
 // RDP tabs join the same order, including an active RDP tab.
 await page.evaluate(async()=>{qa.rdp=[{id:'rdp-order-test',name:'RDP',status:'disconnected',message:'已断开'}];await DengRDP.refresh()});
 await drag('rdp-order-test',expected[0]);expected=['rdp-order-test',...expected];assert.deepEqual(await order(),expected);
 await page.click(selector('rdp-order-test')+' > button:first-child');assert.equal(await page.evaluate(()=>DengRDP.active()),'rdp-order-test');
 await drag('rdp-order-test',expected[3],true);expected=expected.filter(id=>id!=='rdp-order-test');expected.splice(3,0,'rdp-order-test');assert.deepEqual(await order(),expected);assert.equal(await page.evaluate(()=>DengRDP.active()),'rdp-order-test');
 // Only ready SSH tabs may detach; local sorting must not invoke native hit tests.
 await page.evaluate(id=>{const s=sessions.get(id);s.ready=true;s.connected=true;s.localOnly=false;qa.probes=0;renderTabs()},ids[4]);
 await drag(ids[4],expected[0]);expected=expected.filter(id=>id!==ids[4]);expected.unshift(ids[4]);assert.deepEqual(await order(),expected);assert.equal(await page.evaluate(()=>qa.probes),0);
 // A real drag outside still reaches the existing detach flow; this fixture
 // intentionally lacks a terminal serializer, so it stops before opening a window.
 await begin(ids[4]);const bounds=await page.$eval('#session-tabs',e=>{const r=e.getBoundingClientRect();return {x:r.left,y:r.bottom}});await page.mouse.move(bounds.x+80,bounds.y+110,{steps:8});await drop();
 await page.waitForFunction(()=>qa.toasts.includes('终端快照组件尚未准备好'));assert.deepEqual(await order(),expected);
 // Wrapped list at fractional UI scales; six per row at most remains intact.
 await page.evaluate(()=>DengRDP.deactivate());
 for(const [width,height,scale] of [[1600,1000,1],[1280,800,1.25],[1920,1080,1.5]]){
  await page.setViewport({width,height});await page.evaluate(scale=>{appearance.uiScale=scale;applyUIScale()},scale);
  ids=await page.evaluate(()=>qaSetTabs(9));await new Promise(r=>setTimeout(r,150));
  // Keep RDP out of view for this geometry matrix without modifying its module.
  await drag(ids[8],ids[0]);const actual=await order();assert(actual.indexOf(ids[8])<actual.indexOf(ids[0]));
  const rows=await page.$$eval('#session-tabs > .session-tab',ts=>{const counts={};for(const t of ts)counts[t.offsetTop]=(counts[t.offsetTop]||0)+1;return Object.values(counts)});assert(rows.every(c=>c<=6));
  results.push({width,height,scale,rows});
 }
 // Edge scrolling reaches rows beyond the visible titlebar area.
 await page.setViewport({width:1280,height:800});await page.evaluate(()=>{appearance.uiScale=1;applyUIScale();qaSetTabs(60);$('#session-tabs').scrollTop=0});await new Promise(r=>setTimeout(r,150));
 const first=(await order())[0];await begin(first);
 const edge=await page.$eval('#session-tabs',e=>{const r=e.getBoundingClientRect();return {x:r.left+60,y:r.bottom-2}});await page.mouse.move(edge.x,edge.y,{steps:8});await new Promise(r=>setTimeout(r,350));assert(await page.$eval('#session-tabs',e=>e.scrollTop>0));await page.keyboard.press('Escape');await drop();
 assert.equal(await page.$('.session-tab-insertion'),null);
 // Visual feedback in both palettes.
 for(const theme of ['dark','light']){
  await page.evaluate(theme=>{CloudShellTheme.set(theme);qaSetTabs(12);$('#session-tabs').scrollTop=0},theme);await new Promise(r=>setTimeout(r,950));
  const visible=await order();await begin(visible[0]);await moveTo(visible[5],true);await page.screenshot({path:stage+`/tab-drag-${theme}.png`});await page.keyboard.press('Escape');await drop();
 }
 assert.deepEqual(errors,[]);fs.writeFileSync(stage+'/tab-drag-results.json',JSON.stringify({passed:true,results,errors},null,2));
 console.log('PASS: real mouse SSH/process/RDP reorder, disconnected/pending tabs, ID transition, multirow + fractional zoom, deferred renders, Escape/cancel, active tab/connection preservation, close, edge scrolling and existing detach route');
}finally{await browser.close()}
