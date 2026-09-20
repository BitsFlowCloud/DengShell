import fs from 'node:fs';
import assert from 'node:assert/strict';
const [stage, chrome, modulePath] = process.argv.slice(2);
const { default: puppeteer } = await import(modulePath);
const fixture = JSON.parse(fs.readFileSync(stage + '/browser-fixture.json'));
assert.equal(new URL(fixture.url).hostname, '127.0.0.1');
const headers = { 'Content-Type':'application/json', 'X-CloudShell-Token':fixture.token };
const call = async (path, method='GET', body) => {
  const r = await fetch(new URL(path,fixture.url),{method,headers,body:body===undefined?undefined:JSON.stringify(body)});
  assert(r.ok, `${method} ${path}: ${r.status} ${await (!r.ok ? r.text() : '')}`); return r.json();
};
await call('/api/appearance/patch','POST',{onboardingCompleted:true,startupAnimation:false,uiScale:1});
const [a,b] = fixture.sessions;
for (const s of [a,b]) {
  await call(`/api/sessions/${s.id}/files?path=${encodeURIComponent(s.directory)}`);
  await call(`/api/sessions/${s.id}/file-content?path=${encodeURIComponent(s.file)}`);
}
const browser = await puppeteer.launch({executablePath:chrome,headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const errors=[],checks=[];let page;
try {
  page=await browser.newPage();page.on('pageerror',e=>errors.push(e.message));
  await page.emulateMediaFeatures([{name:'prefers-reduced-motion',value:'reduce'}]);
  await page.setViewport({width:1366,height:1000,deviceScaleFactor:1.25});
  await page.goto(fixture.url,{waitUntil:'networkidle0'});
  await page.evaluate(()=>{document.querySelectorAll('dialog[open]').forEach(d=>d.close());setDrawer(false);CloudShellTheme.set('dark');});
  const note='https://example.test/unique-renewal\n到期：2027-02-03\n<img src=x onerror="injected=1">';
  await page.evaluate((id)=>showConnectionForm(profiles.find(p=>p.id===id)),a.profileId);
  assert(await page.$eval('#connection-notes',e=>e.value.includes('2027-01-01')));
  await page.$eval('#connection-notes',(e,n)=>{e.value=n;},note);
  await page.click('#save-connection');
  await page.waitForFunction(()=>!document.querySelector('#connection-dialog').open);
  await page.waitForFunction((id,n)=>profiles.find(p=>p.id===id)?.notes===n,{},a.profileId,note);
  await page.evaluate(()=>{setDrawer(true);$('#connection-search').value='unique-renewal';renderConnections();});
  assert.equal(await page.$$eval('.server-profile-row',es=>es.length),1);
  await page.click(`.server-profile-row[data-profile-id="${a.profileId}"] .connection-card`);
  await page.click('.server-notes-preview');
  assert.equal(await page.$eval('#profile-notes-content',e=>e.textContent),note);
  assert.equal(await page.$eval('#profile-notes-content',e=>e.querySelectorAll('img,script').length),0);
  assert.equal(await page.evaluate(()=>window.injected),undefined);
  await page.screenshot({path:stage+'/notes-dark.png'});
  await page.click('#edit-profile-notes');
  assert.equal(await page.$eval('#connection-notes',e=>e.value),note);
  await page.click('#cancel-connection');await page.evaluate(()=>setDrawer(false));
  checks.push('Notes save through real API; edit/view/search preserve multiline text; HTML is inert');
  // These frontend session objects deliberately have no terminal transport;
  // all file browsing and text reads still use real isolated SFTP handlers.
  await page.evaluate((data)=>{
    sessions.clear();
    for(const f of data){const state=makeSessionState({...f});state.connected=true;state.ready=false;state.localOnly=false;state.host=document.createElement('div');state.term={options:{},focus(){},refresh(){},dispose(){}};sessions.set(f.id,state);}
    activeID=data[0].id;renderFiles();
  },fixture.sessions);
  await page.click('#path-history-button');
  await page.waitForFunction(()=>document.querySelectorAll('.path-history-row').length===2);
  await page.type('#path-history-search','XRAY');
  assert.equal(await page.$$eval('.path-history-row',es=>es.length),2);
  assert((await page.$$eval('.path-history-row mark',es=>es.length))>=2);
  for(const theme of ['dark','light']){
    await page.evaluate(t=>CloudShellTheme.set(t),theme);
    await page.waitForFunction(t=>document.documentElement.dataset.theme===t,{},theme);
    const geometry=await page.$eval('#path-history-dialog',e=>{const r=e.getBoundingClientRect();return {delta:Math.abs((r.left+r.right)/2-innerWidth/2),top:r.top,bottom:r.bottom,height:innerHeight,width:e.scrollWidth,client:e.clientWidth};});
    assert(geometry.delta<3&&geometry.top>=0&&geometry.bottom<=geometry.height&&geometry.width<=geometry.client+1,JSON.stringify(geometry));
    await page.screenshot({path:stage+`/path-history-${theme}.png`});
  }
  await page.$eval('#path-history-search',e=>{e.value='config.json';e.dispatchEvent(new Event('input'));});
  await page.keyboard.press('Enter');
  await page.waitForSelector('.text-editor-dialog[open]');
  await page.waitForFunction(()=>[...document.querySelectorAll('.text-editor-dialog textarea')].some(e=>e.value.includes('"server":0')));
  assert.equal(await page.$eval('#path-history-dialog',e=>e.open),false);
  await page.evaluate(()=>document.querySelectorAll('.text-editor-dialog[open]').forEach(d=>d.close()));
  await page.click('#path-history-button');await page.waitForFunction(()=>document.querySelectorAll('.path-history-row').length===2);
  await page.click(`.path-history-row[data-path="${a.directory}"]`);
  await page.waitForFunction(p=>current().cwd===p&&!document.querySelector('#path-history-dialog').open,{},a.directory);
  checks.push('History search highlights keyword; Enter opens original-server text; directory jump succeeds');
  // Clear A and verify B remains untouched. Cancelling must preserve A.
  await page.click('#path-history-button');await page.waitForFunction(()=>!document.querySelector('#clear-path-history').disabled);
  await page.click('#clear-path-history');await page.waitForSelector('#action-dialog[open]');await page.click('#action-cancel');
  assert.equal((await call(`/api/sessions/${a.id}/path-history`)).entries.length,2);
  await page.click('#clear-path-history');await page.click('#action-confirm');
  await page.waitForFunction(()=>document.querySelector('#path-history-list').children.length===0);
  assert.equal((await call(`/api/sessions/${a.id}/path-history`)).entries.length,0);
  assert.equal((await call(`/api/sessions/${b.id}/path-history`)).entries.length,2);
  await page.evaluate(id=>{activeID=id;renderFiles();},b.id);
  assert.equal(await page.$eval('#path-history-dialog',e=>e.open),false);
  await page.click('#path-history-button');await page.waitForFunction(()=>document.querySelectorAll('.path-history-row').length===2);
  assert((await page.$$eval('.path-history-row',es=>es.map(e=>e.dataset.path))).every(p=>p.startsWith(b.home)));
  await page.click('#close-path-history');
  checks.push('Clear cancellation and per-server clear; switching SSH tabs closes old popup and isolates records');
  // Delay a response from B, switch to A, then release it. It must not populate
  // A's popup or allow a stale click to execute on the new current session.
  await page.setRequestInterception(true);let held;
  const intercept=r=>{if(r.url().includes(`/api/sessions/${b.id}/path-history`))held=r;else r.continue();};
  page.on('request',intercept);
  await page.click('#path-history-button');
  for(let i=0;i<40&&!held;i++)await new Promise(r=>setTimeout(r,25));assert(held);
  await page.evaluate(id=>{activeID=id;renderFiles();},a.id);
  await held.continue();page.off('request',intercept);await page.setRequestInterception(false);
  await new Promise(r=>setTimeout(r,120));
  assert.equal(await page.$eval('#path-history-dialog',e=>e.open),false);
  await page.click('#path-history-button');await page.waitForFunction(()=>!document.querySelector('#path-history-empty').hidden);
  assert.equal(await page.$$eval('.path-history-row',es=>es.length),0);
  await page.click('#close-path-history');
  checks.push('Late request after session switch cannot leak previous server paths');
  await page.setViewport({width:800,height:600,deviceScaleFactor:1.5});
  await page.evaluate(id=>{activeID=id;renderFiles();},b.id);await page.click('#path-history-button');await page.waitForFunction(()=>document.querySelectorAll('.path-history-row').length===2);
  const fits=await page.$eval('#path-history-dialog',e=>{const r=e.getBoundingClientRect();return r.top>=0&&r.bottom<=innerHeight+1&&r.left>=0&&r.right<=innerWidth+1;});assert(fits);
  await page.screenshot({path:stage+'/path-history-small.png'});
  assert.deepEqual(errors,[]);
  fs.writeFileSync(stage+'/browser-results.json',JSON.stringify({passed:true,checks,errors},null,2));
  console.log('PASS',checks);
} catch(error){if(page)await page.screenshot({path:stage+'/browser-failure.png'});console.error({errors});throw error;} finally {await browser.close();}
