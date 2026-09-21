import fs from 'node:fs';
import assert from 'node:assert/strict';
const [stage,chrome,modulePath]=process.argv.slice(2);
const {default:puppeteer}=await import(modulePath),fixture=JSON.parse(fs.readFileSync(stage+'/browser-fixture.json'));
const checks=[],errors=[],headers={'Content-Type':'application/json','X-CloudShell-Token':fixture.token};
async function call(path,method='GET',body){const r=await fetch(new URL(path,fixture.url),{method,headers,body:body===undefined?undefined:JSON.stringify(body)});const data=await r.json();assert(r.ok,JSON.stringify(data));return data;}
const pause=ms=>new Promise(r=>setTimeout(r,ms));
for (const s of fixture.sessions) for (const path of (await call(`/api/sessions/${s.id}/directory-favorites`)).paths) await call(`/api/sessions/${s.id}/directory-favorites`,'DELETE',{path});
await call('/api/appearance/patch','POST',{startupAnimation:false,onboardingCompleted:true,uiScale:1});
const browser=await puppeteer.launch({executablePath:chrome,headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
let page;
try{
 page=await browser.newPage();await page.emulateMediaFeatures([{name:'prefers-reduced-motion',value:'reduce'}]);page.on('pageerror',e=>errors.push(e.stack));await page.setViewport({width:1440,height:1000});await page.goto(fixture.url,{waitUntil:'networkidle0'});
 await page.evaluate(data=>{document.querySelectorAll('dialog[open]').forEach(d=>d.close());setDrawer(false);sessions.clear();for(const f of data){const s=makeSessionState({...f});s.connected=true;s.ready=false;s.localOnly=false;s.host=document.createElement('div');s.term={options:{},focus(){},refresh(){},dispose(){}};sessions.set(f.id,s);}activeID=data[0].id;showPane('files');renderFiles();},fixture.sessions);
 const [a,b]=fixture.sessions,ep=s=>`/api/sessions/${s.id}/directory-favorites`;
 await page.evaluate(async path=>await navigate(path,current()),a.directory);
 await page.click('#directory-favorites-button');await page.waitForFunction(()=>!document.querySelector('#favorite-current-directory').disabled);
 await page.click('#favorite-current-directory');await page.waitForFunction(()=>document.querySelectorAll('.directory-favorite-row').length===1);
 assert.deepEqual((await call(ep(a))).paths,[a.directory]);assert.deepEqual((await call(ep(b))).paths,[]);
 await page.keyboard.press('Escape');await page.evaluate(async path=>await navigate(path,current()),a.home);
 await page.click('#directory-favorites-button');await page.waitForFunction(()=>!document.querySelector('#favorite-current-directory').disabled);await page.click('#favorite-current-directory');await page.waitForFunction(()=>document.querySelectorAll('.directory-favorite-row').length===2);
 await page.type('#directory-favorites-search','XRAY');assert.equal(await page.$$eval('.directory-favorite-row',es=>es.length),1);
 await page.keyboard.press('Enter');await page.waitForFunction(path=>current().cwd===path&&document.querySelector('#directory-favorites').hidden,{},a.directory);
 checks.push('Real isolated SFTP: add/search/jump, separate saved-server lists, duplicate prevention');
 for(const theme of ['light','dark']){
  await page.evaluate(t=>CloudShellTheme.set(t),theme);await page.click('#directory-favorites-button');await page.waitForFunction(()=>document.querySelectorAll('.directory-favorite-row').length===2);
  await page.screenshot({path:stage+`/favorites-${theme}.png`});await page.keyboard.press('Escape');
 }
 // Keep a former server's request pending while the active SSH tab changes.
 await page.setRequestInterception(true);let held;
 const intercept=r=>{if(r.url().includes(ep(a)))held=r;else r.continue();};page.on('request',intercept);
 await page.click('#directory-favorites-button');for(let i=0;i<40&&!held;i++)await pause(25);assert(held);
 await page.evaluate(id=>{activeID=id;renderFiles()},b.id);await held.continue();page.off('request',intercept);await page.setRequestInterception(false);await pause(150);
 assert(await page.$eval('#directory-favorites',e=>e.hidden));await page.click('#directory-favorites-button');await page.waitForFunction(()=>!document.querySelector('#directory-favorites-empty').hidden);assert.equal(await page.$$eval('.directory-favorite-row',es=>es.length),0);await page.keyboard.press('Escape');
 await page.evaluate(id=>{activeID=id;renderFiles()},a.id);await page.click('#directory-favorites-button');await page.waitForFunction(()=>document.querySelectorAll('.directory-favorite-row').length===2);await page.click('.directory-favorite-remove');await page.waitForFunction(()=>document.querySelectorAll('.directory-favorite-row').length===1);assert.deepEqual((await call(ep(a))).paths,[a.directory]);await page.keyboard.press('Escape');
 checks.push('Late response cannot cross tabs; remove affects one server only; both themes verified');
 const geometry=[];
 for(const [width,height,scale] of [[1440,1000,1],[1440,800,1.5],[1000,700,1.25],[800,600,1],[1920,1080,2]]){
  await page.setViewport({width,height});await page.evaluate(s=>{appearance.uiScale=s;applyUIScale();showPane('files')},scale);await pause(80);
  const g=await page.evaluate(()=>{const rect=s=>{const r=document.querySelector(s).getBoundingClientRect();return {left:r.left,right:r.right,top:r.top,bottom:r.bottom}};return {tabs:rect('.file-tabs'),tab:rect('.file-tab'),toolbar:rect('.file-toolbar'),path:rect('.path-form'),bookmark:rect('#directory-favorites-button')};});
  assert(g.tab.bottom<=g.tabs.bottom+.8&&g.tab.top>=g.tabs.top-.8,JSON.stringify(g));assert(g.path.top>=g.toolbar.top-.8&&g.path.bottom<=g.toolbar.bottom+.8,JSON.stringify(g));assert(g.path.top>=g.tab.bottom,JSON.stringify(g));assert(g.bookmark.right<=g.path.left,JSON.stringify(g));geometry.push({width,height,scale,...g});
  await page.click('#directory-favorites-button');await page.waitForFunction(()=>document.querySelectorAll('.directory-favorite-row').length===1);
  assert(await page.$eval('#directory-favorites',e=>{const r=e.getBoundingClientRect();return r.left>=0&&r.top>=0&&r.right<=innerWidth+1&&r.bottom<=innerHeight+1}));await page.keyboard.press('Escape');
 }
 checks.push('Five viewport/scale cases: tabs, path and favorite controls never overlap; popup fits viewport');
 await page.setViewport({width:1440,height:1000});await page.evaluate(()=>{appearance.uiScale=1;applyUIScale()});
 const grant=(await call('/api/security-lock/authorize','POST',{})).grant;
 await call('/api/security-lock/settings','POST',{grant,enabled:true,idleSeconds:0,passwordEnabled:true,password:'4826',passwordHint:'test',twoFactorEnabled:false});
 await pause(1100);await page.waitForFunction(()=>!document.querySelector('#security-lock-button').disabled);
 await page.evaluate(()=>{document.querySelector('#path-input').focus();window.testLocks=0;window.testKeys=0;window.addEventListener('dengshell:locked',()=>testLocks++);window.addEventListener('keydown',()=>testKeys++);});
 // Hold the authoritative status reply to make even a single transient modal
 // observable; input must remain blocked without showing a false lock UI.
 await page.setRequestInterception(true);held=null;
 const holdStatus=r=>{if(r.url().includes('/api/security-lock/status'))held=r;else r.continue();};page.on('request',holdStatus);
 await page.evaluate(()=>window.dispatchEvent(new Event('focus')));for(let i=0;i<60&&!held;i++)await pause(25);assert(held);
 assert.equal(await page.$eval('#security-lock-screen',e=>e.open),false);assert.equal(await page.evaluate(()=>testLocks),0);assert.equal(await page.evaluate(()=>document.activeElement.id),'path-input');
 await page.keyboard.press('F8');assert.equal(await page.evaluate(()=>testKeys),0);
 await held.continue();page.off('request',holdStatus);await page.setRequestInterception(false);await page.waitForFunction(()=>!DengSecurityLock.isLocked());
 for(let i=0;i<15;i++){await page.evaluate(()=>window.dispatchEvent(new Event('focus')));await page.waitForFunction(()=>!DengSecurityLock.isLocked());}
 assert.equal(await page.evaluate(()=>testLocks),0);assert.equal(await page.evaluate(()=>document.activeElement.id),'path-input');
 checks.push('Repeated window refocus: no false lock event/modal/blur/focus loss; pending verification blocks keyboard');
 // Cross-window/manual locks must still close sensitive popovers and cover all UI.
 await page.click('#directory-favorites-button');await page.waitForFunction(()=>document.querySelectorAll('.directory-favorite-row').length===1);
 await call('/api/security-lock/lock','POST',{});await page.evaluate(()=>window.dispatchEvent(new Event('focus')));await page.waitForSelector('#security-lock-screen[open]');assert(await page.$eval('#directory-favorites',e=>e.hidden));
 assert.equal((await fetch(new URL(ep(a),fixture.url),{headers})).status,423);
 await call('/api/security-lock/unlock','POST',{method:'password',value:'4826'});await page.waitForFunction(()=>!DengSecurityLock.isLocked());
 // A failed focus verification must fail closed, not silently keep accepting input.
 await page.setRequestInterception(true);let aborted=false;const failStatus=r=>{if(r.url().includes('/api/security-lock/status')){aborted=true;r.abort('failed')}else r.continue()};page.on('request',failStatus);await page.evaluate(()=>window.dispatchEvent(new Event('focus')));await page.waitForSelector('#security-lock-screen[open]');assert(aborted);page.off('request',failStatus);await page.setRequestInterception(false);await page.waitForFunction(()=>!DengSecurityLock.isLocked());
 checks.push('Real lock still blocks APIs and closes paths; failed status checks cover UI; recovery works');
 const endGrant=(await call('/api/security-lock/authorize','POST',{method:'password',value:'4826'})).grant;await call('/api/security-lock/settings','POST',{grant:endGrant,enabled:false});
 assert.deepEqual(errors,[]);fs.writeFileSync(stage+'/issues-22-23-browser.json',JSON.stringify({passed:true,checks,geometry,errors},null,2));console.log('PASS',checks);
}catch(error){if(page)await page.screenshot({path:stage+'/issues-22-23-failure.png'});console.error(errors);throw error}finally{await browser.close()}
