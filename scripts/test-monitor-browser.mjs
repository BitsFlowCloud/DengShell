import assert from 'node:assert/strict';
import {readFile,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
const [stage,chrome,modulePath]=process.argv.slice(2);
const {default:puppeteer}=await import(modulePath);
const fixture=JSON.parse(await readFile(join(stage,'browser-fixture.json'),'utf8'));
const browser=await puppeteer.launch({executablePath:chrome,headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-background-networking']});
try {
 const page=await browser.newPage();await page.setViewport({width:1440,height:1000});await page.emulateMediaFeatures([{name:'prefers-reduced-motion',value:'reduce'}]);const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(fixture.url,{waitUntil:'networkidle0'});
 await page.waitForFunction(()=>!document.querySelector('#startup-splash.is-running'));
 const result=await page.evaluate(()=>{
  const original=activeID, state={connected:true,stats:{processSample:{available:true,sampledAt:new Date().toISOString(),intervalMilliseconds:5000},processes:Array.from({length:20},(_,i)=>({pid:i+1,name:'process-'+(i+1),cpu:i+1,cpuReady:true,memory:1024*(21-i),memoryReady:true,memoryEstimated:true})),processMemoryTop:Array.from({length:5},(_,i)=>({pid:100+i,name:'memory-'+i,cpu:0,cpuReady:true,memory:1024*(100-i),memoryReady:true,memoryEstimated:true}))},processSort:{key:'cpu',ascending:true}};
  sessions.set('monitor-fixture',state); activeID='monitor-fixture';
  const rows=()=>[...document.querySelectorAll('#process-list tr')].map(r=>Number(r.dataset.pid));
  try {
   renderProcesses(state.stats);const cpu=rows();sortProcesses('memory');const memory=rows();
   state.stats={processSample:{available:false,error:'fixture timeout'}};renderProcesses(state.stats);const cached=rows(),cacheLabel=document.querySelector('#process-count').textContent;
   return {cpu,memory,cached,cacheLabel,alwaysVisible:document.querySelector('.process-details').tagName==='SECTION'&&document.querySelector('.process-table').getBoundingClientRect().height>0,memoryCells:[...document.querySelectorAll('#process-list tr')].map(row=>row.cells[0].textContent),trafficWindow:trafficWindowMilliseconds};
  } finally {activeID=original; sessions.delete('monitor-fixture');}
 });
 assert.deepEqual(result.cpu,[20,19,18,17,16]);assert.deepEqual(result.memory,[100,101,102,103,104]);assert.deepEqual(result.cached,result.memory);assert.match(result.cacheLabel,/保留缓存/);assert(result.alwaysVisible);assert.deepEqual(result.memoryCells,Array(5).fill('—'),'estimated memory must not be presented as exact RSS');assert.equal(result.trafficWindow,60000);
 await (await page.$('.process-details')).screenshot({path:join(stage,'process-top-five.png')});
 // Consume a snapshot from the Go parser regression, then check the actual
 // summary in both themes, tab changes, missing data and unchanged I/O rates.
 const diskFixture=await readFile(join(stage,'disk-fixture.json'),'utf8').then(JSON.parse).catch(error=>{if(error.code==='ENOENT')return null;throw error;});
 const diskChecks=[];
 if(diskFixture){
  await page.evaluate(()=>{document.querySelectorAll('dialog[open]').forEach(d=>d.close());setDrawer(false);});
  for(const theme of ['dark','light']){
   const check=await page.evaluate(({stats,theme})=>{
    CloudShellTheme.set(theme);
    stats.sampleReady=true;stats.diskRead=326.3*1024;stats.diskWrite=9.2*1024**2;
    const text=id=>document.querySelector('#disk-'+id).textContent;
    renderMonitor(stats);
    const totals={used:text('used'),available:text('available'),total:text('total'),read:text('read'),write:text('write'),tooltip:document.querySelector('.disk-summary').title};
    const expected=['used','available','total'].map(key=>prettySize(stats.disks.reduce((sum,disk)=>sum+disk[key],0)));
    renderMonitor({...stats,disks:[{path:'/',used:1024**3,total:10*1024**3,available:9*1024**3}]});
    const otherTab=text('total');renderMonitor(null);const empty=['used','available','total','read','write'].map(text);
    renderMonitor(stats);
    return {theme,totals,expected,otherTab,empty};
   },{stats:diskFixture,theme});
   assert.deepEqual([check.totals.used,check.totals.available,check.totals.total],check.expected);
   assert.match(check.totals.total,/TB$/);assert.match(check.totals.tooltip,/3 个/);assert.match(check.totals.tooltip,/\/home/);assert.match(check.totals.tooltip,/\/boot\/efi/);
   assert.equal(check.totals.read,'326.3 KB/s');assert.equal(check.totals.write,'9.2 MB/s');assert.equal(check.otherTab,'10.0 GB');assert.deepEqual(check.empty,Array(5).fill('—'));
   await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
   await (await page.$('.disk-summary')).screenshot({path:join(stage,'disk-capacity-'+theme+'.png')});
   diskChecks.push(check);
  }
 }
 assert.deepEqual(errors,[]);await writeFile(join(stage,'process-ui-test.json'),JSON.stringify({passed:true,...result,diskChecks,errors},null,2));console.log('PASS: permanent CPU/memory top five, stale cache label, unavailable exact RSS, 60-second window'+(diskChecks.length?', disk capacity sums in both themes and independent I/O rates.':'.'));
} finally {await browser.close();}
