import fs from 'node:fs';
import assert from 'node:assert/strict';
const [qaDir,chrome,modulePath]=process.argv.slice(2);
if(!qaDir||!chrome||!modulePath)throw Error('Usage: test-ui-refinements-browser.mjs QA_DIR CHROME PUPPETEER_MODULE');
const {default:puppeteer}=await import(modulePath);
const stage=qaDir.replace(/\/$/,'')+'/',fixture=JSON.parse(fs.readFileSync(stage+'browser-fixture.json'));
const browser=await puppeteer.launch({executablePath:chrome,headless:true,userDataDir:stage+'ui-regression-profile',args:['--no-sandbox','--disable-dev-shm-usage']});
const errors=[],results=[];
try {
 const page=await browser.newPage();page.on('pageerror',e=>errors.push(e.message));await page.setViewport({width:1440,height:1000});await page.goto(fixture.url,{waitUntil:'networkidle0'});
 await page.evaluate(async()=>{await chooseAppearance({startupAnimation:false,onboardingCompleted:true,fontId:'builtin:jetbrains-mono',uiFontId:'builtin:ui-ibm-plex-sans-sc'});});
 for(const scale of [1,1.5,2]) {
  await page.evaluate(async scale=>{await chooseAppearance({uiScale:scale});await openAppearance('font')},scale);
  await page.waitForFunction(()=>document.querySelector('#appearance-dialog').open);
  const metrics=await page.evaluate(()=>{
   const d=document.querySelector('#appearance-dialog'),entry=document.querySelector('#shell-font-online-entry'),b=document.querySelector('#online-shell-fonts'),r=entry.getBoundingClientRect(),br=b.getBoundingClientRect();
   return {scale:effectiveScale,button:b.textContent,centerError:Math.abs((r.left+r.right-br.left-br.right)/2),cards:[...document.querySelectorAll('#asset-list .asset-card')].map(c=>({height:c.offsetHeight,overflow:c.scrollWidth>c.clientWidth+1})),viewportOverflow:document.documentElement.scrollWidth>innerWidth+1};
  });
  assert.equal(metrics.button,'在线字体');assert(metrics.centerError<1);assert(metrics.cards.every(c=>c.height<145&&!c.overflow));assert(!metrics.viewportOverflow);results.push(metrics);
 }
 await page.evaluate(async()=>{await chooseAppearance({uiScale:1});await openAppearance('font')});
 const card='#asset-list [data-font-id="builtin:jetbrains-mono"]';
 if(!await page.$eval(card+' input[type=checkbox]',e=>e.checked))await page.click(card+' .font-card-bold');assert.equal(await page.$eval(card+' .font-sample',e=>getComputedStyle(e).fontWeight),'700');
 await page.click(card+' .font-card-color-button');await page.waitForSelector('#font-color-dialog[open]');
 await page.evaluate(()=>{const e=document.querySelector('#font-color-dialog input[type=color]');e.value='#abc123';e.dispatchEvent(new Event('input',{bubbles:true}));});
 assert.equal(await page.$eval(card+' .font-sample',e=>getComputedStyle(e).color),'rgb(171, 193, 35)');
 await page.evaluate(()=>document.querySelector('#font-color-dialog').close());await page.screenshot({path:stage+'compact-fonts-dark.png'});
 await page.evaluate(()=>DengUIAppearance.open());assert.equal(await page.$eval('#ui-font-status',e=>e.hidden),true);
 const centered=await page.$eval('#online-ui-fonts',e=>{const a=e.getBoundingClientRect(),b=e.parentElement.getBoundingClientRect();return Math.abs(a.left+a.right-b.left-b.right)/2});assert(centered<1);
 await page.evaluate(()=>document.querySelector('#ui-appearance-dialog').close());
 // Same behavior with a popover top layer and the older WebKit fallback.
 for(const fallback of [false,true]) {
  const original=await page.evaluate(fallback=>{
   const t=document.querySelector('#toast');if(fallback){if(t.matches(':popover-open'))t.hidePopover();t.removeAttribute('popover');t.showPopover=undefined;t.hidePopover=undefined;}
   const d=document.querySelector('#connection-dialog');d.showModal();const input=[...d.querySelectorAll('input')].find(e=>e.type!=='hidden'&&e.offsetWidth>0);input.focus();toast('连接参数错误，请检查后重试');return input.id;
  },fallback);
  await new Promise(r=>setTimeout(r,60));
  assert.equal(await page.$eval('#toast',e=>e.parentElement.id),'connection-dialog');assert.equal(await page.evaluate(()=>document.activeElement.id),original);
  await page.evaluate(()=>{ask({title:'第二层确认',description:'界面回归测试'});});await page.waitForSelector('#action-dialog[open]');
  await new Promise(r=>setTimeout(r,60));assert.equal(await page.$eval('#toast',e=>e.parentElement.id),'action-dialog');
  assert(await page.$eval('#toast',e=>{const r=e.getBoundingClientRect();return r.width>0&&r.height>0&&r.left>=0&&r.right<=innerWidth+1&&r.top>=0&&r.bottom<=innerHeight}));
  await page.screenshot({path:stage+`modal-toast-${fallback?'fallback':'popover'}.png`});
  await page.click('#action-cancel');await page.waitForFunction(()=>!document.querySelector('#action-dialog').open);
  await new Promise(r=>setTimeout(r,60));assert.equal(await page.$eval('#toast',e=>e.parentElement.id),'connection-dialog');
  await page.evaluate(()=>document.querySelector('#connection-dialog').close());await new Promise(r=>setTimeout(r,60));assert.equal(await page.$eval('#toast',e=>e.parentElement.tagName),'BODY');
 }
 // Five rows, high CPU, exact RSS and long process names without inner scroll.
 const table=await page.evaluate(()=>{
  const now=new Date().toISOString(),processes=Array.from({length:5},(_,i)=>({pid:i+1,name:'qemu-system-x86',cpu:119.8765-i,cpuReady:true,memory:Math.round((7.8125-i)*1024**3),memoryReady:true,memoryEstimated:false,memorySource:'smaps_rollup',memorySampledAt:now}));
  const state={id:'ui-only',connected:true,ready:false,host:Object.assign(document.createElement('div'),{hidden:true})};sessions.set(state.id,state);activeID=state.id;
  renderProcesses({processSample:{available:true,sampledAt:now,readable:1000,visible:1000},processes,processMemoryTop:processes});
  const t=document.querySelector('.process-table');return {rows:document.querySelectorAll('#process-list tr').length,scroll:t.scrollHeight-t.clientHeight,horizontal:t.scrollWidth-t.clientWidth,text:document.querySelector('#process-list').textContent,tooltip:document.querySelector('#process-list td').title};
 });
 assert.equal(table.rows,5);assert(table.scroll<=1&&table.horizontal<=1,JSON.stringify(table));assert(table.text.includes('7.81 GiB')&&table.text.includes('119.88%')&&!table.text.includes('≈'));assert(table.tooltip.includes('字节')&&table.tooltip.includes('smaps_rollup'));
 await page.screenshot({path:stage+'process-precise-dark.png'});
 await page.evaluate(()=>{sessions.delete('ui-only');activeID=null});await page.setViewport({width:800,height:600});await page.evaluate(()=>openAppearance('font'));await page.screenshot({path:stage+'compact-fonts-small.png'});
 assert.deepEqual(errors,[]);fs.writeFileSync(stage+'ui-regression.json',JSON.stringify({passed:true,scales:results,table,toastPopoverAndFallback:true,errors},null,2));console.log('PASS compact fonts, centered entries, immediate style, exact process text, five rows without scroll, nested modal toast and fallback.');
}finally{await browser.close()}
