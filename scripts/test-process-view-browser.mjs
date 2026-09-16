import fs from 'node:fs';
import assert from 'node:assert/strict';
const [stage,chrome,modulePath]=process.argv.slice(2);
const {default:puppeteer}=await import(modulePath);
const fixture=JSON.parse(fs.readFileSync(stage+'/browser-fixture.json'));
const browser=await puppeteer.launch({executablePath:chrome,headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const errors=[];const results={};
try {
 const page=await browser.newPage();page.on('pageerror',e=>{errors.push(e.message);console.error('PAGE ERROR',e.message)});
 await page.setViewport({width:1440,height:1000});await page.goto(fixture.url,{waitUntil:'networkidle0'});
 await page.evaluate(async()=>{
  document.querySelectorAll('dialog[open]').forEach(d=>d.close());await chooseAppearance({startupAnimation:false,onboardingCompleted:true,uiScale:1});
  pollStats=()=>{};pollNetwork=()=>{};
  window.qaRequests=[];window.qaDelay=0;window.qaFailure=false;window.qaInFlight=0;window.qaPeak=0;
  const original=api;
  api=async(path,options)=>{
   if(!path.includes('/processes?'))return original(path,options);
   const url=new URL(path,location.href),id=url.pathname.split('/')[3],q=url.searchParams;qaRequests.push({id,search:q.get('search'),page:q.get('page'),sort:q.get('sort'),direction:q.get('direction')});
   qaInFlight++;qaPeak=Math.max(qaPeak,qaInFlight);
   try {
    if(qaDelay)await new Promise(r=>setTimeout(r,qaDelay));if(qaFailure)throw Error('fixture timeout');
    const total=50000,matched=q.get('search')==='none'?0:q.get('search')?1:total,size=Number(q.get('pageSize')),pages=Math.max(1,Math.ceil(matched/size)),page=Math.min(Number(q.get('page')),pages);
    const processes=Array.from({length:Math.min(size,Math.max(0,matched-(page-1)*size))},(_,i)=>({pid:(page-1)*size+i+1,name:id+' '+(q.get('search')||'worker')+' <img src=x onerror=alert(1)>',user:'root',userReady:true,cpu:1.5,cpuReady:true,memory:40960,memoryReady:true,memoryEstimated:true,state:'S'}));
    return {processes,total,matched,page,pageSize:size,pages,sample:{sampledAt:new Date().toISOString(),intervalMilliseconds:1000},nextSampleInMilliseconds:1000};
   }finally{qaInFlight--;}
  };
  for(const id of ['process-a','process-b']){
   const profile={id,name:id,host:'example.invalid',port:22,user:'qa'};profiles.push(profile);
   sessions.set(id,{...makeSessionState({id,profileId:id,home:'/'}),profileSnapshot:profile,localOnly:true,term:{options:{},focus(){},refresh(){},dispose(){}},host:document.createElement('div')});
  }
  activate('process-a');setDrawer(false);
 });
 await page.waitForFunction(()=>!document.querySelector('#startup-splash.is-running'));
 await page.click('.process-heading');await page.waitForSelector('#process-view-rows tr');
 assert.equal(await page.$$eval('#process-view-rows tr',r=>r.length),100);
 assert.equal(await page.$eval('#process-view-count',e=>e.textContent),'共 50000 个进程');
 assert.equal(await page.$eval('#workspace',e=>e.hidden),true);
 assert.equal(await page.$$eval('[data-process-session-id="process-a"]',r=>r.length),1);
 await page.click('.process-heading');assert.equal(await page.$$eval('[data-process-session-id="process-a"]',r=>r.length),1);
 assert.equal(await page.$('#process-view-rows img'),null);
 await page.click('#process-view-pause');
 await page.waitForFunction(()=>!document.querySelector('#process-view-refresh').disabled);
 let n=await page.evaluate(()=>qaRequests.length);await new Promise(r=>setTimeout(r,1500));assert.equal(await page.evaluate(()=>qaRequests.length),n);
 await page.click('th[data-sort=memory] button');await page.waitForFunction(()=>!document.querySelector('#process-view-refresh').disabled);
 assert.equal(await page.evaluate(()=>qaRequests.at(-1).sort),'memory');assert.equal(await page.$eval('th[data-sort=memory]',e=>e.getAttribute('aria-sort')),'descending');
 await page.click('#process-view-next');await page.waitForFunction(()=>document.querySelector('#process-view-page').textContent==='2 / 500');
 assert.equal(await page.$eval('#process-view-rows tr',e=>e.dataset.pid),'101');
 await page.select('#process-view-size','200');await page.waitForFunction(()=>document.querySelectorAll('#process-view-rows tr').length===200);
 await page.type('#process-view-search','none');await page.waitForFunction(()=>!document.querySelector('#process-view-empty').hidden);
 await page.$eval('#process-view-search',e=>{e.value='needle';e.dispatchEvent(new Event('input',{bubbles:true}))});await page.waitForFunction(()=>document.querySelectorAll('#process-view-rows tr').length===1);
 assert((await page.$eval('#process-view-rows',e=>e.textContent)).includes('needle'));
 // Late responses never cross server tabs, and rapid query changes serialize.
 await page.evaluate(()=>{qaDelay=450;qaPeak=0;});await page.click('#process-view-refresh');
 await page.evaluate(()=>{activate('process-b');DengProcessView.open();});await page.waitForFunction(()=>document.querySelector('#process-view-title').textContent.includes('process-b')&&document.querySelectorAll('#process-view-rows tr').length===100);
 assert(!(await page.$eval('#process-view-rows',e=>e.textContent)).includes('process-a'));
 await page.click('#process-view-pause');
 await page.evaluate(()=>{qaPeak=0;qaDelay=600;document.querySelector('#process-view-search').value='one';document.querySelector('#process-view-search').dispatchEvent(new Event('input'));});
 await new Promise(r=>setTimeout(r,300));
 await page.$eval('#process-view-search',e=>{e.value='last';e.dispatchEvent(new Event('input'))});
 await page.waitForFunction(()=>document.querySelector('#process-view-rows').textContent.includes('last')&&!document.querySelector('#process-view-refresh').disabled);
 assert.equal(await page.evaluate(()=>qaPeak),1);
 await page.evaluate(()=>{qaDelay=0;qaFailure=true});await page.click('#process-view-refresh');await page.waitForFunction(()=>document.querySelector('#process-view-status').textContent.includes('fixture timeout'));
 assert((await page.$eval('#process-view-rows',e=>e.textContent)).includes('last'));
 await page.evaluate(()=>{qaFailure=false;activate('process-a','processes')});
 assert.equal(await page.$eval('#process-view-search',e=>e.value),'needle');assert.equal(await page.$eval('#process-view-size',e=>e.value),'200');assert.equal(await page.$eval('#process-view-pause',e=>e.textContent),'恢复刷新');
 await page.evaluate(()=>{const input=document.querySelector('#process-view-search');input.value='pending';input.dispatchEvent(new Event('input'));activate('process-b','processes');activate('process-a','processes');});await page.waitForFunction(()=>document.querySelector('#process-view-rows').textContent.includes('pending'));
 await page.evaluate(()=>activate('process-a'));n=await page.evaluate(()=>qaRequests.length);await new Promise(r=>setTimeout(r,1500));assert.equal(await page.evaluate(()=>qaRequests.length),n);
 await page.evaluate(()=>activate('process-a','processes'));await page.click('#process-view-pause');
 await page.waitForFunction(()=>!document.querySelector('#process-view-refresh').disabled);
 await page.evaluate(()=>window.DengShellWindowHidden=true);n=await page.evaluate(()=>qaRequests.length);await new Promise(r=>setTimeout(r,1500));assert.equal(await page.evaluate(()=>qaRequests.length),n);
 await page.evaluate(()=>window.DengShellWindowHidden=false);
 await page.click('#process-view-pause');
 for(const theme of ['light','dark']){
  await page.evaluate(t=>CloudShellTheme.set(t),theme);await page.waitForFunction(()=>!document.documentElement.dataset.lightSwitch);
  await page.screenshot({path:stage+'/process-view-'+theme+'.png'});
 }
 await page.setViewport({width:800,height:600});
 assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
 assert(await page.$eval('.process-view-table',e=>e.clientHeight>150));
 await page.screenshot({path:stage+'/process-view-narrow.png'});
 await page.click('[data-process-session-id="process-a"] .tab-close');assert(await page.evaluate(()=>sessions.has('process-a')));assert(await page.$eval('#process-view',e=>e.hidden));
 await page.evaluate(()=>{activate('process-b','processes');dropSessionView('process-b');});
 assert.equal(await page.$('[data-process-session-id="process-b"]'),null);
 assert.deepEqual(errors,[]);
 Object.assign(results,{passed:true,fixtureProcesses:50000,maxDOMRows:200,searchSortPaging:true,perServerState:true,serializedRequests:true,lateRepliesIsolated:true,hiddenAndPausedRequestsStopped:true,failedRequestRetainsRows:true,closePreservesSSH:true,lightDarkNarrow:true});
 fs.writeFileSync(stage+'/process-browser.json',JSON.stringify(results,null,2));console.log(results);
}finally{await browser.close()}
