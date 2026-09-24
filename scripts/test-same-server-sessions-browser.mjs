// Real terminal rendering with controlled connection/relay timing. No live SSH.
import fs from 'node:fs';
import assert from 'node:assert/strict';
const [stage, chrome, modulePath] = process.argv.slice(2);
const {default: puppeteer} = await import(modulePath);
const fixture = JSON.parse(fs.readFileSync(stage + '/browser-fixture.json'));
const origin = new URL(fixture.url).origin;
assert.equal(new URL(origin).hostname, '127.0.0.1');
const setup = await fetch(origin + '/api/appearance/patch', {method:'POST', headers:{'Content-Type':'application/json', 'X-CloudShell-Token':fixture.token}, body:JSON.stringify({onboardingCompleted:true, startupAnimation:false, uiScale:1})});
assert(setup.ok);
const browser = await puppeteer.launch({executablePath:chrome, headless:true, args:['--no-sandbox','--disable-dev-shm-usage']});
const checks = [], errors = [];
try {
  const page = await browser.newPage();
  await page.setViewport({width:1440,height:1000});
  page.on('pageerror', error => errors.push(error.message));
  await page.setRequestInterception(true);
  page.on('request', request => {
    const url = new URL(request.url());
    if (/^https?:/.test(url.protocol) && url.origin !== origin) request.abort(); else request.continue();
  });
  await page.goto(fixture.url, {waitUntil:'networkidle0'});
  await page.evaluate(() => {
    document.querySelectorAll('dialog[open]').forEach(dialog => dialog.close());
    setDrawer(false);
    pollStats = pollNetwork = pollLatency = () => {};
    navigate = async () => {};
    DengCommandHistory.refresh = async () => {};
    window.qa = {requests:{}, sockets:{}, deleted:[]};
    const originalAPI = api;
    api = (path, options = {}) => {
      if (path === '/api/sessions' && options.method === 'POST') {
        const {profileId} = JSON.parse(options.body);
        return new Promise((resolve,reject) => { (qa.requests[profileId] ||= []).push({resolve,reject}); });
      }
      if (path.startsWith('/api/sessions/') && options.method === 'DELETE') { qa.deleted.push(path); return Promise.resolve({}); }
      if (path.endsWith('/disconnect-diagnostic')) return Promise.resolve({code:'DS-100',message:'测试断开'});
      return originalAPI(path, options);
    };
    window.WebSocket = class {
      static OPEN = 1;
      constructor(url) { this.readyState = 1; qa.sockets[new URL(url).pathname.split('/')[3]] = this; }
      send(data) { (this.frames ||= []).push(JSON.parse(data)); }
      close() { this.readyState = 3; this.onclose?.(); }
    };
    qa.start = () => { void connect('same', false, {refreshHistory:false}); };
    qa.resolve = sessionId => { qa.requests.same.shift().resolve({id:sessionId, profileId:'same', home:'/', sftpAvailable:true}); };
    qa.ready = id => qa.sockets[id].onmessage({data:JSON.stringify({type:'ready'})});
    profiles.push({id:'same',name:'同一服务器',host:'fixture.invalid',port:22,user:'qa',auth:'password',hasSecret:true});
  });
  const start = async () => { await page.evaluate(() => qa.start()); await page.waitForFunction(() => qa.requests.same?.length > 0); };
  const finish = async id => { await page.evaluate(id=>qa.resolve(id),id); await page.waitForFunction(id=>!!qa.sockets[id],{},id); await page.evaluate(id=>qa.ready(id),id); };
  await start(); await finish('first');
  await start(); await page.evaluate(()=>qa.start());
  assert.equal(await page.evaluate(()=>qa.requests.same.length),1,'repeat pending clicks should coalesce');
  await finish('second');
  assert.equal(await page.evaluate(()=>sessions.size),2);
  assert.equal(await page.evaluate(()=>qa.deleted.length),0,'new session closed the first');
  assert.deepEqual(await page.$$eval('.session-tab-label', nodes=>nodes.map(n=>n.textContent)),['同一服务器 · 会话 1','同一服务器 · 会话 2']);
  await page.evaluate(()=>{
    activate('first');DengCommandComposer.open({id:'p',name:'p',body:'echo [p#1 value]',appendCR:true});
    window.qaPinned = document.querySelector('#composer-target').value;
    activate('second');
    pasteTerminalText(sessions.get('first'),'one',{execute:true});
    pasteTerminalText(sessions.get('second'),'two',{execute:true});
  });
  assert.equal(await page.$eval('#composer-target',e=>e.value),'first');
  assert(await page.evaluate(()=>qa.sockets.first.frames.some(f=>f.data?.includes('one'))));
  assert(!await page.evaluate(()=>qa.sockets.second.frames.some(f=>f.data?.includes('one'))));
  assert.equal(await page.$$eval('#composer-target option',nodes=>new Set(nodes.map(n=>n.textContent)).size),3);
  checks.push('new session, pending click coalescing, distinct labels, pinned target, input isolation');
  // Starting a third connection and reconnecting the first must have independent requests.
  await start();
  await page.evaluate(()=>{ activate('first');void connect('same',true,{sessionId:'first',refreshHistory:false}); });
  await page.waitForFunction(()=>qa.requests.same.length===2);
  await finish('third');await finish('first-reconnected');
  assert(await page.evaluate(()=>sessions.get('second')?.connected&&sessions.get('third')?.connected&&sessions.get('first-reconnected')?.connected));
  assert.deepEqual(await page.evaluate(()=>qa.deleted),['/api/sessions/first']);
  await page.evaluate(()=>closeSession('second'));
  assert(await page.evaluate(()=>sessions.get('third')?.connected&&sessions.get('first-reconnected')?.connected));
  checks.push('concurrent new connection/reconnect and closing one leaves siblings alive');
  // Cancel a new attempt, immediately retry, then discard the cancelled late result.
  await start();
  await page.evaluate(async()=>{ const pending=[...sessions.values()].find(s=>s.pendingConnection);await closeSession(pending.id);qa.start(); });
  await page.waitForFunction(()=>qa.requests.same.length===2);
  await page.evaluate(()=>qa.resolve('cancelled-late'));
  await page.waitForFunction(()=>qa.deleted.includes('/api/sessions/cancelled-late'));
  assert(await page.evaluate(()=>connecting.has('same')),'old attempt cleared the new pending request');
  await finish('after-cancel');
  assert(!await page.evaluate(()=>connecting.has('same')));
  assert.equal(await page.evaluate(()=>sessions.size),3);
  // The visible tab menu offers an explicit extra SSH session.
  await page.click('.session-tab[data-session-id="third"]',{button:'right'});
  assert(await page.$eval('#session-tab-menu',e=>e.textContent.includes('新建同服务器会话')));
  checks.push('cancel/retry isolation, late result cleanup, explicit tab menu');
  await page.screenshot({path:stage+'/same-server-sessions.png'});
  assert.deepEqual(errors,[]);
  fs.writeFileSync(stage+'/same-server-sessions.json',JSON.stringify({passed:true,checks,errors},null,2));
  console.log('PASS',checks);
} finally { await browser.close(); }
