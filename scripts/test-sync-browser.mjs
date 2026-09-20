import fs from 'node:fs';
import assert from 'node:assert/strict';
import net from 'node:net';
const [stage,chrome,modulePath]=process.argv.slice(2);
const {default:puppeteer}=await import(modulePath);
const fixture=JSON.parse(fs.readFileSync(stage+'/ui-fixtures.json'));
const browser=await puppeteer.launch({executablePath:chrome,headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const errors=[],checks=[];
const sleep=ms=>new Promise(r=>setTimeout(r,ms));
async function api(which,path,body){const u=new URL(fixture[which].url);const token=new URLSearchParams(u.hash.slice(1)).get('token');const r=await fetch(u.origin+path,{method:body===undefined?'GET':'POST',headers:{'X-CloudShell-Token':token,'Content-Type':'application/json'},body:body===undefined?undefined:JSON.stringify(body)});const v=await r.json();assert(r.ok,v.error);return v;}
async function page(which){await api(which,'/api/appearance/patch',{startupAnimation:false,onboardingCompleted:true,uiScale:1});console.log('Load isolated '+which);const p=await browser.newPage();p.on('pageerror',e=>errors.push(e.message));await p.setViewport({width:1280,height:900});await p.goto(fixture[which].url,{waitUntil:'networkidle0'});await p.waitForFunction(()=>window.DengSync&&window.DengSecurityLock&&!DengSecurityLock.isLocked());await p.evaluate(()=>document.querySelectorAll('dialog[open]').forEach(d=>d.close()));return p;}
async function waitIdle(p){await p.waitForFunction(()=>!document.querySelector('#sync-now').disabled,{timeout:15000});}
try{
 const a=await page('owner'),b=await page('peer');await a.bringToFront();console.log('Checking sync layout');
 const geometry=await a.evaluate(()=>{const r=id=>{const b=document.getElementById(id).getBoundingClientRect();return{x:b.x,right:b.right,width:b.width,height:b.height}};return{lock:r('security-lock-button'),sync:r('sync-button'),theme:r('theme-toggle')}});
 assert.equal(geometry.sync.x-geometry.lock.right,14);assert.equal(geometry.sync.width,28);assert.equal(geometry.lock.x-geometry.theme.right,14);checks.push('Separate 28 px sync button exactly 14 CSS px after existing lock button');
 await a.click('#sync-button');await a.waitForSelector('#sync-dialog[open]');assert.equal(await a.$('#sync-google-tab'),null);assert.equal(await a.$('#sync-google-login'),null);assert.equal(await a.$('#sync-oauth-details'),null);assert.equal(await a.$eval('#sync-local-panel',e=>e.hidden),false);assert.equal(await a.$eval('#sync-connect',e=>e.disabled),false);
 await a.evaluate(()=>CloudShellTheme.set('light'));await a.waitForFunction(()=>!document.documentElement.dataset.lightSwitch);await sleep(100);await a.screenshot({path:stage+'/sync-light.png'});
 await a.evaluate(()=>CloudShellTheme.set('dark'));await a.waitForFunction(()=>!document.documentElement.dataset.lightSwitch);await sleep(100);await a.screenshot({path:stage+'/sync-dark.png'});
 for(const zoom of [1,1.25,1.5,2]){
  await a.evaluate(z=>{document.body.style.zoom=z;document.documentElement.style.setProperty('--view-width',innerWidth/z+'px');document.documentElement.style.setProperty('--view-height',innerHeight/z+'px');window.dispatchEvent(new Event('resize'));},zoom);
  const r=await a.$eval('#sync-dialog',d=>{const r=d.getBoundingClientRect();return{x:r.x,y:r.y,w:r.width,h:r.height,iw:innerWidth,ih:innerHeight,sw:d.scrollWidth,cw:d.clientWidth}});
  assert(Math.abs(r.x+r.w/2-r.iw/2)<3,JSON.stringify(r));assert(Math.abs(r.y+r.h/2-r.ih/2)<3,JSON.stringify(r));assert(r.x>=0&&r.y>=0&&r.w<=r.iw&&r.h<=r.ih,JSON.stringify(r));assert(r.sw<=r.cw+1);
 }
 await a.evaluate(()=>{document.body.style.zoom=1;document.documentElement.style.setProperty('--view-width',innerWidth+'px');document.documentElement.style.setProperty('--view-height',innerHeight+'px');window.dispatchEvent(new Event('resize'));});
 await a.setViewport({width:480,height:700});await sleep(100);await a.screenshot({path:stage+'/sync-narrow.png'});assert(await a.$eval('#sync-dialog',d=>d.scrollWidth<=d.clientWidth+1));await a.setViewport({width:1280,height:900});
 assert(await a.$eval('.sync-check',e=>getComputedStyle(e).flexDirection==='row'));checks.push('Local-only setup, no Google/OAuth UI, light/dark, narrow layout, centered and bounded at 100–200% UI zoom');
 await a.select('#sync-host-ip','127.0.0.1');
 await a.$eval('#sync-local-guide',e=>e.open=true);assert.match(await a.$eval('#sync-local-guide',e=>e.textContent),/10 分钟/);await a.screenshot({path:stage+'/sync-local-guide.png'});
 const port=await new Promise(resolve=>{const s=net.createServer();s.listen(0,'127.0.0.1',()=>{const p=s.address().port;s.close(()=>resolve(p));});});
 await a.$eval('#sync-host-port',(e,p)=>e.value=p,port);await a.$eval('#sync-device-name',e=>e.value='UI isolated owner');await a.type('#sync-password','test-browser-sync-password');await a.click('#sync-connect');
 await a.waitForFunction(()=>!document.querySelector('#sync-connected').hidden&&!document.querySelector('#sync-secret-panel').hidden,{timeout:20000});assert.equal((await a.$eval('#sync-secret-value',e=>e.value)).length,43);await waitIdle(a);
 await a.click('#sync-secret-hide');await a.click('#sync-invite');await a.waitForFunction(()=>document.querySelector('#sync-secret-value').value.startsWith('dengsync1.'));const invite=await a.$eval('#sync-secret-value',e=>e.value);
 await b.bringToFront();await b.click('#sync-button');await b.select('#sync-local-kind','join');await b.type('#sync-join-code',invite);await b.$eval('#sync-device-name',e=>e.value='UI isolated peer');await b.type('#sync-password','test-browser-sync-password');await b.click('#sync-connect');await b.waitForFunction(()=>!document.querySelector('#sync-connected').hidden,{timeout:20000});await waitIdle(b);
 const saved=await api('owner','/api/profiles',{name:'UI fixture server',host:'fixture.invalid',port:22,user:'root',auth:'password',secret:'not-to-be-synced',proxy:{type:'direct'}});
 await api('owner','/api/sync/run',{});await api('peer','/api/sync/run',{});const cfg=await api('peer','/api/config');assert(cfg.servers.some(p=>p.name==='UI fixture server'&&!p.hasSecret));checks.push('Actual UI creates HTTPS service and one-use invite; second backend joins and syncs server without password');
 await a.bringToFront();await waitIdle(a);await a.click('#sync-history-open');await a.waitForFunction(()=>!document.querySelector('#sync-history-panel').hidden);assert((await a.$$eval('#sync-history-select option',es=>es.length))>0);
 await a.click('#sync-close');await a.click('#sync-button');await waitIdle(a);
 // Closing the window must also discard a recovery response still in flight.
 await a.setRequestInterception(true);
 let releaseRecovery;
 const heldRecovery=new Promise(resolve=>releaseRecovery=resolve);
 const intercept=request=>{if(new URL(request.url()).pathname==='/api/sync/recovery')releaseRecovery(request);else void request.continue();};
 a.on('request',intercept);
 await a.click('#sync-recovery-show');const pendingRecovery=await heldRecovery;
 await a.click('#sync-close');
 const recoveryResponse=a.waitForResponse(r=>new URL(r.url()).pathname==='/api/sync/recovery');
 await pendingRecovery.continue();await (await recoveryResponse).text();await waitIdle(a);
 assert.equal(await a.$eval('#sync-secret-value',e=>e.value),'');assert.equal(await a.$eval('#sync-secret-panel',e=>e.hidden),true);
 a.off('request',intercept);await a.setRequestInterception(false);
 checks.push('Closing the sync window discards an in-flight recovery response');
 await a.click('#sync-button');await waitIdle(a);
 const grant=await api('owner','/api/security-lock/authorize',{});
 // The exact API contract is shared with security-lock.js.
 await api('owner','/api/security-lock/settings',{grant:grant.grant,enabled:true,passwordEnabled:true,password:'1234',idleSeconds:0});
 await a.click('#sync-recovery-show');await a.waitForFunction(()=>!!document.querySelector('#sync-secret-value').value);
 await api('owner','/api/security-lock/lock',{});await a.waitForSelector('#security-lock-screen[open]');assert.equal(await a.$eval('#sync-dialog',e=>e.open),false);assert.equal(await a.$eval('#sync-secret-value',e=>e.value),'');
 await api('owner','/api/security-lock/unlock',{method:'password',value:'1234'});await a.waitForFunction(()=>!DengSecurityLock.isLocked());assert.equal((await api('owner','/api/sync/status')).unlocked,false);checks.push('Security lock closes sync modal, erases displayed recovery/invite/password and clears backend sync key');
 assert.deepEqual(errors,[]);fs.writeFileSync(stage+'/browser-results.json',JSON.stringify({checks,errors},null,2));console.log(JSON.stringify({checks,errors},null,2));
}catch(e){console.error(e);console.error({errors});for(const p of await browser.pages()){if(p.url()!=='about:blank')await p.screenshot({path:stage+'/sync-failure-'+new URL(p.url()).port+'.png'}).catch(()=>{});}process.exitCode=1;}finally{await Promise.race([browser.close(),sleep(3000)]);browser.process()?.kill('SIGKILL');}
