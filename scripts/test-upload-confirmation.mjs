import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
const [url, sessionID, remote, local, module] = process.argv.slice(2);
const { default: puppeteer } = await import(module);
const browser = await puppeteer.launch({executablePath: process.env.CHROME_BIN || '/usr/bin/google-chrome', headless:true, args:['--no-sandbox']});
const origin = new URL(url).origin;
const token = new URL(url).hash.split('=')[1];
await fetch(origin+'/api/appearance/patch',{method:'POST',headers:{'Content-Type':'application/json','X-CloudShell-Token':token},body:JSON.stringify({onboardingCompleted:true,startupAnimation:false,uiScale:1})});
const errors=[];
const contents=file=>fs.readFileSync(path.join(remote,file),'utf8');
const write=(root,file,data)=>{const target=path.join(root,file);fs.mkdirSync(path.dirname(target),{recursive:true});fs.writeFileSync(target,data)};
try {
 const page=await browser.newPage();page.on('pageerror',e=>errors.push(e.message));
 await page.setRequestInterception(true);page.on('request',r=>{const u=new URL(r.url());if(['http:','https:'].includes(u.protocol)&&u.origin!==origin)r.abort();else r.continue()});
 await page.setViewport({width:1280,height:800});await page.goto(url,{waitUntil:'networkidle0'});
 await page.evaluate(({sessionID,remote})=>{
  document.querySelectorAll('dialog[open]').forEach(d=>d.close());setDrawer(false);
  window.qaState={id:sessionID,connected:true,sftpAvailable:true,cwd:remote,name:'上传测试服务器'};
  sessions.set(sessionID,qaState);
  // The fixture has a real file channel but no SSH terminal/monitor channels.
  refreshUploaded=()=>{};navigate=async()=>{};
  window.qaOp=null;window.qaError=null;window.qaSettled=false;
 },{sessionID,remote});
 async function start(items,native=false){await page.evaluate(({items,native})=>{
   window.qaSettled=false;window.qaError=null;
   const request=native?uploadNative(items,qaState,qaState.cwd):queueFiles(items.map(item=>({file:new File([item.data],item.name),relativePath:item.name})),qaState,qaState.cwd);
   window.qaOp=request.catch(e=>{window.qaError=e.message}).finally(()=>window.qaSettled=true);
 },{items,native});}
 const ready=()=>page.waitForFunction(()=>document.querySelector('#upload-confirmation-title')?.textContent.startsWith('发现'));
 async function decision(text){await page.evaluate(text=>[...document.querySelectorAll('#upload-confirmation-dialog button')].find(b=>b.textContent===text).click(),text);await page.waitForFunction(()=>qaSettled);assert.equal(await page.evaluate(()=>qaError),null);}
 async function finishTasks(){await page.waitForFunction(async()=>{await pollTransfers();return [...localTasks.values()].every(t=>['done','failed','cancelled'].includes(t.status))});return page.evaluate(()=>[...localTasks.values()].map(t=>({status:t.status,error:t.error,target:t.target})));}
 async function reset(){await finishTasks();await page.evaluate(async()=>{for(const t of localTasks.values())await remove('/api/transfers/'+t.id);localTasks.clear();renderTransfers()});}
 for(const name of ['甲.txt','nested/乙.txt'])write(remote,name,'old');
 const files=[{name:'甲.txt',data:'new-a'},{name:'nested/乙.txt',data:'new-b'},{name:'fresh.txt',data:'new-c'}];
 await start(files);await ready();
 assert.equal(await page.$$eval('#upload-confirmation-dialog',e=>e.length),1);
 assert.equal(await page.$$eval('.upload-conflict-list li',e=>e.length),2);
 assert.equal(await page.evaluate(()=>localTasks.size),0);assert.equal(contents('甲.txt'),'old');assert(!fs.existsSync(path.join(remote,'fresh.txt')));
 assert.equal(await page.evaluate(()=>document.activeElement.textContent),'取消上传');
 await page.keyboard.press('Escape');await page.waitForFunction(()=>qaSettled);assert.equal(await page.evaluate(()=>localTasks.size),0);assert.equal(contents('甲.txt'),'old');
 await start(files);await ready();
 // Both themes and a narrow viewport: dialog/actions fit, long lists scroll.
 for(const theme of ['dark','light']){await page.evaluate(t=>CloudShellTheme.set(t),theme);await new Promise(r=>setTimeout(r,1000));
   assert.equal(await page.evaluate(()=>CloudShellTheme.current),theme);
   if(process.env.DENGSHELL_UI_ARTIFACTS)await page.screenshot({path:process.env.DENGSHELL_UI_ARTIFACTS+`/overwrite-${theme}.png`});
   await page.setViewport({width:640,height:480});await new Promise(r=>setTimeout(r,300));
   const bounds=await page.$eval('#upload-confirmation-dialog',d=>{const r=d.getBoundingClientRect();return {top:r.top,bottom:r.bottom,left:r.left,right:r.right,overflow:d.scrollWidth>d.clientWidth+1}});
   assert(bounds.top>=0&&bounds.bottom<=481&&bounds.left>=0&&bounds.right<=641&&!bounds.overflow,JSON.stringify(bounds));
   await page.setViewport({width:1280,height:800});}
 await decision('覆盖并上传');assert((await finishTasks()).every(t=>t.status==='done'));
 assert.equal(contents('甲.txt'),'new-a');assert.equal(contents('nested/乙.txt'),'new-b');assert.equal(contents('fresh.txt'),'new-c');
 await reset();
 for(const name of ['甲.txt','nested/乙.txt'])write(remote,name,'old');fs.unlinkSync(path.join(remote,'fresh.txt'));
 await start(files);await ready();await decision('跳过同名文件');assert.equal((await finishTasks()).length,1);assert.equal(contents('甲.txt'),'old');assert.equal(contents('fresh.txt'),'new-c');await reset();
 // Native multi-select and folder upload go through the same single prompt.
 for(const name of ['folder/a.txt','folder/sub/b.txt','folder/fresh.txt'])write(local,name,'native-new');
 for(const name of ['folder/a.txt','folder/sub/b.txt'])write(remote,name,'native-old');
 await start([path.join(local,'folder')],true);await ready();assert.equal(await page.$$eval('.upload-conflict-list li',e=>e.length),2);
 assert.equal(contents('folder/a.txt'),'native-old');await decision('取消上传');assert(!fs.existsSync(path.join(remote,'folder/fresh.txt')));
 await start([path.join(local,'folder')],true);await ready();await decision('覆盖并上传');assert((await finishTasks()).every(t=>t.status==='done'));assert.equal(contents('folder/a.txt'),'native-new');assert.equal(contents('folder/sub/b.txt'),'native-new');await reset();
 for(const name of ['one.txt','two.txt']){write(local,name,'selection-new');write(remote,name,'selection-old')}
 await start(['one.txt','two.txt'].map(name=>path.join(local,name)),true);await ready();await decision('跳过同名文件');assert.equal((await finishTasks()).length,0);assert.equal(contents('one.txt'),'selection-old');
 await start(['one.txt','two.txt'].map(name=>path.join(local,name)),true);await ready();await decision('覆盖并上传');assert.equal((await finishTasks()).length,2);assert.equal(contents('two.txt'),'selection-new');await reset();
 // All-skipped browser batch creates no requests/tasks; symlinks cannot be overwritten.
 await start([{name:'one.txt',data:'do-not-upload'}]);await ready();await decision('跳过同名文件');assert.equal((await finishTasks()).length,0);
 fs.symlinkSync(path.join(remote,'one.txt'),path.join(remote,'link.txt'));
 await start([{name:'link.txt',data:'unsafe'}]);await ready();assert(await page.$eval('#upload-confirmation-dialog .primary-button',e=>e.hidden));await decision('跳过同名文件');assert.equal(contents('one.txt'),'selection-new');
 // Non-conflicting upload proceeds without asking for a decision.
 await start([{name:'unique.txt',data:'unique'}]);await page.waitForFunction(()=>qaSettled);assert((await finishTasks()).every(t=>t.status==='done'));assert.equal(contents('unique.txt'),'unique');await reset();
 // Large selections remain one modal, bounded DOM, safe text insertion.
 await page.evaluate(()=>{window.qaOriginalAPI=api;api=async(p,o)=>p.endsWith('/upload-check')?{conflicts:Array.from({length:5000},(_,i)=>({path:'/tmp/<img onerror=alert(1)>-'+i,canOverwrite:true}))}:qaOriginalAPI(p,o)});
 await start([{name:'many.txt',data:'data'}]);await ready();assert.equal(await page.$$eval('.upload-conflict-list li',e=>e.length),100);assert.equal(await page.$$eval('.upload-conflict-list img',e=>e.length),0);assert.match(await page.$eval('.upload-confirmation-more',e=>e.textContent),/5000/);await decision('取消上传');
 await page.evaluate(()=>{api=qaOriginalAPI});
 // Cancel while preflight is still pending. A late native response cannot start uploads.
 await page.evaluate(()=>{api=(p,o)=>p.endsWith('/upload-check')?new Promise(r=>window.qaReleaseCheck=r):qaOriginalAPI(p,o)});
 await start([path.join(local,'one.txt')],true);await page.waitForSelector('#upload-confirmation-dialog');await decision('取消上传');
 await page.evaluate(()=>{qaReleaseCheck({conflicts:[]});api=qaOriginalAPI});await new Promise(r=>setTimeout(r,100));assert.equal(await page.evaluate(()=>localTasks.size),0);
 // A second batch cannot replace the first pending modal.
 await page.evaluate(()=>{
   qaFirst=DengUploadConfirmation.prepare(qaState,{targets:[qaState.cwd+'/one.txt']});
   qaSecond=DengUploadConfirmation.prepare(qaState,{targets:[qaState.cwd+'/two.txt']});
 });await ready();assert.equal(await page.$$eval('#upload-confirmation-dialog',e=>e.length),1);await page.keyboard.press('Escape');await ready();assert.equal(await page.$$eval('#upload-confirmation-dialog',e=>e.length),1);await page.keyboard.press('Escape');await page.evaluate(()=>Promise.all([qaFirst,qaSecond]));
 assert.deepEqual(errors,[]);
 console.log('PASS: actual SFTP browser/native/folder uploads, single batch confirmation, exact overwrite, skip/cancel, preflight cancellation, queued dialogs, non-regular protection, 5000 conflicts, light/dark responsive layout');
} finally {await browser.close()}
