import fs from 'node:fs';
import assert from 'node:assert/strict';
const [stage,chrome,modulePath]=process.argv.slice(2);
const {default:puppeteer}=await import(modulePath),fixture=JSON.parse(fs.readFileSync(stage+'/browser-fixture.json'));
assert.equal(new URL(fixture.url).hostname,'127.0.0.1');
const init=await fetch(new URL('/api/appearance/patch',fixture.url),{method:'POST',headers:{'Content-Type':'application/json','X-CloudShell-Token':fixture.token},body:JSON.stringify({onboardingCompleted:true,startupAnimation:false,uiScale:1})});assert(init.ok);
const browser=await puppeteer.launch({executablePath:chrome,headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const errors=[],checks=[];let page;
try {
 page=await browser.newPage();page.on('pageerror',e=>errors.push(e.message));await page.setViewport({width:1280,height:900});await page.goto(fixture.url,{waitUntil:'networkidle0'});
 await page.evaluate(async()=>{document.querySelectorAll('dialog[open]').forEach(d=>d.close());await chooseAppearance({startupAnimation:false,onboardingCompleted:true,uiScale:1});setDrawer(false)});
 await page.click('#connection-button');
 const manager=await page.$eval('#connections-drawer',e=>{const r=e.getBoundingClientRect();return {center:(r.left+r.right)/2,width:innerWidth}});
 assert(Math.abs(manager.center-manager.width/2)<2);await page.click('#close-connections');
 await page.click('#add-session');await page.waitForSelector('#quick-connect-dialog[open]');assert(await page.$eval('#connections-drawer',e=>e.hidden));
 await page.type('#quick-connect-command','ssh -o ProxyCommand=bad a@localhost');await page.click('#quick-connect-submit');
 await page.waitForFunction(()=>document.querySelector('#quick-connect-output').textContent.includes('支持 ssh'));
 await page.click('#quick-connect-dialog [data-close]');
 await page.click('#toggle-sftp');assert(await page.$eval('#files-panel',e=>e.hidden));assert(await page.$eval('#files-splitter',e=>e.hidden));
 await page.click('#toggle-sftp');assert.equal(await page.$eval('#files-panel',e=>e.hidden),false);checks.push('Distinct server/quick-connect entries; centered server picker; SFTP hide/show; invalid SSH options rejected');
 await page.evaluate(()=>{
  sessions.clear();profiles=[];
  for(let i=0;i<8;i++){
   const id='issue-tab-'+i;profiles.push({id,name:i===1?'a much longer server name':'S'+i,host:'fixture.invalid',user:'qa',port:22});
   const state={...makeSessionState({id,profileId:id,home:'/'}),localOnly:false,connected:true,ready:true,term:{options:{},modes:{},focus(){},refresh(){},dispose(){}},host:Object.assign(document.createElement('div'),{hidden:true})};sessions.set(id,state);
  }
  activeID='issue-tab-0';renderTabs();
 });
 await new Promise(r=>setTimeout(r,150));
 const widths=await page.$$eval('.session-tab',es=>es.map(e=>e.getBoundingClientRect().width));assert(widths[1]>widths[2]+60,JSON.stringify(widths));
 const rowCounts=await page.$$eval('.session-tab',es=>Object.values(es.reduce((r,e)=>(r[e.offsetTop]=(r[e.offsetTop]||0)+1,r),{})));assert(rowCounts.every(n=>n<=6));checks.push('Tab width follows label length and rows never exceed six');
 await page.evaluate(()=>{window.sent=[];pasteTerminalText=(s,text,opts)=>sent.push({id:s.id,text,...opts});DengCommandComposer.run({id:'qa-param',name:'日志',body:'echo [p#1 文件] [p#2 行数]',appendCR:true});});
 assert(await page.$eval('#command-composer',e=>e.classList.contains('command-composer-compact')));
 assert.equal(await page.$eval('#composer-body',e=>e.getClientRects().length),0);assert.equal(await page.$eval('#composer-preview',e=>e.getClientRects().length),0);
 assert(await page.$eval('#composer-send',e=>{const r=e.getBoundingClientRect();return r.top>=0&&r.bottom<=innerHeight}));
 await page.screenshot({path:stage+'/issue-compact-command.png'});
 await page.type('[data-parameter="1"]','example.log');await page.type('[data-parameter="2"]','20');await page.click('#composer-send');
 assert.deepEqual(await page.evaluate(()=>sent),[{id:'issue-tab-0',text:'echo example.log 20',execute:true}]);
 await page.evaluate(()=>DengCommandComposer.open({id:'qa-edit',name:'编辑',body:'echo [p#1 内容]'}));
 assert(await page.$eval('#composer-body',e=>e.getClientRects().length>0));checks.push('Compact parameter execution hides template/preview, keeps send visible, sends exact parameters to pinned SSH; explicit editing remains available');
 await page.evaluate(()=>{
  localTasks.clear();for(const status of ['done','uploading','queued','failed','cancelled']) localTasks.set(status,{id:status,status,name:status,target:'/qa/'+status,total:10,done:status==='done'?10:0});showPane('transfers');renderTransfers();
 });
 await page.click('#clear-completed-transfers');await page.waitForFunction(()=>!localTasks.has('done'));
 assert.deepEqual(await page.evaluate(()=>[...localTasks.keys()]),['uploading','queued','failed','cancelled']);checks.push('Bulk clear preserves active, queued, failed and cancelled records');
 // Simulate native lifecycle without launching a GUI. Backend lock and its HTTP
 // gate remain real. Closing sockets must never wait on APIs that await unlock.
 await page.evaluate(async()=>{
  sessions.clear();localTasks.clear();window.socketCloses=0;window.nativeQuits=0;window.nativeCancels=0;
  window.go={main:{Desktop:{ConfirmQuit:async()=>{nativeQuits++},CancelQuit:async()=>{nativeCancels++}}}};
  const id='lock-owner';sessions.set(id,{...makeSessionState({id,profileId:'issue-tab-0',home:'/'}),connected:true,ready:false,ws:{close(){socketCloses++}},term:{dispose(){}},host:document.createElement('div')});activeID=id;
  const headers={'Content-Type':'application/json','X-CloudShell-Token':CLOUDSHELL.token};
  const req=async(path,body)=>{const r=await fetch(CLOUDSHELL.base+'/api/security-lock/'+path,{method:'POST',headers,body:JSON.stringify(body)});return r.json()};
  const {grant}=await req('authorize',{});await req('settings',{grant,enabled:true,idleSeconds:0,passwordEnabled:true,password:'4826',passwordHint:'fixture',twoFactorEnabled:false});await req('lock',{});
 });
 await page.waitForFunction(()=>DengSecurityLock.isLocked());await page.click('#security-unlock-open');
 await page.evaluate(()=>requestQuit());await page.waitForFunction(()=>!document.querySelector('#security-exit-form').hidden);
 assert(await page.$eval('#security-unlock-form',e=>e.hidden));assert(await page.$eval('#security-lock-screen',e=>e.open));
 await page.click('#security-exit-cancel');assert.equal(await page.evaluate(()=>nativeCancels),1);assert.equal(await page.evaluate(()=>socketCloses),0);assert.equal(await page.evaluate(()=>nativeQuits),0);
 await page.evaluate(()=>requestQuit());await page.click('#security-exit-confirm');
 await page.waitForFunction(()=>nativeQuits===1);assert.equal(await page.evaluate(()=>socketCloses),1);assert(await page.evaluate(()=>DengSecurityLock.isLocked()));
 const locked=await page.evaluate(async()=>{const r=await fetch(CLOUDSHELL.base+'/api/config',{headers:{'X-CloudShell-Token':CLOUDSHELL.token}});return r.status});assert.equal(locked,423);
 await page.screenshot({path:stage+'/issue-locked-exit.png'});
 checks.push('Locked unlock-form can cancel/confirm native close; owning socket closes; no unlock or protected API access');
 // Restore the isolated fixture for following suites.
 await page.evaluate(async()=>{const request=async(path,body)=>(await fetch(CLOUDSHELL.base+'/api/security-lock/'+path,{method:'POST',headers:{'Content-Type':'application/json','X-CloudShell-Token':CLOUDSHELL.token},body:JSON.stringify(body)})).json();await request('unlock',{method:'password',value:'4826'});const{grant}=await request('authorize',{method:'password',value:'4826'});await request('settings',{grant,enabled:false,passwordEnabled:false,twoFactorEnabled:false,idleSeconds:0})});
 assert.deepEqual(errors,[]);fs.writeFileSync(stage+'/issues-browser.json',JSON.stringify({passed:true,checks,errors},null,2));console.log('PASS',checks);
} catch(error){if(page)await page.screenshot({path:stage+'/issues-failure.png'});console.error({errors});throw error}finally{await browser.close()}
