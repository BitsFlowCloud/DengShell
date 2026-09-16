import fs from 'node:fs';
import assert from 'node:assert/strict';
const [qaDir,chrome,modulePath]=process.argv.slice(2);
if(!qaDir||!chrome||!modulePath)throw Error('Usage: test-session-tabs-browser.mjs QA_DIR CHROME PUPPETEER_MODULE');
const {default:puppeteer}=await import(modulePath);
const stage=qaDir.replace(/\/$/,'')+'/';
const fixture=JSON.parse(fs.readFileSync(stage+'browser-fixture.json'));
const browser=await puppeteer.launch({executablePath:chrome,headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const errors=[],matrix=[]; let page;
try {
 page=await browser.newPage();page.on('pageerror',e=>errors.push(e.message));
 await page.setViewport({width:1440,height:1000});
 await page.goto(fixture.url,{waitUntil:'networkidle0'});
 await page.evaluate(async()=>{
   document.querySelectorAll('dialog[open]').forEach(d=>d.close());
   await chooseAppearance({startupAnimation:false,onboardingCompleted:true,uiScale:1});
   document.documentElement.classList.add('frameless');document.querySelector('#window-controls').hidden=false;
 });
 await page.waitForFunction(()=>!document.querySelector('#startup-splash.is-running'));
 assert.deepEqual(await page.$$eval('.brand button',bs=>bs.map(b=>b.id)),['settings-button','theme-toggle']);
 assert.equal(await page.$('.session-bar #settings-button'),null);
 const initialTheme=await page.evaluate(()=>document.documentElement.dataset.theme);
 await page.waitForFunction(()=>!document.documentElement.dataset.lightSwitch);await page.click('#theme-toggle');await page.waitForFunction(t=>document.documentElement.dataset.theme!==t&&!document.documentElement.dataset.lightSwitch,{},initialTheme);
 await page.click('#settings-button');await page.waitForSelector('#settings-menu:not([hidden])');
 assert.equal(await page.$eval('#settings-button',e=>e.getAttribute('aria-expanded')),'true');
 await page.keyboard.press('End');assert.equal(await page.evaluate(()=>document.activeElement.id),'about-software');
 await page.keyboard.press('Escape');assert.equal(await page.evaluate(()=>document.activeElement.id),'settings-button');
 assert.equal(await page.$eval('#settings-menu',e=>e.hidden),true);
 for(const [entry,dialog,close] of [['#manage-shell-fonts','#appearance-dialog','#close-appearance'],['#manage-ui-appearance','#ui-appearance-dialog','#close-ui-appearance'],['#help-button','#help-manual-dialog','#help-manual-done']]){
   await page.click('#settings-button');await page.click(entry);
   await page.waitForSelector(dialog+'[open]');assert.equal(await page.$eval('#settings-menu',e=>e.hidden),true);
   if(await page.$(close))await page.click(close);else await page.$eval(dialog,e=>e.close());
 }
 await page.evaluate(()=>chooseAppearance({monitorSide:'right',filesPosition:'top',terminalFontSize:19}));
 await page.click('#settings-button');await page.click('#reset-layout');
 await page.waitForFunction(()=>appearance.monitorSide==='left'&&appearance.filesPosition==='bottom'&&terminalFont===14);
 await page.evaluate(()=>{DengShellHelp.replayGuide()});
 for(let i=0;i<12;i++){
   await page.waitForFunction(n=>document.querySelector('#help-guide-step').textContent.startsWith(n+' / '),{},i+1);await new Promise(r=>setTimeout(r,80));
   const target=await page.$eval('#help-guide-target',e=>e.dataset.target);
   if(i===9)assert.equal(target,'settings-button');
   if(i===10)assert.equal(target,'theme-toggle');
   if(i===11)assert.equal(target,'settings-button');
   await page.click('#help-guide-next');
 }
 await page.waitForFunction(()=>!document.querySelector('#help-guide-dialog').open);
 // Create real tab markup without opening any network connection.
 await page.evaluate(()=>{
   window.qaSetTabs=count=>{
     sessions.clear();profiles=[];activeID=null;
     for(let i=0;i<count;i++){
       const id='header-qa-'+i;profiles.push({id,name:`服务器 ${i+1} · HK JP US`,host:'example.invalid',port:22,user:'qa'});
       sessions.set(id,{...makeSessionState({id,profileId:id,home:'/'}),profileSnapshot:profiles.at(-1),connected:false,ready:false,localOnly:true,term:{options:{},focus(){},refresh(){},dispose(){}},host:Object.assign(document.createElement('div'),{hidden:true})});
     }
     activeID=count?'header-qa-0':null;renderTabs();
   };
 });
 for(const count of [0,1,6,7,12,13,42]){
   await page.evaluate(n=>qaSetTabs(n),count);await new Promise(r=>setTimeout(r,100));
   const rows=await page.evaluate(()=>{
     const list=[...document.querySelectorAll('.session-tab')],positions=list.map(e=>({top:e.offsetTop,left:e.offsetLeft,right:e.offsetLeft+e.offsetWidth}));
     const rows=[...new Set(positions.map(p=>p.top))].map(top=>positions.filter(p=>p.top===top));
     return {counts:rows.map(r=>r.length),overlap:rows.some(row=>row.slice(1).some((p,i)=>p.left<row[i].right)),header:document.querySelector('.titlebar').offsetHeight,terminal:document.querySelector('.terminal-panel').offsetHeight};
   });
   assert(rows.counts.every(n=>n<=6)&&!rows.overlap,JSON.stringify(rows));
   if(count===6)assert.deepEqual(rows.counts,[6]);
   if(count===7)assert.deepEqual(rows.counts,[6,1]);
   if(count===13)assert.deepEqual(rows.counts,[6,6,1]);
   if(count===0||count===1)assert(rows.header<=44);
   assert(rows.terminal>200);
 }
 await page.evaluate(()=>qaSetTabs(7));await new Promise(r=>setTimeout(r,100));
 await page.click('[data-session-id="header-qa-6"] [role=tab]');
 assert.equal(await page.evaluate(()=>activeID),'header-qa-6');
 await page.click('[data-session-id="header-qa-6"] .tab-close');
 await page.waitForFunction(()=>document.querySelectorAll('.session-tab').length===6&&document.querySelector('.titlebar').offsetHeight<=44);
 await page.evaluate(()=>qaSetTabs(16));
 for(const [width,height,scale] of [[1440,1000,1],[1024,768,1],[800,600,1],[640,480,1],[480,360,1],[1440,1000,1.5],[1440,1000,2],[1024,768,1.5]]){
   await page.setViewport({width,height});await page.evaluate(s=>chooseAppearance({uiScale:s}),scale);
   await page.click('#settings-button');
   const metrics=await page.evaluate(()=>{
     const box=id=>{const r=document.querySelector(id).getBoundingClientRect();return {left:r.left,right:r.right,top:r.top,bottom:r.bottom,width:r.width}};
     const menu=box('#settings-menu'),tabs=box('#session-tabs'),brand=box('.brand'),bar=box('.session-bar'),controls=box('#window-controls');
     return {width:innerWidth,height:innerHeight,scale:effectiveScale,menu,tabs,brand,bar,controls,overflow:document.documentElement.scrollWidth>innerWidth+1};
   });
   assert(!metrics.overflow,JSON.stringify(metrics));assert(metrics.menu.left>=0&&metrics.menu.right<=width+1&&metrics.menu.bottom<=height+1,JSON.stringify(metrics));
   assert(metrics.brand.right<=metrics.bar.left+1,JSON.stringify(metrics));assert(metrics.tabs.width>40,JSON.stringify(metrics));assert(metrics.controls.right<=width+1,JSON.stringify(metrics));
   await page.keyboard.press('Escape');
   assert(await page.$eval('#session-tabs',e=>e.scrollWidth<=e.clientWidth+1));
   await page.$eval('#session-tabs',e=>e.scrollTop=e.scrollHeight);
   const gap=await page.evaluate(()=>document.querySelector('#theme-toggle').getBoundingClientRect().left-document.querySelector('#settings-button').getBoundingClientRect().right);assert(gap>=13*metrics.scale,JSON.stringify({gap,metrics}));matrix.push(metrics);
 }
 await page.setViewport({width:1440,height:1000});await page.evaluate(()=>chooseAppearance({uiScale:1}));
 for(const theme of ['light','dark']){
   await page.evaluate(t=>CloudShellTheme.set(t),theme);await page.waitForFunction(()=>!document.documentElement.dataset.lightSwitch);
   await page.screenshot({path:stage+`header-${theme}.png`});
   await page.click('#settings-button');await page.screenshot({path:stage+`settings-${theme}.png`});await page.keyboard.press('Escape');
 }
 assert.deepEqual(errors,[]);fs.writeFileSync(stage+'header-validation.json',JSON.stringify({passed:true,matrix,errors},null,2));
 console.log('PASS: left settings/theme, menu actions, keyboard, guide targets, 16 tabs, eight viewport/scale combinations.');
} catch(error) {await page.screenshot({path:stage+'failure.png'});console.log(JSON.stringify({errors,debug:await page.evaluate(()=>({theme:document.documentElement.dataset.theme,dialogs:[...document.querySelectorAll('dialog[open]')].map(d=>d.id),body:document.body.className,active:document.activeElement.id,settings:document.querySelector('#settings-menu').hidden}))}));throw error;} finally {await browser.close()}
