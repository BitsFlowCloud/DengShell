// Run only against a disposable localhost backend and SSH/SFTP container.
// This exercises the real button, layout, xterm, backend resize relay and SFTP.
import fs from 'node:fs';
import path from 'node:path';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
const [stage, chrome, modulePath] = process.argv.slice(2);
const {default:puppeteer}=await import(modulePath);
const fixture=JSON.parse(fs.readFileSync(path.join(stage,'browser-fixture.json')));
const origin=new URL(fixture.url).origin;
assert.equal(new URL(origin).hostname,'127.0.0.1');
assert.match(fixture.container,/^dengshell-sftp-(?:layout|right)-qa-/);
const headers={'Content-Type':'application/json','X-CloudShell-Token':fixture.token};
async function api(route,body,method=body===undefined?'GET':'POST') {
 const r=await fetch(new URL(route,origin),{method,headers,body:body===undefined?undefined:JSON.stringify(body)});
 const data=await r.json();assert(r.ok,`${route}: ${JSON.stringify(data)}`);return data;
}
await api('/api/appearance/patch',{onboardingCompleted:true,startupAnimation:false,uiScale:1,filesPosition:'bottom'});
const key=await api('/api/keys',{name:'SFTP right isolated key',sourcePath:fixture.key});
const profile=await api('/api/profiles',{name:'SFTP right isolated SSH',host:'127.0.0.1',port:fixture.port,user:'bitsflow',auth:'key',keyId:key.id});
const trust=await api('/api/sessions',{profileId:profile.id,hostKeyApproval:{host:'127.0.0.1:'+fixture.port,fingerprint:fixture.fingerprint}});
await api('/api/sessions/'+trust.id,undefined,'DELETE');
const browser=await puppeteer.launch({executablePath:chrome,headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const checks=[],failures=[],errors=[],measurements=[];
const pause=ms=>new Promise(r=>setTimeout(r,ms));
let page,sequence=0,completed=false;
async function check(name,test) {
 try{await test();checks.push(name);}catch(e){failures.push({name,error:e.stack||e.message});console.log('CHECK FAILED:',name,e.message);if(failures.length<=5)await page?.screenshot({path:path.join(stage,'sftp-right-failure-'+failures.length+'.png')}).catch(()=>{});}
}
async function settle(){await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));await pause(120);}
async function snapshot(){return page.evaluate(()=>{
 const rect=e=>{const r=e.getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,height:r.height,bottom:r.bottom,right:r.right};};
 const s=current(),x=s?.host.querySelector('.xterm'),style=x&&getComputedStyle(x);
 return {workspace:rect($('#workspace')),terminal:rect($('.terminal-panel')),files:rect($('#files-panel')),splitter:rect($('#files-splitter')),
  hidden:$('#files-panel').hidden,splitterHidden:$('#files-splitter').hidden,expanded:$('#toggle-sftp').getAttribute('aria-expanded'),position:appearance.filesPosition,
  orientation:$('#files-splitter').getAttribute('aria-orientation'),cursor:getComputedStyle($('#files-splitter')).cursor,
  scale:effectiveScale,session:s?.id,rows:s?.term.rows,cols:s?.term.cols,hostHeight:s?.host.clientHeight,hostWidth:s?.host.clientWidth,
  cellHeight:s?.term._core._renderService.dimensions.css.cell.height,padding:style?parseFloat(style.paddingTop)+parseFloat(style.paddingBottom):0,
  overflowX:$('#workspace').scrollWidth>$('#workspace').clientWidth+1,overflowY:$('#workspace').scrollHeight>$('#workspace').clientHeight+1};
});}
async function geometry(label,hidden=false){
 const s=await snapshot();measurements.push({label,...s});
 assert.equal(s.hidden,hidden);assert.equal(s.splitterHidden,hidden);assert.equal(s.expanded,String(!hidden));
 assert.equal(s.overflowX,false,'workspace has horizontal overflow');assert.equal(s.overflowY,false,'workspace has vertical overflow');
 const eq=(a,b,m)=>assert(Math.abs(a-b)<2,m+`: ${a} / ${b}`);
 if(hidden){eq(s.terminal.x,s.workspace.x,'hidden left edge');eq(s.terminal.y,s.workspace.y,'hidden top edge');eq(s.terminal.right,s.workspace.right,'hidden right edge');eq(s.terminal.bottom,s.workspace.bottom,'hidden bottom edge');}
 else if(s.position==='right'){
  assert(s.files.x>=s.terminal.right-1,'files must be to the right');eq(s.terminal.y,s.workspace.y,'right terminal top');eq(s.files.y,s.workspace.y,'right files top');eq(s.files.bottom,s.workspace.bottom,'right files bottom');eq(s.terminal.bottom,s.workspace.bottom,'right terminal bottom');eq(s.files.right,s.workspace.right,'right files edge');
  assert.equal(s.orientation,'vertical');assert.equal(s.cursor,'col-resize');assert(s.terminal.width>100&&s.files.width>100,'usable columns');
 }else{
  eq(s.terminal.x,s.workspace.x,'stacked terminal left');eq(s.files.x,s.workspace.x,'stacked files left');eq(s.terminal.right,s.workspace.right,'stacked terminal right');eq(s.files.right,s.workspace.right,'stacked files right');
  if(s.position==='top'){eq(s.files.y,s.workspace.y,'top files start');eq(s.terminal.bottom,s.workspace.bottom,'top terminal end');assert(s.terminal.y>=s.files.bottom-1);}
  else{eq(s.terminal.y,s.workspace.y,'bottom terminal start');eq(s.files.bottom,s.workspace.bottom,'bottom files end');assert(s.files.y>=s.terminal.bottom-1);}
  assert.equal(s.orientation,'horizontal');assert.equal(s.cursor,'row-resize');
 }
 if(s.rows){const available=s.hostHeight-s.padding;assert(s.rows*s.cellHeight<=available+2,'xterm too tall');assert(available-s.rows*s.cellHeight<s.cellHeight+2,'xterm leaves unused rows');}
 return s;
}
async function button(label){
 const b=await page.evaluate(()=>{
  const e=$('#toggle-files-position'),tip=$('.files-tip'),panel=$('#files-panel'),r=e.getBoundingClientRect(),t=tip.getBoundingClientRect(),p=panel.getBoundingClientRect();
  return {tag:e.tagName,type:e.type,disabled:e.disabled,tabIndex:e.tabIndex,label:e.getAttribute('aria-label')||e.textContent||e.title,
   before:!!(e.compareDocumentPosition(tip)&Node.DOCUMENT_POSITION_FOLLOWING),visible:!!e.getClientRects().length,
   inside:r.left>=p.left-1&&r.right<=p.right+1&&r.top>=p.top-1&&r.bottom<=p.bottom+1,
   hit:e.contains(document.elementFromPoint(r.left+r.width/2,r.top+r.height/2)),tipVisible:!!tip.getClientRects().length,
   leftOfTip:r.right<=t.left+2||r.top<t.top-2,width:r.width,height:r.height};
 });
 measurements.push({label,button:b});assert.equal(b.tag,'BUTTON');assert.equal(b.type,'button');assert(!b.disabled&&b.tabIndex>=0&&b.label.trim());assert(b.before,'toggle must precede upload hint');assert(b.visible&&b.inside&&b.hit,'toggle must be visible and reachable');assert(b.width>=20&&b.height>=20,'toggle target too small');if(b.tipVisible)assert(b.leftOfTip,'toggle must be left of upload hint');
}
async function toolbar(label){
 const controls=await page.evaluate(()=>{
  const panel=$('#files-panel').getBoundingClientRect();
  return ['parent-directory','directory-favorites-button','path-input','path-history-button','refresh-files','mkdir','choose-files'].map(id=>{
   const e=$('#'+id),r=e.getBoundingClientRect();return {id,visible:!!e.getClientRects().length,inside:r.left>=panel.left-1&&r.right<=panel.right+1&&r.top>=panel.top-1&&r.bottom<=panel.bottom+1,hit:e.contains(document.elementFromPoint(r.left+r.width/2,r.top+r.height/2)),width:r.width/effectiveScale,height:r.height/effectiveScale};
  });
 });
 measurements.push({label,toolbar:controls});for(const c of controls)assert(c.visible&&c.inside&&c.hit&&c.width>=15&&c.height>=20,'toolbar control unreachable: '+JSON.stringify(c));
 await page.click('#choose-files');assert.equal(await page.$eval('#upload-menu',e=>e.hidden),false);await page.click('#choose-files');
}
async function realPTY(label){
 await page.waitForFunction(()=>current()?.ready&&current()?.shellIntegration?.atPrompt,{timeout:20000});
 const filename='/home/bitsflow/right-pty-'+(++sequence)+'.txt';await page.evaluate(filename=>pasteTerminalText(current(),'stty size > '+filename,{execute:true}),filename);
 let value;for(let i=0;i<100;i++){try{value=execFileSync('docker',['exec',fixture.container,'cat',filename],{encoding:'utf8',stdio:['ignore','pipe','ignore']}).trim();if(value)break;}catch{}await pause(50);}
 assert(value,'missing remote stty output');const [rows,cols]=value.split(/\s+/).map(Number),s=await snapshot();assert.deepEqual({rows,cols},{rows:s.rows,cols:s.cols},label+' PTY mismatch');measurements.push({label,remotePTY:{rows,cols},session:s.session});
}
async function connect(){const id=await page.evaluate(async id=>(await connect(id)).id,profile.id);await page.waitForFunction(id=>sessions.get(id)?.ready&&sessions.get(id)?.shellIntegration?.atPrompt&&filesUsable(sessions.get(id)),{timeout:30000},id);await settle();return id;}
async function show(){if(await page.$eval('#files-panel',e=>e.hidden))await page.click('#toggle-sftp');await settle();}
async function hide(){if(!await page.$eval('#files-panel',e=>e.hidden))await page.click('#toggle-sftp');await settle();}
async function position(wanted){await show();const old=await page.evaluate(()=>appearance.filesPosition);if(old!==wanted){await page.click('#toggle-files-position');await page.waitForFunction(wanted=>appearance.filesPosition===wanted,{},wanted);await settle();}assert.equal(await page.evaluate(()=>appearance.filesPosition),wanted);}
async function drag(axis,delta){const s=(await snapshot()).splitter;const x=s.x+s.width/2,y=s.y+s.height/2;await page.mouse.move(x,y);await page.mouse.down();await page.mouse.move(x+(axis==='x'?delta:0),y+(axis==='y'?delta:0),{steps:6});await page.mouse.up();await settle();}
async function reload(){await page.evaluate(async()=>{await DengPortablePreferences.flush();for(const id of [...sessions.keys()])await closeSession(id);});await page.reload({waitUntil:'networkidle0'});await page.evaluate(()=>{document.querySelectorAll('dialog[open]').forEach(d=>d.close());setDrawer(false);});await settle();}
try{
 page=await browser.newPage();page.on('pageerror',e=>errors.push(e.message));await page.setViewport({width:1600,height:1000,deviceScaleFactor:1});await page.emulateMediaFeatures([{name:'prefers-reduced-motion',value:'reduce'}]);
 await page.setRequestInterception(true);page.on('request',req=>{const u=new URL(req.url());if(/^https?:/.test(u.protocol)&&u.origin!==origin)req.abort();else req.continue();});
 await page.goto(fixture.url,{waitUntil:'networkidle0'});await page.evaluate(()=>{document.querySelectorAll('dialog[open]').forEach(d=>d.close());setDrawer(false);});let first=await connect();
 await check('Actual placement button is immediately before upload hint and reachable',async()=>{await show();await button('initial bottom button');assert.equal(await page.$eval('#toggle-files-position',e=>e.nextElementSibling?.classList.contains('files-tip')),true);});
 await check('Click switches bottom to right; keyboard switches back',async()=>{
  const old=await geometry('initial bottom');await page.click('#toggle-files-position');await page.waitForFunction(()=>appearance.filesPosition==='right');await settle();const right=await geometry('clicked right');await button('right button');assert(right.cols<old.cols&&right.rows>old.rows,'right position should trade columns for rows');await realPTY('clicked right');
  await page.focus('#toggle-files-position');await page.keyboard.press('Enter');await page.waitForFunction(()=>appearance.filesPosition==='bottom');await settle();await geometry('keyboard bottom');await realPTY('keyboard bottom');
 });
 await check('Independent bottom height and right width survive pointer/keyboard resize and toggles',async()=>{
  await position('bottom');await drag('y',-48);const bottom=await geometry('resized bottom');
  await position('right');const before=await snapshot();await drag('x',-64);const dragged=await geometry('dragged right');assert(dragged.files.width>before.files.width+40);
  await page.focus('#files-splitter');await page.keyboard.press('ArrowLeft');await settle();const wider=await geometry('keyboard wider');assert(wider.files.width>dragged.files.width+10);
  await page.keyboard.press('ArrowRight');await settle();const restored=await geometry('keyboard narrower');assert(Math.abs(restored.files.width-dragged.files.width)<2);await realPTY('resized right');
  for(let i=0;i<3;i++){await position('bottom');assert(Math.abs((await snapshot()).files.height-bottom.files.height)<2,'bottom height lost');await position('right');assert(Math.abs((await snapshot()).files.width-dragged.files.width)<2,'right width lost');}
 });
 for(const place of ['bottom','right']){
  await position(place);
  await check(`${place}: repeated hide fills workspace and restore retains dimensions`,async()=>{
   const old=await snapshot();for(let i=0;i<3;i++){await hide();const hidden=await geometry(place+' hidden '+i,true);assert(place==='right'?hidden.cols>old.cols:hidden.rows>old.rows);await show();const restored=await geometry(place+' restored '+i);assert(Math.abs(restored.files[place==='right'?'width':'height']-old.files[place==='right'?'width':'height'])<2);}await realPTY(place+' restored');
  });
  for(const viewport of [{width:1600,height:1000},{width:960,height:800},{width:640,height:800}]){
   await page.setViewport({...viewport,deviceScaleFactor:1});await settle();await check(`${place}: window ${viewport.width}`,async()=>{await geometry(place+' viewport '+viewport.width);await button(place+' button '+viewport.width);await toolbar(place+' toolbar '+viewport.width);await realPTY(place+' viewport '+viewport.width);await hide();await geometry(place+' hidden viewport '+viewport.width,true);await show();});
  }
  await page.setViewport({width:1600,height:1000,deviceScaleFactor:1});
  for(const scale of [.75,1,1.25,1.5]){
   await page.evaluate(scale=>{appearance.uiScale=scale;applyUIScale();},scale);await settle();await check(`${place}: UI scale ${scale}`,async()=>{await geometry(place+' scale '+scale);await button(place+' scale button '+scale);await toolbar(place+' scale toolbar '+scale);await realPTY(place+' scale '+scale);await hide();await geometry(place+' hidden scale '+scale,true);await show();});
  }
  await page.evaluate(()=>{appearance.uiScale=1;applyUIScale();});await settle();
  await check(`${place}: terminal font change refits PTY`,async()=>{const old=await snapshot();for(let i=0;i<6;i++)await page.click('#font-up');await settle();const large=await geometry(place+' font enlarged');assert(large.cols<old.cols&&large.rows<old.rows);await realPTY(place+' large font');for(let i=0;i<6;i++)await page.click('#font-down');await settle();});
  for(const pane of ['files','transfers','commands','common-apps'])await check(`${place}: ${pane} tab keeps placement toggle reachable`,async()=>{
   await show();await page.click(`.file-tab[data-pane="${pane}"]`);await settle();assert.equal(await page.$eval('.file-tab.active',e=>e.dataset.pane),pane);await geometry(place+' pane '+pane);await button(place+' pane button '+pane);
   await page.click('#toggle-files-position');await settle();assert.equal(await page.$eval('.file-tab.active',e=>e.dataset.pane),pane);await geometry(place+' switched pane '+pane);await page.click('#toggle-files-position');await settle();await hide();await geometry(place+' hidden pane '+pane,true);await show();
  });
 }
 await position('right');await page.click('.file-tab[data-pane="files"]');await settle();
 await check('DPR 2 and dark theme retain right button and terminal geometry',async()=>{await page.setViewport({width:1600,height:1000,deviceScaleFactor:2});await page.evaluate(()=>document.documentElement.dataset.theme='dark');await settle();await geometry('right dark DPR2');await button('right dark DPR2');await toolbar('right dark DPR2 toolbar');await realPTY('right dark DPR2');await page.screenshot({path:path.join(stage,'sftp-right-dark-dpr2.png')});await page.setViewport({width:1600,height:1000,deviceScaleFactor:1});await page.evaluate(()=>document.documentElement.dataset.theme='light');await settle();});
 const second=await connect();
 await check('Two real SSH sessions keep right layout and receive current rows/columns',async()=>{await geometry('second right');await realPTY('second right');await hide();await geometry('second hidden',true);await realPTY('second hidden');await page.click(`.session-tab[data-session-id="${first}"] > button[role="tab"]`);await settle();await geometry('first hidden activated',true);await realPTY('first hidden activated');await show();await geometry('first right restored');await realPTY('first right restored');await page.click(`.session-tab[data-session-id="${second}"] > button[role="tab"]`);await settle();await geometry('second right activated');await realPTY('second right activated');});
 await check('Real SFTP upload completes while hidden; transfer tab opens at right',async()=>{
  await hide();await page.evaluate(async()=>queueFiles([{file:new File(['right-layout real SFTP\n'],'right-layout-upload.txt'),relativePath:'right-layout-upload.txt'}],current(),'/home/bitsflow'));await page.waitForFunction(()=>[...localTasks.values()].some(t=>t.name==='right-layout-upload.txt'&&t.status==='done'),{timeout:20000});assert.equal(execFileSync('docker',['exec',fixture.container,'cat','/home/bitsflow/right-layout-upload.txt'],{encoding:'utf8'}),'right-layout real SFTP\n');await geometry('after hidden upload',true);await show();await page.click('.file-tab[data-pane="transfers"]');await settle();await geometry('transfer open right');await button('transfer open right');
 });
 for(const place of ['bottom','right'])await check(`${place}: persisted shown and hidden placement after full refresh`,async()=>{
  await position(place);const original=await snapshot();await page.evaluate(()=>DengPortablePreferences.flush());await reload();assert.equal(await page.evaluate(()=>appearance.filesPosition),place);first=await connect();const shown=await geometry(place+' refresh shown');assert(Math.abs(shown.files[place==='right'?'width':'height']-original.files[place==='right'?'width':'height'])<2);await realPTY(place+' refresh shown');await hide();await reload();assert.equal(await page.evaluate(()=>appearance.filesPosition),place);await geometry(place+' refresh hidden before SSH',true);first=await connect();await geometry(place+' refresh hidden SSH',true);await realPTY(place+' refresh hidden');await show();
 });
 await check('Legacy top preference survives reload; hide and button transition remain compatible',async()=>{
  await api('/api/appearance/patch',{filesPosition:'top'});await reload();assert.equal(await page.evaluate(()=>appearance.filesPosition),'top');first=await connect();await geometry('legacy top shown');await button('legacy top button');await hide();await geometry('legacy top hidden',true);await realPTY('legacy top hidden');await show();await page.click('#toggle-files-position');await settle();assert(['bottom','right'].includes(await page.evaluate(()=>appearance.filesPosition)));await geometry('legacy top switched by button');await realPTY('legacy top switched');
 });
 await position('right');await page.click('.file-tab[data-pane="files"]');await settle();await page.screenshot({path:path.join(stage,'sftp-right-final.png')});await position('bottom');await page.screenshot({path:path.join(stage,'sftp-bottom-final.png')});
 assert.deepEqual(failures,[]);assert.deepEqual(errors,[]);completed=true;
}finally{
 const result={passed:completed&&failures.length===0&&errors.length===0,completed,checks,failures,errors,measurements};fs.writeFileSync(path.join(stage,'sftp-right-results.json'),JSON.stringify(result,null,2));console.log(JSON.stringify({passed:result.passed,checks:checks.length,failures,errors},null,2));
 if(page){if(!result.passed)await page.screenshot({path:path.join(stage,'sftp-right-failure.png')}).catch(()=>{});await page.evaluate(async()=>{for(const id of [...sessions.keys()])await closeSession(id);}).catch(()=>{});}await browser.close();
}
