import fs from 'node:fs';
import assert from 'node:assert/strict';
const [stage,chrome,modulePath]=process.argv.slice(2);
const {default:puppeteer}=await import(modulePath),fixture=JSON.parse(fs.readFileSync(stage+'/browser-fixture.json'));
const endpoint=new URL(fixture.url).origin;
assert.equal(new URL(endpoint).hostname,'127.0.0.1');
async function api(path,body){const r=await fetch(endpoint+path,{method:body===undefined?'GET':'POST',headers:{'Content-Type':'application/json','X-CloudShell-Token':fixture.token},body:body===undefined?undefined:JSON.stringify(body)});const data=await r.json();return {status:r.status,data};}
await api('/api/appearance/patch',{startupAnimation:false,onboardingCompleted:true,uiScale:1});
const {data:{grant}}=await api('/api/security-lock/authorize',{});
assert.equal((await api('/api/security-lock/settings',{grant,enabled:true,passwordEnabled:true,password:'4826',idleSeconds:0})).status,200);
const browser=await puppeteer.launch({executablePath:chrome,headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const checks=[],errors=[];let page;
try{
 page=await browser.newPage();page.on('pageerror',e=>errors.push(e.message));await page.setViewport({width:1280,height:900});await page.goto(fixture.url,{waitUntil:'networkidle0'});
 await page.waitForFunction(()=>window.DengSecurityLock&&!DengSecurityLock.isLocked());
 await page.evaluate(()=>{
  document.querySelectorAll('dialog[open]').forEach(d=>d.close());setDrawer(false);
  window.nativeQuits=0;window.nativeCancels=0;window.failQuit=false;
  window.go={main:{Desktop:{ConfirmQuit:async()=>{if(failQuit)throw Error('fixture save failure');nativeQuits++},CancelQuit:async()=>{nativeCancels++}}}};
 });
 // Exercise the actual focus handler with a delayed authoritative lock reply.
 await page.setRequestInterception(true);page.on('request',async r=>{if(r.url().endsWith('/api/security-lock/status'))await new Promise(resolve=>setTimeout(resolve,150));await r.continue()});
 for(let i=0;i<5;i++){
  await page.evaluate(()=>{window.dispatchEvent(new Event('focus'));requestQuit();requestQuit();});
  await page.waitForFunction(()=>!DengSecurityLock.isLocked()&&document.querySelector('#exit-dialog').open);
  assert(await page.$eval('#security-exit-form',e=>e.hidden));
  assert(await page.$eval('#cancel-exit',e=>{const r=e.getBoundingClientRect();return document.elementFromPoint(r.x+r.width/2,r.y+r.height/2)===e}));
  await page.click('#cancel-exit');await page.waitForFunction(()=>!document.querySelector('#exit-dialog').open);
 }
 assert.equal(await page.evaluate(()=>nativeQuits),0);assert.equal(await page.evaluate(()=>nativeCancels),5);
 checks.push('Repeated focus checks and repeated close requests retain one visible, clickable exit confirmation; cancel never exits');
 await page.evaluate(()=>{requestQuit();window.dispatchEvent(new Event('focus'));});
 await page.waitForFunction(()=>!DengSecurityLock.isLocked()&&document.querySelector('#exit-dialog').open);
 await page.evaluate(()=>{failQuit=true});await page.click('#confirm-exit');
 await page.waitForFunction(()=>document.querySelector('#exit-description').textContent.includes('fixture save failure'));
 assert(await page.$eval('#exit-dialog',e=>e.open));await page.evaluate(()=>{failQuit=false});await page.click('#confirm-exit');
 await page.waitForFunction(()=>nativeQuits===1&&!document.querySelector('#exit-dialog').open);
 checks.push('Focus changes while confirmation is open preserve it; native failure stays visible and retry succeeds');
 await api('/api/security-lock/lock',{});await page.waitForSelector('#security-lock-screen[open]');
 await page.click('#security-unlock-open');await page.evaluate(()=>requestQuit());
 await page.waitForFunction(()=>!document.querySelector('#security-exit-form').hidden);
 await page.click('#security-exit-cancel');await page.waitForFunction(()=>!document.querySelector('#security-unlock-form').hidden);
 assert.equal((await api('/api/config')).status,423);assert.equal(await page.evaluate(()=>nativeQuits),1);
 await page.evaluate(()=>requestQuit());await api('/api/security-lock/unlock',{method:'password',value:'4826'});
 await page.waitForFunction(()=>!DengSecurityLock.isLocked()&&document.querySelector('#exit-dialog').open);
 await page.click('#cancel-exit');await page.waitForFunction(()=>!document.querySelector('#exit-dialog').open);
 checks.push('Locked unlock-form exit can be cancelled; another device unlocking moves confirmation into the unlocked window');
 // Lock while a normal exit confirmation is visible, then confirm without
 // unlocking. Only this window owns the fixture socket.
 await page.evaluate(()=>{
  sessions.clear();localTasks.clear();window.socketCloses=0;
  const id='exit-owner';sessions.set(id,{...makeSessionState({id,profileId:'exit-fixture',home:'/'}),connected:true,ready:false,ws:{close(){socketCloses++}},term:{dispose(){}},host:document.createElement('div')});activeID=id;requestQuit();
 });
 await api('/api/security-lock/lock',{});await page.waitForFunction(()=>!document.querySelector('#security-exit-form').hidden&&document.querySelector('#security-lock-screen').open);
 await page.click('#security-exit-confirm');await page.waitForFunction(()=>nativeQuits===2);
 assert.equal(await page.evaluate(()=>socketCloses),1);assert.equal((await api('/api/config')).status,423);
 checks.push('Locking during normal confirmation keeps locked exit available; confirmation closes owned socket without unlocking protected APIs');
 await api('/api/security-lock/unlock',{method:'password',value:'4826'});const authorization=await api('/api/security-lock/authorize',{method:'password',value:'4826'});
 await api('/api/security-lock/settings',{grant:authorization.data.grant,enabled:false});await page.waitForFunction(()=>!DengSecurityLock.isLocked());
 await page.evaluate(()=>requestQuit());await page.waitForSelector('#exit-dialog[open]');await page.click('#confirm-exit');await page.waitForFunction(()=>nativeQuits===3);
 checks.push('Security lock disabled still supports normal confirmed exit');
 assert.deepEqual(errors,[]);fs.writeFileSync(stage+'/exit-confirmation.json',JSON.stringify({passed:true,checks,errors},null,2));console.log('PASS',checks);
}catch(error){if(page)await page.screenshot({path:stage+'/exit-failure.png'});throw error}finally{await browser.close()}
