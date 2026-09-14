import fs from 'node:fs';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
const {default:puppeteer}=await import(process.env.DENG_PUPPETEER_MODULE||'puppeteer-core');
const stage = process.argv[2];
if (!stage) throw Error('Usage: node scripts/test-reconnect-browser.mjs PRIVATE_QA_DIR');
const fixture = JSON.parse(fs.readFileSync(stage+'/browser-fixture.json'));
const connection = JSON.parse(fs.readFileSync(stage+'/connection.json'));
const origin = new URL(fixture.url).origin;
assert.equal(new URL(origin).hostname, '127.0.0.1');
const api = async (path, body) => {
  const response = await fetch(origin+path, { method: body ? 'POST' : 'GET', headers: {'X-CloudShell-Token':fixture.token, 'Content-Type':'application/json'}, body:body?JSON.stringify(body):undefined });
  const value = await response.json(); assert(response.ok, JSON.stringify(value)); return value;
};
await api('/api/appearance/patch', {onboardingCompleted:true, startupAnimation:false, theme:'light', uiScale:1});
const profiles = [];
for (const shell of ['bash','zsh','fish']) profiles.push(await api('/api/profiles', {name:'隔离重连测试 '+shell, host:'127.0.0.1', port:connection.port, user:'qa_'+shell, auth:'key', keyPath:connection.keyPath}));
const browser = await puppeteer.launch({executablePath:process.env.CHROME_BINARY||'google-chrome', headless:true, userDataDir:stage+'/browser-profile', args:['--no-sandbox','--disable-dev-shm-usage','--disable-background-networking']});
const page = await browser.newPage(), errors = [], clearRequests = [], results = [];
page.on('pageerror', error=>errors.push(error.message));
await page.setRequestInterception(true);
page.on('request', request=>{
  const url = new URL(request.url());
  if (request.method()==='DELETE' && /\/history$/.test(url.pathname)) clearRequests.push(url.pathname);
  if (/^https?:/.test(url.protocol) && url.origin!==origin) request.abort(); else request.continue();
});
const delay = ms=>new Promise(resolve=>setTimeout(resolve,ms));
async function waitConnected(oldID='') {
  const deadline = Date.now()+30000;
  while(Date.now()<deadline) {
    if(await page.evaluate(id=>!!current()?.ready && current().id!==id && !!current().shellIntegration?.ready && !!current().shellIntegration?.atPrompt, oldID)) return;
    // Only this private loopback fixture's empty test-key password and host key are accepted.
    if (await page.$('#action-dialog[open]')) await page.click('#action-confirm');
    await delay(100);
  }
  throw Error('Connection did not become ready: '+await page.evaluate(()=>({message:current()?.connectionMessage, integration:current()?.shellIntegration?.ready, toast:document.querySelector('#toast').textContent})).then(JSON.stringify));
}
async function snapshot(marker) {
  const ui=await page.evaluate(marker=>{
    const state=current(), buffer=state.term.buffer.active;
    const lines=Array.from({length:buffer.length},(_,i)=>buffer.getLine(i).translateToString(true));
    return {id:state.id, connected:state.connected, screenContainsMarker:lines.some(line=>line.includes(marker)), screen:lines.join('\n'), inputHistory:[...state.history], globalList:[...globalCommandHistory.entries]};
  },marker);
  ui.persistedGlobal=(await api('/api/history')).entries;
  ui.persistedProfile=(await api('/api/profiles/'+await page.evaluate(()=>current().profileId)+'/history')).entries;
  return ui;
}
try {
  await page.setViewport({width:1440,height:1000});
  await page.goto(fixture.url,{waitUntil:'networkidle2'});
  await page.evaluate(async()=>{await loadProfiles();setDrawer(false)});
  for(const [index, profile] of profiles.entries()) {
    const shell=['bash','zsh','fish'][index];
    await page.evaluate(id=>{window.qaConnection=connect(id)},profile.id);
    await waitConnected();
    if(index===0) {
      const savedID=await page.evaluate(()=>current().id);
      const command="printf 'PAUSE_BEGIN\\n'; sleep 16; printf 'PAUSE_END\\n'";
      await page.type('#command-input',command);await page.keyboard.press('Enter');
      await page.waitForFunction(command=>globalCommandHistory.entries.includes(command),{},command);
      execFileSync('sudo',['-n','docker','pause',connection.container]);
      try { await delay(12000); } finally { execFileSync('sudo',['-n','docker','unpause',connection.container]); }
      await page.waitForFunction(()=>current()?.shellIntegration?.atPrompt,{timeout:20000});
      assert.equal(await page.evaluate(()=>current().id),savedID,'brief outage must retain original SSH session');
      assert.equal(await page.evaluate(()=>current().connected&&current().ready),true);
      const buffer=await snapshot('PAUSE_END');assert(buffer.screenContainsMarker);
      fs.writeFileSync(stage+'/network-pause.json',JSON.stringify({seconds:12,sameSession:true,connected:true,commandCompleted:true},null,2));
      console.log('PASS: 12-second server pause recovered on the same SSH connection with its running command intact');
    }
    for(const mode of ['manual','remote-kill']) {
      const marker=`QA_${shell.toUpperCase()}_${mode.replace('-','_').toUpperCase()}_HISTORY`;
      const command=`printf '${marker}\\n'`;
      await page.type('#command-input',command);
      await page.keyboard.press('Enter');
      await page.waitForFunction(command=>globalCommandHistory.entries.includes(command)&&current().shellIntegration.atPrompt, {timeout:15000}, command);
      await page.evaluate(()=>DengCommandHistory.flush());
      const before = await snapshot(marker);
      assert(before.screenContainsMarker);
      assert(before.persistedGlobal.includes(command));
      if(index===0&&mode==='manual') await page.screenshot({path:stage+'/before-disconnect.png'});
      if(mode==='manual') await page.click('#disconnect');
      else execFileSync('sudo',['-n','docker','exec',connection.container,'pkill','-KILL','-u','qa_'+shell]);
      await page.waitForFunction(()=>current()&&!current().connected);
      await page.waitForFunction(()=>!!current()?.disconnectDiagnostic?.code,{timeout:5000});
      const diagnostic=await api('/api/sessions/'+before.id+'/disconnect-diagnostic');
      assert(diagnostic.code&&diagnostic.traceId&&diagnostic.logPath,'missing persistent disconnect diagnosis');
      if(mode==='manual')assert.equal(diagnostic.code,'DS-100');
      else assert(['DS-202','DS-210','DS-211'].includes(diagnostic.code),'unexpected remote disconnect code: '+diagnostic.code);
      assert(await page.evaluate(()=>current().term.buffer.active.length>0&&document.querySelector('#terminal-state').textContent.includes(current().disconnectDiagnostic.code)),'diagnostic code missing from terminal status');
      const disconnected=await snapshot(marker);
      assert(disconnected.screenContainsMarker,'disconnect must retain terminal output');
      if(index===0&&mode==='manual') await page.screenshot({path:stage+'/after-disconnect.png'});
      if(index===0&&mode==='manual') {
        await api('/api/profiles',{...profile,port:9});await page.evaluate(()=>loadProfiles());
        await page.click('#reconnect');
        const deadline=Date.now()+25000;
        while(Date.now()<deadline&&!await page.evaluate(()=>!!current()?.connectionFailed)) {
          if(await page.$('#action-dialog[open]'))await page.click('#action-confirm');await delay(100);
        }
        assert(await page.evaluate(()=>!!current()?.connectionFailed),'failed reconnect fixture');
        assert((await snapshot(marker)).screenContainsMarker,'failed reconnect must also preserve old screen');
        await api('/api/profiles',profile);await page.evaluate(()=>loadProfiles());
        console.log('PASS: failed reconnect retained old terminal output, followed by a successful retry');
      }
      await page.click('#reconnect');
      await waitConnected(before.id);
      await page.evaluate(()=>DengCommandHistory.refresh(current().profileId));
      const reconnected=await snapshot(marker);
      assert.notEqual(reconnected.id,before.id);
      assert.equal(reconnected.screenContainsMarker,true,'reconnect must retain old terminal output');
      assert(reconnected.persistedGlobal.includes(command),'persistent global history');
      assert(reconnected.persistedProfile.includes(command),'persistent per-profile history');
      assert(reconnected.inputHistory.includes(command),'input-bar navigation cache');
      if(index===0&&mode==='manual') await page.screenshot({path:stage+'/after-reconnect.png'});
      await page.click('#command-history');
      await page.waitForSelector('#history-dialog[open]');
      assert(await page.$$eval('.history-entry code',(nodes,command)=>nodes.some(node=>node.textContent===command),command),'history modal displays previous command');
      if(index===0&&mode==='manual') await page.screenshot({path:stage+'/history-list-after-reconnect.png'});
      await page.keyboard.press('Escape');
      await page.click('#command-input');await page.keyboard.press('ArrowUp');
      assert.equal(await page.$eval('#command-input',input=>input.value),command,'input bar up arrow recalls previous command');
      await page.$eval('#command-input',input=>{input.value=''});
      results.push({shell,mode,marker,command,diagnostic,before,disconnected,reconnected,historyDialogRetained:true,inputArrowUpRetained:true});
      fs.writeFileSync(stage+'/results.json',JSON.stringify({results,clearRequests,errors},null,2));
      console.log(`${shell} ${mode}: old terminal output retained; global history, profile history, dialog and input-bar up arrow retained`);
    }
    await page.evaluate(()=>closeSession(current().id));
  }
  assert.deepEqual(clearRequests,[]);
  assert.deepEqual(errors,[]);
  // Reload the app page to verify persisted history survives client reinitialization too.
  await page.reload({waitUntil:'networkidle2'});
  await page.evaluate(()=>loadProfiles());
  const persisted=await api('/api/history');
  for(const result of results) assert(persisted.entries.includes(result.command));
  fs.writeFileSync(stage+'/results.json',JSON.stringify({results,clearRequests,errors,pageReloadRetained:true},null,2));
  console.log('PASS: 6 SSH reconnect scenarios and page reload; no history DELETE request issued');
} finally { await browser.close(); }
