import assert from 'node:assert/strict';
import {readFile,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
const [stage,chrome,modulePath]=process.argv.slice(2);
const {default:puppeteer}=await import(modulePath);
const fixture=JSON.parse(await readFile(join(stage,'browser-fixture.json'),'utf8'));
const browser=await puppeteer.launch({executablePath:chrome,headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-background-networking']});
try {
 const page=await browser.newPage();await page.setViewport({width:1440,height:1000});const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(fixture.url,{waitUntil:'networkidle0'});
 await page.waitForFunction(()=>!document.querySelector('#startup-splash.is-running'));
 const result=await page.evaluate(()=>{
  const original=activeID, state={connected:true,stats:{processSample:{available:true,sampledAt:new Date().toISOString(),intervalMilliseconds:5000},processes:Array.from({length:20},(_,i)=>({pid:i+1,name:'process-'+(i+1),cpu:i+1,cpuReady:true,memory:1024*(21-i),memoryReady:true,memoryEstimated:true})),processMemoryTop:Array.from({length:5},(_,i)=>({pid:100+i,name:'memory-'+i,cpu:0,cpuReady:true,memory:1024*(100-i),memoryReady:true,memoryEstimated:true}))},processSort:{key:'cpu',ascending:true}};
  sessions.set('monitor-fixture',state); activeID='monitor-fixture';
  const rows=()=>[...document.querySelectorAll('#process-list tr')].map(r=>Number(r.dataset.pid));
  try {
   renderProcesses(state.stats);const cpu=rows();sortProcesses('memory');const memory=rows();
   state.stats={processSample:{available:false,error:'fixture timeout'}};renderProcesses(state.stats);const cached=rows(),cacheLabel=document.querySelector('#process-count').textContent;
   return {cpu,memory,cached,cacheLabel,alwaysVisible:document.querySelector('.process-details').tagName==='SECTION'&&document.querySelector('.process-table').getBoundingClientRect().height>0,estimated:document.querySelector('#process-list').textContent.includes('≈'),trafficWindow:trafficWindowMilliseconds};
  } finally {activeID=original; sessions.delete('monitor-fixture');}
 });
 assert.deepEqual(result.cpu,[20,19,18,17,16]);assert.deepEqual(result.memory,[100,101,102,103,104]);assert.deepEqual(result.cached,result.memory);assert.match(result.cacheLabel,/保留缓存/);assert(result.alwaysVisible);assert(result.estimated);assert.equal(result.trafficWindow,60000);
 await (await page.$('.process-details')).screenshot({path:join(stage,'process-top-five.png')});
 assert.deepEqual(errors,[]);await writeFile(join(stage,'process-ui-test.json'),JSON.stringify({passed:true,...result,errors},null,2));console.log('PASS: permanent CPU/memory top five, stale cache label, RSS estimate and 60-second window.');
} finally {await browser.close();}
