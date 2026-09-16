import fs from 'node:fs';
import assert from 'node:assert/strict';
import puppeteer from '/home/bitsflow/.cache/dengshell-dev-tools/node_modules/puppeteer-core/lib/puppeteer/puppeteer-core.js';
const stage=process.env.DENG_MANAGER_QA_DIR || '/home/bitsflow/.cache/dengshell-r40-manager-20260916/qa';
const base=process.env.DENG_MANAGER_BASE_SOURCE;
const fixture=JSON.parse(fs.readFileSync(stage+'/browser-fixture.json'));
assert.equal(new URL(fixture.url).hostname,'127.0.0.1');
const api=async(path,body)=>{const r=await fetch(new URL(path,fixture.url),{method:body?'POST':'GET',headers:{'X-CloudShell-Token':fixture.token,'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined});const d=await r.json();assert(r.ok,JSON.stringify(d));return d};
await api('/api/appearance/patch',{onboardingCompleted:true,startupAnimation:false,theme:'light',uiScale:1});
for(const g of (await api('/api/config')).groupNodes.filter(g=>!g.parentId)){const plan=await api(`/api/group-nodes/${g.id}/deletion`);await api(`/api/group-nodes/${g.id}/deletion`,{revision:plan.revision,name:plan.name,confirmations:3})}
const groups=[];for(const [name,parent]of [['VPS 厂商',-1],['AWS',0],['AWS01 深层分组',1],['第四级归档',2],['其他厂商',-1],['其他子分组',4]])groups.push(await api('/api/group-nodes',{name,parentId:parent<0?'':groups[parent].id}));
const profiles=[];for(let i=0;i<24;i++)profiles.push(await api('/api/profiles',{name:`测试服务器 ${i+1}`,groupId:groups[i%4].id,user:'fixture',host:'192.0.2.'+(i+1),port:22,auth:'password',secret:''}));
const browser=await puppeteer.launch({executablePath:'/usr/bin/google-chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const errors=[],checks=[],layouts=[];let before;
async function load(old=false){const page=await browser.newPage();page.on('pageerror',e=>errors.push(e.message));await page.setRequestInterception(true);page.on('request',r=>{const u=new URL(r.url());if(old&&['/','/server-manager.css','/server-manager.js','/server-explorer.js'].includes(u.pathname)){const name=u.pathname==='/'?'index.html':u.pathname.slice(1);return r.respond({status:200,contentType:name.endsWith('.html')?'text/html':name.endsWith('.css')?'text/css':'application/javascript',body:fs.readFileSync(base+'/web/'+name)})}if(/^https?:/.test(u.protocol)&&u.origin!==new URL(fixture.url).origin)r.abort();else r.continue()});await page.setViewport({width:1440,height:1000});await page.goto(fixture.url,{waitUntil:'networkidle0'});await page.evaluate(async()=>{await loadProfiles();setDrawer(true)});return page}
const geometry=page=>page.evaluate(()=>{const d=document.querySelector('#connections-drawer'),r=d.getBoundingClientRect(),b=document.querySelector('#server-manager-body').getBoundingClientRect(),l=document.querySelector('#connection-groups').getBoundingClientRect(),z=Number(document.body.style.zoom)||1;const controls=[...document.querySelectorAll('.drawer-heading h2,.server-manager-tabs,#close-connections,.connection-search,#clear-selected-servers,#open-selected-servers')].filter(e=>e.getClientRects().length&&!e.closest('[hidden]')).map(e=>{const x=e.getBoundingClientRect();return {id:e.id||e.className,x:x.x,y:x.y,right:x.right,bottom:x.bottom,width:x.width,height:x.height}});return{top:(b.top-r.top)/z,body:b.height/z,list:l.height/z,height:r.height/z,ratio:b.height/r.height,overflow:d.scrollWidth>d.clientWidth+1,controls}});
try{
 if(base){const p=await load(true);before=await geometry(p);await p.screenshot({path:stage+'/manager-before.png'});await p.close()}
 const page=await load();
 const row=id=>`#server-folder-list [data-group-id="${id}"]`;
 const pick=id=>page.click(row(id)+' .server-folder-select');
 const expanded=id=>page.$eval(row(id)+' .server-folder-caret',e=>e.getAttribute('aria-expanded'));
 await page.click('#server-collapse-all');assert.equal(await page.$(row(groups[1].id)),null);assert.equal(await page.$(row(groups[5].id)),null);
 await pick(groups[0].id);assert.equal(await expanded(groups[0].id),'true');assert(await page.$(row(groups[1].id)));assert.equal(await page.$(row(groups[2].id)),null);assert.equal(await expanded(groups[4].id),'false');
 assert.equal(await page.$eval('#server-explorer-count',e=>e.textContent),'24 个连接');
 await pick(groups[0].id);assert.equal(await expanded(groups[0].id),'true');
 await page.click(row(groups[0].id)+' .server-folder-caret');assert.equal(await expanded(groups[0].id),'false');assert.equal(await page.$(row(groups[1].id)),null);assert.equal(await page.$eval('#server-explorer-count',e=>e.textContent),'24 个连接');
 await pick(groups[0].id);await pick(groups[1].id);assert(await page.$(row(groups[2].id)));assert.equal(await expanded(groups[2].id),'false');
 await page.focus(row(groups[2].id)+' .server-folder-select');await page.keyboard.press('Enter');assert.equal(await expanded(groups[2].id),'true');assert(await page.$(row(groups[3].id)));
 await page.keyboard.press('ArrowLeft');assert.equal(await expanded(groups[2].id),'false');await page.keyboard.press('ArrowRight');assert.equal(await expanded(groups[2].id),'true');
 await page.click('#server-collapse-all');await page.evaluate(id=>chooseServerFolder(id),groups[3].id);for(let i=0;i<3;i++)assert.equal(await expanded(groups[i].id),'true');assert.equal(await expanded(groups[4].id),'false');
 await page.evaluate(()=>DengPortablePreferences.flush());await page.reload({waitUntil:'networkidle0'});await page.evaluate(async()=>{await loadProfiles();setDrawer(true)});for(let i=0;i<3;i++)assert.equal(await expanded(groups[i].id),'true');assert.equal(await expanded(groups[4].id),'false');
 await page.click(row(groups[4].id)+' .server-group-more');assert.equal(await expanded(groups[4].id),'false');await page.evaluate(()=>closeServerGroupMenu());
 checks.push('Name click selects and expands; repeated click stays expanded; caret folds independently; ancestors revealed, siblings preserved; keyboard and persisted state');
 await pick(groups[0].id);await page.click('#server-include-children');assert.equal(await page.$$eval('#connection-groups .connection-card',es=>es.length),6);await page.click('#server-include-children');
 await page.type('#connection-search','192.0.2.24');assert.equal(await page.$$eval('#connection-groups .connection-card',es=>es.length),1);await pick(groups[0].id);assert.equal(await page.$eval('#connection-search',e=>e.value),'');
 await page.click(`[data-profile-id="${profiles[0].id}"] .connection-card`);await page.keyboard.down('Control');await page.click(`[data-profile-id="${profiles[1].id}"] .connection-card`);await page.keyboard.up('Control');assert.equal(await page.$eval('#open-selected-servers',e=>e.textContent),'打开所选 (2)');
 // Opening is intercepted so the UI test never contacts a server.
 await page.evaluate(()=>{window.qaOpened=[];connect=async id=>{window.qaOpened.push(id)}});await page.click('#open-selected-servers');await page.waitForFunction(()=>window.qaOpened.length===2);assert.deepEqual((await page.evaluate(()=>window.qaOpened)).sort(),profiles.slice(0,2).map(p=>p.id).sort());await page.evaluate(()=>setDrawer(true));
 await page.click('#server-tab-history');await page.waitForSelector('#server-manager-body.server-explorer-list-only');await page.click('#server-tab-trash');assert(await page.$eval('#server-selection-bar',e=>e.hidden));await page.click('#server-tab-servers');
 checks.push('Descendant filter and global search, two-server selection/open target, history/trash tabs remain usable');
 for(const [width,height,scale]of [[1440,1000,1],[1024,768,1],[768,560,1],[480,640,1],[1280,900,1.5],[1440,1000,2]]){
  await page.setViewport({width,height});await page.evaluate(scale=>{appearance.uiScale=scale;applyUIScale();window.dispatchEvent(new Event('resize'))},scale);await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
  await pick(groups[0].id);await page.click(`[data-profile-id="${profiles[0].id}"] .connection-card`);
  const g=await geometry(page);assert(!g.overflow,JSON.stringify(g));assert(g.ratio>=0.60,JSON.stringify(g));assert(g.list>=120,JSON.stringify(g));assert(g.top<=125,JSON.stringify(g));
  for(let i=0;i<g.controls.length;i++)for(let j=i+1;j<g.controls.length;j++){const a=g.controls[i],b=g.controls[j];assert(!(Math.min(a.right,b.right)-Math.max(a.x,b.x)>1&&Math.min(a.bottom,b.bottom)-Math.max(a.y,b.y)>1),`overlap ${a.id}/${b.id}`)}
  await page.click('#clear-selected-servers');assert.equal(await page.$eval('#open-selected-servers',e=>e.textContent),'打开所选 (0)');layouts.push({width,height,scale,...g});
 }
 await page.setViewport({width:1440,height:1000});await page.evaluate(()=>{appearance.uiScale=1;applyUIScale();window.dispatchEvent(new Event('resize'));document.documentElement.dataset.theme='light'});await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
 const after=await geometry(page);if(before){assert(after.top<before.top*.55,JSON.stringify({before,after}));assert(after.list>before.list+80)}
 await page.screenshot({path:stage+'/manager-after-light.png'});await page.evaluate(()=>document.documentElement.dataset.theme='dark');await page.evaluate(()=>new Promise(r=>setTimeout(r,350)));await page.screenshot({path:stage+'/manager-after-dark.png'});
 assert.deepEqual(errors,[]);fs.writeFileSync(stage+'/manager-browser.json',JSON.stringify({passed:true,checks,before,after,layouts,errors},null,2));console.log('PASS '+checks.join('\nPASS '));console.log(JSON.stringify({beforeTop:before?.top,afterTop:after.top,bodyRatio:after.ratio}));
}finally{await browser.close()}
