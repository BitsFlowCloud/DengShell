import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
const [url, sessionID, remote, local, module] = process.argv.slice(2);
const { default: puppeteer } = await import(module);
const browser = await puppeteer.launch({executablePath:process.env.CHROME_BIN || '/usr/bin/google-chrome',headless:true,args:['--no-sandbox']});
const origin = new URL(url).origin, token = new URL(url).hash.split('=')[1];
await fetch(origin+'/api/appearance/patch',{method:'POST',headers:{'Content-Type':'application/json','X-CloudShell-Token':token},body:JSON.stringify({onboardingCompleted:true,startupAnimation:false,uiScale:1})});
const errors=[], deletes=[];
const write=(name,data='fixture')=>{const target=path.join(remote,name);fs.mkdirSync(path.dirname(target),{recursive:true});fs.writeFileSync(target,data)};
for(const name of ['a.txt','b.txt','c.txt','d.txt','keep.txt','folder/child.txt','other/a.txt','other/b.txt'])write(name);
fs.symlinkSync(path.join(remote,'keep.txt'),path.join(remote,'link.txt'));
let page;
try {
 page=await browser.newPage();page.on('pageerror',e=>errors.push(e.message));
 await page.setRequestInterception(true);page.on('request',r=>{const u=new URL(r.url());if(['http:','https:'].includes(u.protocol)&&u.origin!==origin)return r.abort();if(u.pathname.endsWith('/file-action')&&r.postData()){const data=JSON.parse(r.postData());if(data.action==='delete')deletes.push(data.path)}return r.continue()});
 await page.setViewport({width:1440,height:1000});await page.goto(url,{waitUntil:'networkidle0'});
 await page.evaluate(async({sessionID,remote})=>{
  document.querySelectorAll('dialog[open]').forEach(d=>d.close());setDrawer(false);monitorVisible=()=>false;
  window.qaState={...makeSessionState({id:sessionID,profileId:'qa-files',home:remote}),connected:true,sftpAvailable:true,ready:false,term:{options:{},modes:{},focus(){},refresh(){},dispose(){}},host:document.createElement('div')};
  profiles.push({id:'qa-files',name:'文件操作测试服务器',host:'fixture.invalid',user:'qa',port:22});
  sessions.set(sessionID,qaState);activeID=sessionID;renderTabs();showPane('files');document.documentElement.style.setProperty('--files-height','470px');await navigate(remote,qaState);
 },{sessionID,remote});
 const row=name=>`#file-list tr[data-file-name="${name}"]`;
 const selected=()=>page.$$eval('#file-list tr.selected',rows=>rows.map(row=>row.dataset.fileName).sort());
 async function click(name,modifier){if(modifier)await page.keyboard.down(modifier);await page.click(row(name));if(modifier)await page.keyboard.up(modifier);}
 async function choose(names){await click(names[0]);for(const name of names.slice(1))await click(name,'Control');assert.deepEqual(await selected(),[...names].sort());}
 const dialog=()=>page.waitForSelector('#action-dialog[open]');
 async function finish(confirm=true){await page.click(confirm?'#action-confirm':'#action-cancel');await page.waitForFunction(()=>!qaState.fileDeletePending);}
 await choose(['a.txt','c.txt']);assert.match(await page.$eval('#file-status-count',e=>e.textContent),/已选 2 项/);assert(await page.$eval('#rename-file',e=>e.disabled));assert.equal(await page.$eval('#download-file',e=>e.textContent),'打包下载');
 await click('a.txt','Control');assert.deepEqual(await selected(),['c.txt']);await click('b.txt','Meta');assert.deepEqual(await selected(),['b.txt','c.txt']);
 await click('a.txt');await click('d.txt','Shift');assert.deepEqual(await selected(),['a.txt','b.txt','c.txt','d.txt']);
 await page.keyboard.down('Control');await page.keyboard.press('a');await page.keyboard.up('Control');assert.equal((await selected()).length,8);assert.equal(await page.evaluate(()=>getSelection().toString()),'');
 await page.keyboard.press('Escape');assert.deepEqual(await selected(),[]);
 // Sorting retains names; filtering cannot leave invisible items selected.
 await choose(['a.txt','c.txt']);await page.click('#sort-size');assert.deepEqual(await selected(),['a.txt','c.txt']);
 await page.type('#file-filter','a.txt');assert.deepEqual(await selected(),['a.txt']);await page.$eval('#file-filter',e=>{e.value='';e.dispatchEvent(new Event('input'))});assert.deepEqual(await selected(),['a.txt']);
 await page.click('#refresh-files');await page.waitForFunction(()=>!document.querySelector('#file-status-count').textContent.includes('读取目录'));assert.deepEqual(await selected(),['a.txt']);
 // One dialog; cancelling keeps all data. Right click preserves the whole batch.
 await choose(['a.txt','b.txt']);await page.click(row('a.txt'),{button:'right'});
 assert.deepEqual(await selected(),['a.txt','b.txt']);assert.match(await page.$eval('.file-context-menu',e=>e.textContent),/删除选中的 2 项/);assert.doesNotMatch(await page.$eval('.file-context-menu',e=>e.textContent),/重命名/);
 await page.click('.file-context-danger');await dialog();assert.equal(await page.$$eval('dialog:modal',rows=>rows.length),1);assert.match(await page.$eval('#action-description',e=>e.textContent),/a.txt/);assert.match(await page.$eval('#action-description',e=>e.textContent),/b.txt/);assert.equal(deletes.length,0);await finish(false);assert(fs.existsSync(path.join(remote,'a.txt')));
 await page.evaluate(()=>{window.qaCopied='';navigator.clipboard.writeText=async text=>{qaCopied=text}});
 await page.click(row('b.txt'),{button:'right'});await page.evaluate(()=>[...document.querySelectorAll('.file-context-menu button')].find(b=>b.textContent==='复制所选路径').click());assert.deepEqual((await page.evaluate(()=>qaCopied)).split('\n').sort(),['a.txt','b.txt'].map(name=>path.join(remote,name)));
 await page.click(row('c.txt'),{button:'right'});assert.deepEqual(await selected(),['c.txt']);await page.keyboard.press('Escape');
 // Selected entries archive into one actual download; no unselected files.
 await choose(['a.txt','folder']);
 const cdp=await page.createCDPSession();await cdp.send('Page.setDownloadBehavior',{behavior:'allow',downloadPath:local});
 await page.click('#download-file');
 await new Promise((resolve,reject)=>{const end=Date.now()+10000;const timer=setInterval(()=>{if(fs.existsSync(path.join(local,'DengShell-files.tar.gz'))){clearInterval(timer);resolve()}else if(Date.now()>end){clearInterval(timer);reject(new Error('archive download timeout'))}},50)});
 const {execFileSync}=await import('node:child_process');const archive=execFileSync('tar',['-tzf',path.join(local,'DengShell-files.tar.gz')],{encoding:'utf8'});assert.match(archive,/a.txt/);assert.match(archive,/folder\/child.txt/);assert.doesNotMatch(archive,/b.txt|keep.txt|other/);
 // The desktop bridge receives the whole selection once, including folders.
 await page.evaluate(async()=>{window.qaArchives=[];window.go={main:{Desktop:{DownloadSelectionArchive:async(id,paths)=>{qaArchives.push({id,paths});return '/fixture/output.tar.gz'}}}};try{await downloadSelected()}finally{delete window.go}});
 const nativeArchive=await page.evaluate(()=>qaArchives);assert.equal(nativeArchive.length,1);assert.equal(nativeArchive[0].id,sessionID);assert.deepEqual(nativeArchive[0].paths.sort(),['a.txt','folder'].map(name=>path.join(remote,name)));
 // Appearance and modal bounds in both themes. Delete key uses the same batch.
 await choose(['a.txt','b.txt']);await page.keyboard.press('Delete');await dialog();
 for(const theme of ['dark','light']){
  await page.evaluate(t=>CloudShellTheme.set(t),theme);await page.waitForFunction(t=>document.documentElement.dataset.theme===t&&!document.documentElement.dataset.lightSwitch,{},theme);
  if(process.env.DENGSHELL_UI_ARTIFACTS)await page.screenshot({path:process.env.DENGSHELL_UI_ARTIFACTS+`/file-multiselect-${theme}.png`});
  const colors=await page.$eval(row('a.txt'),r=>({selected:getComputedStyle(r).backgroundColor,plain:getComputedStyle(document.querySelector('tr[data-file-name="keep.txt"]')).backgroundColor}));assert.notEqual(colors.selected,colors.plain);
 }
 await finish();assert(!fs.existsSync(path.join(remote,'a.txt')));assert(!fs.existsSync(path.join(remote,'b.txt')));assert(fs.existsSync(path.join(remote,'c.txt')));assert.equal(deletes.length,2);
 // Double-click opens a directory, clears selection; text files still open editor.
 await page.click(row('folder'),{count:2});await page.waitForFunction(p=>current().cwd===p,{},path.join(remote,'folder'));assert.deepEqual(await selected(),[]);
 await page.click(row('child.txt'),{count:2});await page.waitForSelector('dialog.text-editor-dialog[open]');assert.equal(await page.$eval('.text-editor-area',e=>e.value),'fixture');
 await page.evaluate(()=>document.querySelectorAll('dialog[open]').forEach(d=>d.close()));
 await page.evaluate(async remote=>{await navigate(remote,qaState)},remote);
 // A vanished file is reported, others succeed, failed selection remains visible
 // if still on disk. Inject permission denial without altering host permissions.
 write('denied.txt');write('success.txt');await page.evaluate(()=>navigate(qaState.cwd,qaState));
 await page.evaluate(()=>{window.qaAPI=api;api=(p,options={})=>p.endsWith('/file-action')&&JSON.parse(options.body||'{}').path?.endsWith('/denied.txt')?Promise.reject(new Error('permission denied')):qaAPI(p,options)});
 await choose(['denied.txt','success.txt']);await page.click('#delete-file');await dialog();await finish();assert(fs.existsSync(path.join(remote,'denied.txt')));assert(!fs.existsSync(path.join(remote,'success.txt')));assert.match(await page.$eval('#toast',e=>e.textContent),/已删除 1 项，1 项未完成/);assert.deepEqual(await selected(),['denied.txt']);await page.evaluate(()=>{api=qaAPI});
 // Captured targets survive directory change while waiting for confirmation.
 await choose(['c.txt','d.txt']);await page.click('#delete-file');await dialog();await page.evaluate(()=>navigate(qaState.cwd+'/other',qaState));await finish();assert(!fs.existsSync(path.join(remote,'c.txt')));assert(fs.existsSync(path.join(remote,'other/a.txt')));assert.deepEqual(await selected(),[]);
 await page.evaluate(remote=>navigate(remote,qaState),remote);
 // Session switches keep their own selection, and stale/reconnected identities abort.
 await choose(['denied.txt','link.txt']);await page.evaluate(()=>{window.qaOther={...qaState,id:'other-session',fileSelection:null};sessions.set(qaOther.id,qaOther);activeID=qaOther.id;renderFiles()});assert.deepEqual(await selected(),[]);await click('keep.txt');await page.evaluate(()=>{activeID=qaState.id;renderFiles()});assert.deepEqual(await selected(),['denied.txt','link.txt']);
 await page.click('#delete-file');await dialog();const beforeDisconnect=deletes.length;await page.evaluate(()=>{qaState.connected=false});await finish();assert.equal(deletes.length,beforeDisconnect);assert(fs.existsSync(path.join(remote,'denied.txt')));await page.evaluate(()=>{qaState.connected=true;renderFiles()});
 await page.click('#delete-file');await dialog();await page.evaluate(()=>{sessions.set(qaState.id,{...qaState})});await finish();assert.equal(deletes.length,beforeDisconnect);await page.evaluate(()=>{sessions.set(qaState.id,qaState);renderFiles()});
 // A confirmed operation stays on the original session even if a different tab becomes active.
 await page.click('#delete-file');await dialog();await page.evaluate(()=>{activeID=qaOther.id;renderFiles()});await finish();assert(!fs.existsSync(path.join(remote,'denied.txt')));assert(fs.existsSync(path.join(remote,'keep.txt')));assert.deepEqual(await selected(),['keep.txt']);await page.evaluate(()=>{activeID=qaState.id;renderFiles()});
 fs.symlinkSync(path.join(remote,'keep.txt'),path.join(remote,'link.txt'));await page.evaluate(()=>navigate(qaState.cwd,qaState));
 // Recursive batch delete removes a link itself, never its unselected target.
 await choose(['folder','link.txt']);await page.click('#delete-file');await dialog();await finish();assert(!fs.existsSync(path.join(remote,'folder')));assert(!fs.existsSync(path.join(remote,'link.txt')));assert.equal(fs.readFileSync(path.join(remote,'keep.txt'),'utf8'),'fixture');
 assert.deepEqual(errors,[]);console.log('PASS: actual SFTP Ctrl/Cmd/Shift selection, select all, no text selection, filter/sort/refresh, right-click batch, single confirmation/cancel, batch archive, double-click navigation, delete/partial error, session/directory isolation, stale session rejection, symlink-safe recursion, light/dark.');
}catch(error){if(page&&process.env.DENGSHELL_UI_ARTIFACTS)await page.screenshot({path:process.env.DENGSHELL_UI_ARTIFACTS+'/file-multiselect-failure.png'});console.error({errors,deletes});throw error}finally{await browser.close()}
