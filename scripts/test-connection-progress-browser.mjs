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
        return new Promise((resolve,reject) => { qa.requests[profileId] = {resolve,reject}; });
      }
      if (path.startsWith('/api/sessions/') && options.method === 'DELETE') { qa.deleted.push(path); return Promise.resolve({}); }
      if (path.endsWith('/disconnect-diagnostic')) return Promise.resolve({code:'DS-100',message:'测试断开'});
      return originalAPI(path, options);
    };
    window.WebSocket = class {
      static OPEN = 1;
      constructor(url) { this.readyState = 1; qa.sockets[new URL(url).pathname.split('/')[3]] = this; }
      send() {}
      close() { this.readyState = 3; this.onclose?.(); }
    };
    qa.start = (id, background = false, hasSecret = true) => {
      profiles.push({id,name:id,host:'fixture.invalid',port:22,user:'qa',auth:'password',hasSecret});
      void connect(id, false, {background,refreshHistory:false});
    };
    qa.state = id => [...sessions.values()].find(s => s.profileId === id);
    qa.text = async id => {
      const s = qa.state(id); await writeTerminalAndWait(s);
      const b = s.term.buffer.normal;
      return Array.from({length:b.length}, (_,i) => b.getLine(i).translateToString(true)).join('\n');
    };
    qa.resolve = (id, sessionId) => { const request = qa.requests[id]; delete qa.requests[id]; request.resolve({id:sessionId,profileId:id,home:'/'}); };
    qa.ready = (id, output = '') => {
      const socket = qa.sockets[qa.state(id).id];
      socket.onmessage({data:JSON.stringify({type:'ready'})});
      if (output) socket.onmessage({data:new TextEncoder().encode(output).buffer});
    };
  });
  const text = id => page.evaluate(id => qa.text(id), id);
  for (const theme of ['dark','light']) {
    await page.evaluate(theme => { CloudShellTheme.set(theme); qa.start(theme + '-slow'); }, theme);
    const slow = theme + '-slow', fast = theme + '-fast';
    await page.waitForFunction(id => !!qa.requests[id], {}, slow);
    assert.equal((await text(slow)).trim(), '🔗  连接主机...');
    assert.equal(await page.$('.terminal-connection-state'), null);
    await page.screenshot({path:stage + '/connecting-' + theme + '.png'});
    await page.evaluate(id => qa.start(id, true), fast);
    await page.waitForFunction(id => !!qa.requests[id], {}, fast);
    await page.evaluate(id => qa.resolve(id, id + '-1'), fast);
    await page.waitForFunction(id => !!qa.sockets[id + '-1'], {}, fast);
    assert(!(await text(fast)).includes('✅  连接主机成功！'), 'SSH authentication alone is not terminal readiness');
    await page.evaluate(id => qa.ready(id, 'REMOTE_FIRST_LINE\r\nqa@fixture:~$ '), fast);
    assert.match(await text(fast), /^🔗  连接主机\.\.\.\n✅  连接主机成功！\nREMOTE_FIRST_LINE\nqa@fixture:~\$ /);
    assert.equal((await text(slow)).trim(), '🔗  连接主机...', 'another tab received a success message');
    assert.equal(await page.evaluate(() => current().profileId), slow, 'background connection stole focus');
    await page.evaluate(id => qa.ready(id), fast);
    assert.equal((await text(fast)).split('✅  连接主机成功！').length - 1, 1, 'duplicate readiness repeated status');
    await page.evaluate(id => qa.requests[id].reject(Error('连接被拒绝\n请重试\x1b[2J')), slow);
    await page.waitForFunction(id => qa.state(id).connectionFailed, {}, slow);
    assert.match(await text(slow), /^🔗  连接主机\.\.\.\n❌  连接主机失败：连接被拒绝\n请重试\[2J/);
    assert(!(await text(slow)).includes('✅  连接主机成功！'));
    // Reconnect must append new status after retained output, not overwrite it.
    await page.evaluate(id => {
      activate(qa.state(id).id); markSessionDisconnected(qa.state(id));
      void connect(id, true, {refreshHistory:false});
    }, fast);
    await page.waitForFunction(id => !!qa.requests[id], {}, fast);
    const reconnecting = await text(fast);
    assert(reconnecting.includes('REMOTE_FIRST_LINE'));
    assert(reconnecting.lastIndexOf('🔗  连接主机...') > reconnecting.indexOf('── 重新连接'));
    await page.evaluate(id => qa.resolve(id, id + '-2'), fast);
    await page.waitForFunction(id => !!qa.sockets[id + '-2'], {}, fast);
    await page.evaluate(id => qa.ready(id, 'NEW_SHELL\r\nqa@fixture:~$ '), fast);
    assert.match(await text(fast), /🔗  连接主机\.\.\.\n✅  连接主机成功！\nNEW_SHELL/);
    await page.screenshot({path:stage + '/connected-' + theme + '.png'});
    checks.push(theme + ': inline progress, readiness ordering, concurrent tabs, failures, reconnect history');
  }
  // Closing a pending tab cannot resurrect it or print success after a late response.
  await page.evaluate(() => qa.start('cancelled'));
  await page.waitForFunction(() => !!qa.requests.cancelled);
  await page.evaluate(async () => { await closeSession(qa.state('cancelled').id); qa.resolve('cancelled','late-result'); });
  await page.waitForFunction(() => qa.deleted.includes('/api/sessions/late-result'));
  assert.equal(await page.evaluate(() => !!qa.state('cancelled')), false);
  // A failed terminal channel must never claim connection success.
  await page.evaluate(() => qa.start('channel-failure'));
  await page.waitForFunction(() => !!qa.requests['channel-failure']);
  await page.evaluate(() => qa.resolve('channel-failure','channel-failure-1'));
  await page.waitForFunction(() => !!qa.sockets['channel-failure-1']);
  await page.evaluate(() => qa.sockets['channel-failure-1'].close());
  assert(!(await text('channel-failure')).includes('✅  连接主机成功！'));
  assert((await text('channel-failure')).includes('🔌  连接已断开'));
  // Keep credential/host-key approval dialogs; cancellation stays in the terminal.
  await page.evaluate(() => qa.start('credentials', false, false));
  await page.waitForSelector('#action-dialog[open]');
  assert((await text('credentials')).includes('🔑  等待输入凭据'));
  await page.click('#action-cancel');
  await page.waitForFunction(() => qa.state('credentials').connectionFailed);
  assert((await text('credentials')).includes('🚫  已取消连接'));
  checks.push('late cancellation, channel startup failure, credential cancellation');
  assert.deepEqual(errors, []);
  fs.writeFileSync(stage + '/connection-progress.json', JSON.stringify({passed:true,checks,errors},null,2));
  console.log('PASS', checks);
} finally { await browser.close(); }
