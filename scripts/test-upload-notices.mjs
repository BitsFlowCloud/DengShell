import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
const [url,sessionID,remote,local,module]=process.argv.slice(2);
const {default:puppeteer}=await import(module);
const origin=new URL(url).origin,token=new URL(url).hash.split('=')[1];
await fetch(origin+'/api/appearance/patch',{method:'POST',headers:{'Content-Type':'application/json','X-CloudShell-Token':token},body:JSON.stringify({onboardingCompleted:true,startupAnimation:false,uiScale:1})});
const browser=await puppeteer.launch({executablePath:process.env.CHROME_BIN||'/usr/bin/google-chrome',headless:true,args:['--no-sandbox']});
let page,hold=false;const held=[],errors=[];
try{
 page=await browser.newPage();page.on('pageerror',e=>errors.push(e.message));await page.setViewport({width:1440,height:900});
 await page.setRequestInterception(true);page.on('request',r=>{const u=new URL(r.url());if(['http:','https:'].includes(u.protocol)&&u.origin!==origin)return r.abort();if(hold&&u.pathname.endsWith('/upload')){held.push(r);return}r.continue()});
 // Transfer/security/sync polling continues after the UI is usable, so network
 // idleness is not a startup signal. Wait until configuration and workspace
 // restoration complete; assert the restored pane separately below.
 const waitForWorkspace=()=>page.waitForFunction(()=>window.DengPortablePreferences?.ready&&workspaceInitialized&&document.querySelector('#connection-button')?.title==='打开服务器管理');
 await page.goto(url,{waitUntil:'domcontentloaded'});await waitForWorkspace();
 await page.evaluate(async({sessionID,remote})=>{
  document.querySelectorAll('dialog[open]').forEach(d=>d.close());setDrawer(false);monitorVisible=()=>false;
  window.qaState={...makeSessionState({id:sessionID,profileId:'upload-qa',home:remote}),connected:true,sftpAvailable:true,host:Object.assign(document.createElement('div'),{hidden:true}),term:{options:{},focus(){},dispose(){}}};sessions.set(sessionID,qaState);activeID=sessionID;
  refreshUploaded=()=>{};navigate=async()=>{};await pollTransfers();showPane('files');
 },{sessionID,remote});
 const pane=()=>page.$eval('.file-tab.active',e=>e.dataset.pane);
 const notice=()=>page.$eval('#transfer-count',e=>({hidden:e.hidden,text:e.textContent,title:e.closest('button').title}));
 const upload=names=>page.evaluate(names=>queueFiles(names.map(name=>({file:new File(['real SFTP payload'],name),relativePath:name})),qaState,qaState.cwd),names);
 async function settle(){await page.waitForFunction(async()=>{await pollTransfers();return [...localTasks.values()].every(task=>['done','failed','cancelled'].includes(task.status))});}
 async function release(){hold=false;await Promise.all(held.splice(0).map(r=>r.continue()));}
 const read=()=>page.click('.file-tab[data-pane="transfers"]');
 const go=which=>page.click(`.file-tab[data-pane="${which}"]`);
 async function assertCount(count){const n=await notice();assert.equal(n.hidden,count===0,JSON.stringify(n));assert.equal(n.text,count>99?'99+':String(count));}
 // Queue a real browser upload but hold its network request: no pane change and
 // no completion notice merely because the bytes entered the local queue.
 hold=true;await upload(['one.txt','two.txt']);assert.equal(await pane(),'files');await assertCount(0);
 await page.waitForFunction(()=>[...localTasks.values()].length===2&&[...localTasks.values()].every(t=>t.status==='uploading'));
 await release();await settle();assert.equal(await pane(),'files');await assertCount(2);
 assert.equal(fs.readFileSync(path.join(remote,'one.txt'),'utf8'),'real SFTP payload');
 for(let i=0;i<3;i++)await page.evaluate(()=>pollTransfers());await assertCount(2);
 for(const theme of ['dark','light']){await page.evaluate(t=>CloudShellTheme.set(t),theme);await page.waitForFunction(t=>document.documentElement.dataset.theme===t&&!document.documentElement.dataset.lightSwitch,{},theme);const c=await page.$eval('#transfer-count',e=>({background:getComputedStyle(e).backgroundColor,color:getComputedStyle(e).color}));assert.equal(c.background,'rgb(201, 76, 87)');assert.equal(c.color,'rgb(255, 255, 255)');if(process.env.DENGSHELL_UI_ARTIFACTS)await (await page.$('.file-tabs')).screenshot({path:process.env.DENGSHELL_UI_ARTIFACTS+`/upload-badge-${theme}.png`});}
 await read();await assertCount(0);await go('files');await page.evaluate(()=>pollTransfers());await assertCount(0);
 // Native file/folder uploads also leave the current pane alone, and completion
 // from several batches accumulates until the user opens the transfer list.
 fs.mkdirSync(path.join(local,'folder'),{recursive:true});fs.writeFileSync(path.join(local,'folder/甲.txt'),'native-a');fs.writeFileSync(path.join(local,'folder/乙.txt'),'native-b');
 await go('commands');await page.evaluate(folder=>uploadNative([folder],qaState,qaState.cwd),path.join(local,'folder'));await settle();assert.equal(await pane(),'commands');await assertCount(2);assert.equal(fs.readFileSync(path.join(remote,'folder/甲.txt'),'utf8'),'native-a');
 await upload(['three.txt']);await settle();assert.equal(await pane(),'commands');await assertCount(3);
 // Reading an active transfer list acknowledges only results that already
 // finished. Jobs that finish later after leaving it create another notice.
 await read();await assertCount(0);hold=true;await upload(['later.txt']);await assertCount(0);await go('files');await release();await settle();await assertCount(1);
 await read();await upload(['watched.txt']);await settle();await assertCount(0);assert.equal(await pane(),'transfers');await go('files');await page.evaluate(()=>pollTransfers());await assertCount(0);
 // Drag-and-drop uses the same background queue.
 await page.evaluate(()=>{const dt=new DataTransfer();dt.items.add(new File(['drag'], 'dragged.txt'));document.querySelector('#files-panel').dispatchEvent(new DragEvent('drop',{dataTransfer:dt,bubbles:true,cancelable:true}))});
 await page.waitForFunction(()=>[...localTasks.values()].some(t=>t.name==='dragged.txt'));await settle();assert.equal(await pane(),'files');await assertCount(1);await read();await go('files');
 // Windows/macOS native drops use a separate path callback. Reopening SFTP
 // must not allow a later config refresh (including background sync) to restore
 // the previously viewed transfer pane while the upload is in progress.
 await read();await page.click('#toggle-sftp');await page.click('#toggle-sftp');assert.equal(await pane(),'files');
 fs.writeFileSync(path.join(local,'native-drop.txt'),'native-drop');
 await page.evaluate(async file=>{
  const rect=document.querySelector('#files-panel').getBoundingClientRect();
  await window.cloudshellNativeDrop({x:rect.left+40,y:rect.top+80,paths:[file]});
  await loadProfiles();
 },path.join(local,'native-drop.txt'));
 await settle();assert.equal(await pane(),'files','config reload after native drop must preserve the visible file pane');
 assert.equal(fs.readFileSync(path.join(remote,'native-drop.txt'),'utf8'),'native-drop');await assertCount(1);await read();await go('files');
 // Test the actual native drop callback with a directory while another pane
 // is selected; successful uploads should only add the completion badge.
 fs.mkdirSync(path.join(local,'dropped-folder'));fs.writeFileSync(path.join(local,'dropped-folder/文件.txt'),'native-directory');
 await go('commands');await page.evaluate(async folder=>{const r=document.querySelector('#files-panel').getBoundingClientRect();await cloudshellNativeDrop({x:r.left+40,y:r.top+80,paths:[folder]});await loadProfiles()},path.join(local,'dropped-folder'));
 await settle();assert.equal(await pane(),'commands');await assertCount(1);assert.equal(fs.readFileSync(path.join(remote,'dropped-folder/文件.txt'),'utf8'),'native-directory');await read();await go('files');
 // A config response can arrive after the user changes panes. Even if the
 // older response contains 'transfers', it must not undo that local choice.
 await page.evaluate(async()=>{
  window.qaConfigAPI=api;window.qaOldConfig=await api('/api/config');qaOldConfig.appearance.layout['dengshell.workspace']={pane:'transfers'};
  api=(p,o)=>p==='/api/config'?new Promise(resolve=>{window.qaConfigResolve=resolve}):qaConfigAPI(p,o);window.qaConfigRefresh=loadProfiles();
 });
 await page.waitForFunction(()=>typeof qaConfigResolve==='function');await go('commands');await page.evaluate(async()=>{await DengPortablePreferences.flush();qaConfigResolve(qaOldConfig);await qaConfigRefresh;api=qaConfigAPI});assert.equal(await pane(),'commands');await go('files');
 // A failed actual SFTP write is also visible as a new result, and a successful
 // retry gets its own notice even though the original failure was viewed.
 fs.mkdirSync(path.join(remote,'blocked'),{mode:0o500});
 try{await page.evaluate(()=>queueFiles([{file:new File(['retry bytes'],'retry.txt'),relativePath:'blocked/retry.txt'}],qaState,qaState.cwd));await settle();await assertCount(1);assert.match((await notice()).title,/失败 1 项/);await read();await assertCount(0);}finally{fs.chmodSync(path.join(remote,'blocked'),0o700)}
 await go('files');await page.evaluate(()=>retryTask([...localTasks.values()].find(t=>t.name==='retry.txt')));await settle();await assertCount(1);assert.equal(fs.readFileSync(path.join(remote,'blocked/retry.txt'),'utf8'),'retry bytes');await read();await go('files');
 // Cancelling a queued upload is not a completed-upload notice.
 hold=true;await upload(['cancel.txt']);await page.evaluate(()=>cancelTask([...localTasks.values()].find(t=>t.name==='cancel.txt')));await release();await settle();await assertCount(0);
 // Hidden transfer panels and hidden native windows must not mark results read.
 await read();await page.evaluate(()=>setWorkspaceVisible(false,false));await upload(['hidden-panel.txt']);await settle();await assertCount(1);assert(await page.$eval('#files-panel',e=>e.hidden));await page.click('#toggle-sftp');await assertCount(1);await read();await assertCount(0);
 await page.evaluate(()=>{window.DengShellWindowHidden=true});await upload(['hidden-window.txt']);await settle();await assertCount(1);await page.evaluate(()=>{window.DengShellWindowHidden=false;updateTransferNotice()});await assertCount(0);
 await page.click('#clear-completed-transfers');await page.waitForFunction(()=>![...localTasks.values()].some(t=>t.status==='done'));await assertCount(0);
 // Exercise initial-history and fast native-upload races with controlled API
 // snapshots, keeping the real upload tests above free of status mocks.
 await go('files');await page.evaluate(async()=>{
  window.qaAPI=api;localTasks.clear();transfersInitialized=false;
  window.qaSnapshot=[{id:'old-done',name:'old',status:'done',total:1,done:1},{id:'old-failed',name:'old',status:'failed',total:1,done:0},{id:'pending',name:'pending',status:'uploading',total:1,done:0}];
  api=async(p,o)=>p==='/api/transfers'?structuredClone(qaSnapshot):qaAPI(p,o);await pollTransfers();
 });await assertCount(0);await page.evaluate(async()=>{qaSnapshot[2].status='done';qaSnapshot[2].done=1;await pollTransfers()});await assertCount(1);await read();await go('files');await page.evaluate(()=>pollTransfers());await assertCount(0);
 // A native upload may finish during the first poll, before upload-local returns.
 await page.evaluate(async()=>{
  localTasks.clear();transfersInitialized=false;qaSnapshot=[{id:'native-fast',name:'fast',status:'done',total:1,done:1}];
  api=async(p,o)=>{if(p.endsWith('/upload-check'))return {conflicts:[]};if(p.endsWith('/upload-local')){await pollTransfers();return {ids:['native-fast']}}if(p==='/api/transfers')return structuredClone(qaSnapshot);return qaAPI(p,o)};
  await uploadNative(['/isolated/fast'],qaState,qaState.cwd);
 });await assertCount(1);await page.evaluate(()=>pollTransfers());await assertCount(1);
 await read();await go('files');await page.evaluate(()=>pollTransfers());await assertCount(0);
 await page.evaluate(()=>{localTasks.clear();for(let i=0;i<120;i++)localTasks.set('many-'+i,{id:'many-'+i,status:'done'});updateTransferNotice()});await assertCount(120);assert.match((await notice()).title,/120 个新结果/);await read();await assertCount(0);
 await page.evaluate(()=>{api=qaAPI;localTasks.clear();renderTransfers()});assert.deepEqual(errors,[]);
 // Startup still restores a deliberately selected pane; after reopening SFTP,
 // both config refresh and the next startup must retain the file pane.
 await read();await page.evaluate(()=>DengPortablePreferences.flush());await page.reload({waitUntil:'domcontentloaded'});await waitForWorkspace();assert.equal(await pane(),'transfers');
 await page.click('#toggle-sftp');await page.click('#toggle-sftp');await page.evaluate(()=>DengPortablePreferences.flush());await page.reload({waitUntil:'domcontentloaded'});await waitForWorkspace();assert.equal(await pane(),'files');assert.deepEqual(errors,[]);
 console.log('PASS: browser/native/folder/drop uploads stay on current pane; native drop + config refresh regression; delayed config response; startup pane restoration; notifications only after remote completion; read acknowledgement; no repeated notices; accumulated results; failures/retry; cancel; hidden panel/window; initial history and fast native-result race; light/dark badge.');
}catch(error){if(page&&process.env.DENGSHELL_UI_ARTIFACTS)await page.screenshot({path:process.env.DENGSHELL_UI_ARTIFACTS+'/upload-notice-failure.png'});console.error({errors});throw error}finally{await browser.close()}
