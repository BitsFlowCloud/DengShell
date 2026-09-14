import fs from 'node:fs';import assert from 'node:assert/strict';
const [stage,modulePath,endpointPath]=process.argv.slice(2);if(!stage||!modulePath||!endpointPath)throw Error('Usage: QA_DIR PUPPETEER_MODULE PRIVATE_BROWSER_ENDPOINT');
const {default:puppeteer}=await import(modulePath),fixture=JSON.parse(fs.readFileSync(stage+'/browser-fixture.json'));
assert.equal(new URL(fixture.url).hostname,'127.0.0.1');
const browser=await puppeteer.connect({browserWSEndpoint:fs.readFileSync(endpointPath,'utf8')});
const api=async(path,body)=>{const r=await fetch(new URL(path,fixture.url),{method:body?'POST':'GET',headers:{'X-CloudShell-Token':fixture.token,'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined});const data=await r.json();assert(r.ok,JSON.stringify(data));return data};
const page=await browser.newPage(),errors=[],checks=[];page.on('pageerror',e=>errors.push(e.message));
try{
 await api('/api/appearance/patch',{onboardingCompleted:true,startupAnimation:false,theme:'light',uiFontId:'builtin:ui-ibm-plex-sans-sc',fontId:'builtin:jetbrains-mono'});
 for(const g of (await api('/api/config')).groupNodes.filter(g=>!g.parentId)){const plan=await api(`/api/group-nodes/${g.id}/deletion`);await api(`/api/group-nodes/${g.id}/deletion`,{revision:plan.revision,name:plan.name,confirmations:3})}
 const colors=['#183f52','#efc47f','#204528','#f7dded'],groups=[];
 for(let i=0;i<4;i++)groups.push(await api('/api/group-nodes',{name:['测试导入','云服务器','配置备份','归档'][i],parentId:groups.at(-1)?.id||'',backgroundColor:colors[i]}));
 const other=await api('/api/group-nodes',{name:'保留分组'});
 const profiles=[];for(const [name,group]of [['测试 A',groups[3]],['测试 B',other]])profiles.push(await api('/api/profiles',{name,groupId:group.id,user:'fixture',host:'192.0.2.1',port:22,auth:'password',secret:''}));
 await page.setRequestInterception(true);page.on('request',r=>{
  const u=new URL(r.url());
  if(u.pathname.startsWith('/api/sessions/editor-fixture-')&&u.pathname.endsWith('/file-content'))r.respond({status:200,contentType:'application/json',body:JSON.stringify({path:u.searchParams.get('path'),text:'server='+u.pathname.split('/')[3]+'\n',encoding:'utf-8',sha256:'a'.repeat(64)})});
  else if(/^https?:/.test(u.protocol)&&u.origin!==new URL(fixture.url).origin)r.abort();else r.continue();
 });
 await page.setViewport({width:1440,height:1000});await page.goto(fixture.url,{waitUntil:'networkidle2'});
 await page.evaluate(async()=>{await loadProfiles();setDrawer(true)});
 await page.evaluate(id=>chooseServerFolder(id),groups[3].id);
 assert.equal(await page.$$eval('.server-folder-colored',r=>r.length),4);
 const size=await page.$eval('#connections-drawer',e=>e.getBoundingClientRect().width/Number(document.body.style.zoom||1));assert(size<=710,`drawer ${size}`);
 for(const [i,g]of groups.entries()){
  const rgb=await page.$eval(`[data-group-id="${g.id}"] .server-folder-row`,e=>getComputedStyle(e).backgroundColor);const channels=[1,3,5].map(n=>parseInt(colors[i].slice(n,n+2),16));assert.equal(rgb,`rgb(${channels.join(', ')})`);
 }
 await page.screenshot({path:stage+'/groups-light.png'});
 await page.evaluate(()=>document.documentElement.dataset.theme='dark');await page.screenshot({path:stage+'/groups-dark.png'});
 assert.equal(await page.$eval(`[data-group-id="${groups[3].id}"] .server-folder-row`,e=>getComputedStyle(e).outlineWidth),'2px');
 await page.evaluate(()=>document.documentElement.dataset.theme='light');
 await page.evaluate(id=>openServerGroupEditor(id),groups[2].id);await page.$eval('#group-background-color',e=>{e.value='#b3d8e8';e.dispatchEvent(new Event('input',{bubbles:true}))});await page.click('#save-server-group');await page.waitForFunction(()=>!document.querySelector('#server-group-dialog').open);
 assert.equal((await api('/api/config')).groupNodes.find(g=>g.id===groups[2].id).backgroundColor,'#b3d8e8');checks.push('四级目录独立背景色、明暗主题对比度、选中边框、保存与 700px 窄列表');
 // A short native window must leave real clickable space for connection rows.
 await page.setViewport({width:768,height:560});
 await page.waitForFunction(()=>innerHeight===560);
 await page.evaluate(id=>chooseServerFolder(id),groups[2].id);
 await page.waitForFunction(()=>document.querySelector('#server-explorer-title').textContent==='配置备份');
 const listHeight=await page.$eval('#connection-groups',e=>e.getBoundingClientRect().height);
 assert(listHeight>=120,`short window connection list has only ${listHeight}px`);
 await page.click(`[data-profile-id="${profiles[0].id}"] .connection-card`);
 assert.equal(await page.$eval('#open-selected-servers',e=>e.textContent),'打开所选 (1)');
 await page.click(`[data-profile-id="${profiles[0].id}"] .server-profile-more`);
 assert.equal(await page.$eval('#server-group-menu',e=>e.hidden),false);
 await page.screenshot({path:stage+'/groups-short-window.png'});
 await page.evaluate(()=>closeServerGroupMenu());
 await page.setViewport({width:1280,height:900});
 await page.evaluate(()=>{appearance.uiScale=1.6;applyUIScale()});
 await page.waitForFunction(()=>document.querySelector('#connections-drawer').classList.contains('server-manager-compact'));
 assert(await page.$eval('#connection-groups',e=>e.getBoundingClientRect().height)>=120);
 await page.click(`[data-profile-id="${profiles[0].id}"] .connection-card`);
 assert.equal(await page.$eval('#open-selected-servers',e=>e.textContent),'打开所选 (1)');
 await page.evaluate(()=>{appearance.uiScale=1;applyUIScale()});
 await page.setViewport({width:1440,height:1000});
 checks.push('768×560 矮窗口保留可点击连接列表，单击选择和管理菜单正常');
 const startDelete=async()=>{await page.evaluate(id=>{window.qaDelete=deleteServerGroupTree(id).catch(e=>toast(e.message))},groups[0].id);await page.waitForSelector('#action-dialog[open]')};
 const confirm=async(step)=>{await page.waitForFunction(step=>document.querySelector('#action-title').textContent.includes(`${step} / 3`),{},step);await page.click('#action-confirm')};
 for(const step of [1,2,3]){await startDelete();for(let n=1;n<step;n++)await confirm(n);await page.waitForFunction(n=>document.querySelector('#action-title').textContent.includes(`${n} / 3`),{},step);await page.click('#action-cancel');await page.evaluate(()=>window.qaDelete);assert((await api('/api/config')).groupNodes.some(g=>g.id===groups[0].id))}
 await startDelete();await confirm(1);await confirm(2);await page.type('#action-input','错误名称');await confirm(3);await page.evaluate(()=>window.qaDelete);assert((await api('/api/config')).servers.some(p=>p.id===profiles[0].id));
 await startDelete();await confirm(1);await confirm(2);await api('/api/group-nodes',{name:'确认期间新增',parentId:groups[0].id});await page.type('#action-input',groups[0].name);await confirm(3);await page.evaluate(()=>window.qaDelete);assert((await api('/api/config')).servers.some(p=>p.id===profiles[0].id));
 await startDelete();await confirm(1);await confirm(2);await page.type('#action-input',groups[0].name);await confirm(3);await page.evaluate(()=>window.qaDelete);
 const final=await api('/api/config');assert(!final.groupNodes.some(g=>g.id===groups[0].id));assert(!final.servers.some(p=>p.id===profiles[0].id));assert(final.servers.some(p=>p.id===profiles[1].id));checks.push('三次确认逐步取消、名称不匹配、确认期间新增保护、子树永久删除及保留其他连接');
 await page.evaluate(async profile=>{
  setDrawer(false);profiles.push({...profile,id:'editor-owner-a',name:'服务器 A'},{...profile,id:'editor-owner-b',name:'服务器 B'});
  for(const suffix of ['a','b']){const state={id:'editor-fixture-'+suffix,profileId:'editor-owner-'+suffix,connected:true,closed:false};sessions.set(state.id,state);await DengTextEditors.openText(state,'/etc/service/config.json')}
 },profiles[1]);
 assert.equal(await page.$$eval('.text-editor-dialog[open] .text-editor-tab',r=>r.length),2);
 assert.equal(await page.$eval('.text-editor-dialog h2',e=>e.textContent),'文本编辑器');
 assert.equal(await page.$eval('.text-editor-panel:not([hidden]) .text-editor-owner',e=>e.textContent),'/etc/service/config.json');
 await page.evaluate(()=>[...document.querySelectorAll('.text-editor-layout-tools button')].find(e=>e.textContent==='移到新窗口').click());
 assert.equal(await page.$$eval('.text-editor-dialog[open]',r=>r.length),2);
 assert.deepEqual(await page.$$eval('.text-editor-dialog[open] .text-editor-tabs',r=>r.map(t=>t.querySelectorAll('[role=tab]').length)),[1,1]);
 assert.deepEqual(await page.$$eval('.text-editor-dialog[open] .text-editor-area',r=>r.map(t=>t.value).sort()),['server=editor-fixture-a\n','server=editor-fixture-b\n']);
 await page.screenshot({path:stage+'/editors-multiple.png'});checks.push('跨服务器同名文件独立标签与窗口、归属标识、正文隔离、重复标题与路径精简');
 assert.deepEqual(errors,[]);fs.writeFileSync(stage+'/groups-editors-browser.json',JSON.stringify({checks,errors},null,2));console.log(checks.map(c=>'PASS '+c).join('\n'));
}finally{await page.close();await browser.disconnect()}
