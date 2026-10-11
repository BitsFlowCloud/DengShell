// Real page + xterm + isolated SSH/SFTP. The fixture must describe a disposable
// localhost backend and disposable container, never a user's connection/profile.
import fs from 'node:fs';
import path from 'node:path';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
const [stage, chrome, modulePath] = process.argv.slice(2);
const { default: puppeteer } = await import(modulePath);
const fixture = JSON.parse(fs.readFileSync(path.join(stage, 'browser-fixture.json')));
const origin = new URL(fixture.url).origin;
assert.equal(new URL(origin).hostname, '127.0.0.1');
assert.match(fixture.container, /^dengshell-sftp-layout-qa-/);
const baseline = process.env.SFTP_LAYOUT_EXPECT_BROKEN === '1';
const headers = {'Content-Type': 'application/json', 'X-CloudShell-Token': fixture.token};
async function api(route, body, method = body === undefined ? 'GET' : 'POST') {
  const response = await fetch(new URL(route, origin), {method, headers, body: body === undefined ? undefined : JSON.stringify(body)});
  const data = await response.json(); assert(response.ok, `${route}: ${JSON.stringify(data)}`); return data;
}
await api('/api/appearance/patch', {onboardingCompleted:true,startupAnimation:false,uiScale:1,filesPosition:'bottom'});
const key = await api('/api/keys', {name:'SFTP layout isolated test key', sourcePath:fixture.key});
const profile = await api('/api/profiles', {name:'SFTP layout isolated SSH', host:'127.0.0.1', port:fixture.port,user:'bitsflow',auth:'key',keyId:key.id});
const trust = await api('/api/sessions', {profileId:profile.id, hostKeyApproval:{host:'127.0.0.1:'+fixture.port,fingerprint:fixture.fingerprint}});
await api('/api/sessions/'+trust.id, undefined, 'DELETE');
const browser = await puppeteer.launch({executablePath:chrome,headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const checks=[],failures=[],errors=[],measurements=[];
const pause = ms => new Promise(resolve=>setTimeout(resolve,ms));
let page, sequence=0, completed=false;
async function check(name, test) {
  try { await test(); checks.push(name); }
  catch(error) { failures.push({name,error:error.stack || error.message}); }
}
async function settle() { await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))); await pause(120); }
async function snapshot() {
  return page.evaluate(()=>{
    const rect = element=>{const r=element.getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,height:r.height,bottom:r.bottom,right:r.right};};
    const state=current(), host=state?.host, term=state?.term;
    const xterm=host?.querySelector('.xterm'), style=xterm&&getComputedStyle(xterm);
    return {workspace:rect($('#workspace')),terminal:rect($('.terminal-panel')),files:rect($('#files-panel')),
      splitter:rect($('#files-splitter')),hidden:$('#files-panel').hidden,splitterHidden:$('#files-splitter').hidden,
      expanded:$('#toggle-sftp').getAttribute('aria-expanded'),position:appearance.filesPosition,scale:effectiveScale,
      configuredHeight:document.documentElement.style.getPropertyValue('--files-height'),session:state?.id,
      rows:term?.rows,cols:term?.cols,hostHeight:host?.clientHeight,cellHeight:term?._core._renderService.dimensions.css.cell.height,
      padding:style ? parseFloat(style.paddingTop)+parseFloat(style.paddingBottom) : 0,
      workspaceOverflow:$('#workspace').scrollHeight>$('#workspace').clientHeight+1};
  });
}
async function fullTerminal(label) {
  const s=await snapshot();measurements.push({label,...s});
  assert(s.hidden&&s.splitterHidden, 'SFTP or separator is not hidden');
  assert.equal(s.expanded,'false');
  assert(Math.abs(s.terminal.y-s.workspace.y)<2, `unused top gap: ${s.terminal.y-s.workspace.y}px`);
  assert(Math.abs(s.workspace.bottom-s.terminal.bottom)<2, `unused bottom gap: ${s.workspace.bottom-s.terminal.bottom}px`);
  assert.equal(s.workspaceOverflow,false,'workspace overflow');
  if(s.rows) {
    const available=s.hostHeight-s.padding;
    assert(s.rows*s.cellHeight<=available+2, 'xterm rows overflow terminal');
    assert(available-s.rows*s.cellHeight<s.cellHeight+2, `xterm leaves ${available-s.rows*s.cellHeight}px unused`);
  }
  return s;
}
async function realPTY(label) {
  await page.waitForFunction(()=>current()?.ready&&current()?.shellIntegration?.atPrompt,{timeout:20000});
  const filename='/home/bitsflow/layout-pty-'+(++sequence)+'.txt';
  await page.evaluate(filename=>pasteTerminalText(current(),'stty size > '+filename,{execute:true}),filename);
  let value;
  for(let i=0;i<100;i++) {
    try { value=execFileSync('docker',['exec',fixture.container,'cat',filename],{encoding:'utf8',stdio:['ignore','pipe','ignore']}).trim(); if(value)break; } catch{}
    await pause(50);
  }
  assert(value, label+': remote stty output missing');
  const [rows,cols]=value.split(/\s+/).map(Number), local=await snapshot();
  assert.deepEqual({rows,cols},{rows:local.rows,cols:local.cols},label+': remote PTY differs from xterm');
  measurements.push({label,remotePTY:{rows,cols},session:local.session});
}
async function connect() {
  const id=await page.evaluate(async id=>(await connect(id)).id,profile.id);
  await page.waitForFunction(id=>sessions.get(id)?.ready&&sessions.get(id)?.shellIntegration?.atPrompt&&filesUsable(sessions.get(id)),{timeout:30000},id);
  await settle();return id;
}
async function placement(position) {
  await page.evaluate(position=>{appearance.filesPosition=position;applyWorkspacePlacement();},position);await settle();
}
async function show() { if(await page.$eval('#files-panel',e=>e.hidden))await page.click('#toggle-sftp');await settle(); }
async function hide() { if(!await page.$eval('#files-panel',e=>e.hidden))await page.click('#toggle-sftp');await settle(); }
try {
  page=await browser.newPage();page.on('pageerror',error=>errors.push(error.message));
  await page.setViewport({width:1600,height:1000,deviceScaleFactor:1});
  await page.emulateMediaFeatures([{name:'prefers-reduced-motion',value:'reduce'}]);
  await page.setRequestInterception(true);
  page.on('request',request=>{const url=new URL(request.url());if(/^https?:/.test(url.protocol)&&url.origin!==origin)request.abort();else request.continue();});
  await page.goto(fixture.url,{waitUntil:'networkidle0'});
  await page.evaluate(()=>{document.querySelectorAll('dialog[open]').forEach(d=>d.close());setDrawer(false);});
  let first=await connect();
  for(const position of ['bottom','top']) {
    await placement(position);await show();
    const before=await snapshot();measurements.push({label:position+' shown before hide',...before});
    await hide();
    await check(`${position}: hide fills workspace and grows actual terminal/PTY`,async()=>{
      const after=await fullTerminal(position+' hidden');
      assert(after.rows>before.rows, `rows did not grow (${before.rows} -> ${after.rows})`);
      await realPTY(position+' hidden');
    });
    await page.screenshot({path:path.join(stage,`sftp-layout-${baseline?'before':'after'}-${position}.png`)});
    if(baseline)continue;
    await check(`${position}: repeated toggle preserves panel height and directory`,async()=>{
      const directory=await page.evaluate(()=>current().cwd);
      for(let i=0;i<4;i++) { await show();assert(Math.abs((await snapshot()).files.height-before.files.height)<2);await hide();await fullTerminal(`${position} toggle ${i+1}`); }
      assert.equal(await page.evaluate(()=>current().cwd),directory);await realPTY(position+' repeated toggle');
    });
    await check(`${position}: separator keyboard and pointer resizing survive hide/show`,async()=>{
      await show();await page.focus('#files-splitter');await page.keyboard.press(position==='top'?'ArrowDown':'ArrowUp');await settle();
      const beforeDrag=await snapshot(), s=beforeDrag.splitter;
      await page.mouse.move(s.x+s.width/2,s.y+s.height/2);await page.mouse.down();await page.mouse.move(s.x+s.width/2,s.y+s.height/2+(position==='top'?32:-32),{steps:6});await page.mouse.up();await settle();
      const dragged=await snapshot();assert(dragged.files.height>beforeDrag.files.height+20);
      await hide();await fullTerminal(position+' after drag');await show();assert(Math.abs((await snapshot()).files.height-dragged.files.height)<2);await hide();
    });
    for(const viewport of [{width:1440,height:900},{width:960,height:700},{width:640,height:800}]) {
      await page.setViewport({...viewport,deviceScaleFactor:1});await settle();
      await check(`${position}: hidden panel on resize ${viewport.width}x${viewport.height}`,async()=>{await fullTerminal(position+' resize '+viewport.width);await realPTY(position+' resized');});
    }
    await page.setViewport({width:1600,height:1000,deviceScaleFactor:1});
    for(const scale of [.75,1,1.25,1.5]) {
      await page.evaluate(scale=>{appearance.uiScale=scale;applyUIScale();},scale);await settle();
      await check(`${position}: hidden panel with UI scale ${scale}`,async()=>{await fullTerminal(position+' UI '+scale);await realPTY(position+' UI '+scale);});
    }
    await page.evaluate(()=>{appearance.uiScale=1;applyUIScale();});await settle();
    await check(`${position}: font change refits the actual PTY`,async()=>{
      const beforeFont=await snapshot();for(let i=0;i<6;i++)await page.click('#font-up');await settle();
      const afterFont=await fullTerminal(position+' larger font');assert(afterFont.rows<beforeFont.rows);await realPTY(position+' larger font');
      for(let i=0;i<6;i++)await page.click('#font-down');await settle();await fullTerminal(position+' restored font');
    });
    await check(`${position}: all workspace tabs reopen and close cleanly`,async()=>{
      for(const pane of ['files','transfers','commands','common-apps']) {
        await page.evaluate(pane=>showPane(pane),pane);await settle();
        assert.equal(await page.$eval('#files-panel',e=>e.hidden),false);
        assert.equal(await page.$eval('.file-tab.active',e=>e.dataset.pane),pane);
        await hide();await fullTerminal(position+' '+pane+' hidden');
      }
    });
  }
  if(!baseline) {
    await placement('bottom');await show();
    const second=await connect();
    await check('Two real SSH sessions refit independently after switching while hidden',async()=>{
      const shownSecond=(await snapshot()).rows;await hide();const hiddenSecond=await fullTerminal('second SSH hidden');assert(hiddenSecond.rows>shownSecond);await realPTY('second SSH hidden');
      await page.click(`.session-tab[data-session-id="${first}"] > button[role="tab"]`);await settle();await fullTerminal('first SSH activated');await realPTY('first SSH activated');
      await page.click(`.session-tab[data-session-id="${second}"] > button[role="tab"]`);await settle();await fullTerminal('second SSH reactivated');await realPTY('second SSH reactivated');
    });
    await check('Real SFTP upload and transfer pane preserve hidden terminal geometry',async()=>{
      await page.evaluate(async()=>queueFiles([{file:new File(['layout SFTP real upload\n'],'layout-upload.txt'),relativePath:'layout-upload.txt'}],current(),'/home/bitsflow'));
      await page.waitForFunction(()=>[...localTasks.values()].some(t=>t.name==='layout-upload.txt'&&t.status==='done'),{timeout:20000});
      assert.equal(execFileSync('docker',['exec',fixture.container,'cat','/home/bitsflow/layout-upload.txt'],{encoding:'utf8'}),'layout SFTP real upload\n');
      await fullTerminal('after real upload');await page.evaluate(()=>showPane('transfers'));await settle();
      assert.equal(await page.$eval('#transfers-view',e=>e.hidden),false);await hide();await fullTerminal('after transfer pane');await realPTY('after transfer pane');
    });
    for(const position of ['bottom','top']) {
      await check(`${position}: refresh restores hidden layout and new SSH fits fully`,async()=>{
        await placement(position);await api('/api/appearance/patch',{filesPosition:position});await show();await hide();
        await page.evaluate(async()=>{await DengPortablePreferences.flush();for(const id of [...sessions.keys()])await closeSession(id);});
        await page.reload({waitUntil:'networkidle0'});await page.evaluate(()=>{document.querySelectorAll('dialog[open]').forEach(d=>d.close());setDrawer(false);});await settle();
        assert.equal(await page.evaluate(()=>appearance.filesPosition),position);await fullTerminal(position+' restored before SSH');
        first=await connect();await fullTerminal(position+' restored connected');await realPTY(position+' restored connected');
      });
    }
  }
  assert.deepEqual(errors,[]);
  if(baseline) {
    assert.equal(failures.length,2,'baseline must fail exactly the two hide-geometry cases');
    assert(failures.every(f=>/unused (top|bottom) gap/.test(f.error)),JSON.stringify(failures));
  } else assert.deepEqual(failures,[]);
  completed=true;
} finally {
  const result={passed:!baseline&&completed&&failures.length===0&&errors.length===0,completed,expectedRegressionReproduced:baseline&&completed&&failures.length===2,checks,failures,errors,measurements};
  fs.writeFileSync(path.join(stage,'sftp-layout-results.json'),JSON.stringify(result,null,2));
  console.log(JSON.stringify({passed:result.passed,expectedRegressionReproduced:result.expectedRegressionReproduced,checks:checks.length,failures,errors},null,2));
  if(page)await page.evaluate(async()=>{for(const id of [...sessions.keys()])await closeSession(id);}).catch(()=>{});
  await browser.close();
}
