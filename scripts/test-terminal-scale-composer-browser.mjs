import fs from 'node:fs';
import assert from 'node:assert/strict';
const [stage, chrome, modulePath] = process.argv.slice(2);
const { default: puppeteer } = await import(modulePath);
const fixture = JSON.parse(fs.readFileSync(stage + '/browser-fixture.json'));
assert.equal(new URL(fixture.url).hostname, '127.0.0.1');
const response = await fetch(new URL('/api/appearance/patch', fixture.url), { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-CloudShell-Token': fixture.token }, body: JSON.stringify({ onboardingCompleted: true, startupAnimation: false, uiScale: 1 }) });
assert(response.ok);
const browser = await puppeteer.launch({ executablePath: chrome, headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
const checks = [], failures = [], errors = [];
async function check(name, action) {
  try { await action(); checks.push(name); } catch (error) { failures.push({ name, error: error.message }); }
}
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
let page;
try {
  page = await browser.newPage(); page.on('pageerror', error => errors.push(error.message));
  await page.setViewport({ width: 1600, height: 1100, deviceScaleFactor: 1 });
  await page.goto(fixture.url, { waitUntil: 'networkidle0' });
  await page.evaluate(() => {
    document.querySelectorAll('dialog[open]').forEach(d => d.close()); setDrawer(false);
    pollStats = () => {}; pollNetwork = () => {};
    const id = 'scale-fixture';
    profiles.push({ id, name: 'Isolated terminal', user: 'qa', host: 'fixture.invalid', port: 22 });
    const state = { ...makeSessionState({ id, profileId: id, home: '/' }), localOnly: true };
    sessions.set(id, state); createTerminal(state);
    state.localOnly = false; state.ready = true; state.term.options.disableStdin = false;
    window.qaFrames = [];
    state.ws = { readyState: WebSocket.OPEN, send(data) { qaFrames.push(JSON.parse(data)); }, close() {} };
    activate(id);
  });
  // Locate real rendered text, not the mouse service's own geometry.
  async function wordPoint(row, column) {
    return page.evaluate(({ row, column }) => {
      const span = current().host.querySelectorAll('.xterm-rows > div')[row].querySelector('span');
      const rect = span.getBoundingClientRect();
      return { x: rect.left + rect.width / span.textContent.length * (column + .2), y: rect.top + rect.height / 2 };
    }, { row, column });
  }
  async function fill(scroll = false) {
    await page.evaluate(async scroll => {
      const term = current().term; term.reset();
      await new Promise(resolve => term.write('\x1b[?1000l\x1b[?1006l', resolve));
      const text = Array.from({ length: scroll ? 130 : Math.min(24, term.rows - 1) }, (_, i) => `ROW${String(i).padStart(3, '0')} abcdefghijklmnopqrstuvwxyz`).join('\r\n');
      await new Promise(resolve => term.write(text, resolve));
      if (scroll) term.scrollToLine(50);
    }, scroll); await pause(90);
  }
  for (const dpr of [1, 1.5, 2]) {
    await page.setViewport({ width: 1600, height: 1100, deviceScaleFactor: dpr });
    for (const scale of [.75, .9, 1, 1.1, 1.25, 1.5]) {
      await page.evaluate(scale => { appearance.uiScale = scale; applyUIScale(); }, scale); await pause(100); await fill();
      await check(`Selection at UI ${scale}, DPR ${dpr}`, async () => {
        const p = await wordPoint(12, 2); await page.mouse.click(p.x, p.y, { count: 2 });
        assert.equal(await page.evaluate(() => current().term.getSelection()), 'ROW012');
        const a = await wordPoint(4, 0), b = await wordPoint(6, 6);
        await page.mouse.move(a.x, a.y); await page.mouse.down(); await page.mouse.move(b.x, b.y, { steps: 8 }); await page.mouse.up();
        assert.equal(await page.evaluate(() => current().term.getSelection()), 'ROW004 abcdefghijklmnopqrstuvwxyz\nROW005 abcdefghijklmnopqrstuvwxyz\nROW006');
      });
      await check(`Mouse reporting at UI ${scale}, DPR ${dpr}`, async () => {
        await page.evaluate(async () => { await new Promise(r => current().term.write('\x1b[?1000h\x1b[?1006h', r)); qaFrames = []; });
        const p = await wordPoint(12, 9); await page.mouse.click(p.x, p.y);
        assert((await page.evaluate(() => qaFrames.some(f => f.data === '\x1b[<0;10;13M'))), JSON.stringify(await page.evaluate(() => qaFrames)));
        await page.evaluate(() => new Promise(r => current().term.write('\x1b[?1000l\x1b[?1006l', r)));
      });
    }
  }
  await page.evaluate(() => { appearance.uiScale = .9; applyUIScale(); }); await pause(100); await fill(true);
  await check('Scrollback selection uses visible row at 90%', async () => {
    const p = await wordPoint(12, 2); await page.mouse.click(p.x, p.y, { count: 2 });
    assert.equal(await page.evaluate(() => current().term.getSelection()), 'ROW062');
  });
  await page.evaluate(() => { appearance.uiScale = 1.25; applyUIScale(); }); await pause(100); await fill(true);
  await check('Drag near bottom inside terminal does not scroll at 125%', async () => {
    const points = await page.evaluate(() => {
      const rows = current().host.querySelectorAll('.xterm-rows > div'), a = rows[2].getBoundingClientRect(), b = rows[rows.length - 1].getBoundingClientRect();
      return { x: a.left + 40, start: a.top + a.height / 2, end: b.top + b.height / 2, before: current().term.buffer.active.viewportY };
    });
    await page.mouse.move(points.x, points.start); await page.mouse.down(); await page.mouse.move(points.x, points.end, { steps: 10 }); await pause(220); await page.mouse.up();
    assert.equal(await page.evaluate(() => current().term.buffer.active.viewportY), points.before);
  });
  await page.evaluate(() => { appearance.uiScale = 1; applyUIScale(); window.qaCommand = { id: 'qa-param', name: '参数命令', body: 'echo [p#1 内容]', appendCR: true }; });
  for (const theme of ['dark', 'light']) {
    for (const width of [1600, 900]) {
      await page.setViewport({ width, height: 1000, deviceScaleFactor: 1 });
      await page.evaluate(theme => { document.documentElement.dataset.theme = theme; document.documentElement.style.setProperty('--files-height', '240px'); showPane('commands'); }, theme);
      await pause(100);
      const before = await page.$eval('#files-panel', e => e.getBoundingClientRect().height);
      await page.evaluate(() => DengCommandComposer.run(qaCommand));
      await pause(100);
      await check(`Stable compact parameters: ${theme} ${width}`, async () => {
        assert(Math.abs(await page.$eval('#files-panel', e => e.getBoundingClientRect().height) - before) < 1);
        assert.equal(await page.$eval('#command-list', e => getComputedStyle(e).display), 'none');
        const size = await page.$eval('[data-parameter="1"]', e => ({ height: e.getBoundingClientRect().height, width: e.getBoundingClientRect().width }));
        assert(size.height <= 33 && size.width <= 241, JSON.stringify(size));
        if (width === 1600) assert(await page.evaluate(() => $('#composer-send').getBoundingClientRect().bottom <= $('#files-panel').getBoundingClientRect().bottom), 'send button clipped');
        await page.type('[data-parameter="1"]', 'hello'); await page.click('#composer-send');
        assert.notEqual(await page.$eval('#command-list', e => getComputedStyle(e).display), 'none');
        assert(Math.abs(await page.$eval('#files-panel', e => e.getBoundingClientRect().height) - before) < 1);
      });
      await page.evaluate(() => DengCommandComposer.run(qaCommand));
      await page.screenshot({path: stage + `/evidence/parameters-${theme}-${width}.png`});
      await page.click('#command-composer .text-button');
      await page.evaluate(() => DengCommandComposer.open({ ...qaCommand, id: 'clear', body: 'echo hi' }));
      await page.click('#command-composer .text-button');
    }
  }
  await page.setViewport({ width: 1600, height: 1100, deviceScaleFactor: 1 });
  await page.evaluate(() => { qaCommand.id = 'qa-reusable'; });
  await check('SSH-only disables files, retains command sending and restores other tabs', async () => {
    const requests = [];
    const capture = req => { if (/\/files\?|\/upload/.test(req.url())) requests.push(req.url()); };
    page.on('request', capture);
    await page.evaluate(async () => {
      current().sftpAvailable = false; renderSessionInfo(); renderFiles();
      await navigate('/'); queueFiles([{file: new File(['x'],'fixture.txt'),relativePath:'fixture.txt'}]);
    });
    await pause(120);
    for (const id of ['path-input','choose-files','refresh-files','mkdir','directory-favorites-button','path-history-button','follow-terminal']) assert(await page.$eval('#'+id,e=>e.disabled), id);
    assert.match(await page.$eval('#file-empty',e=>e.textContent), /SFTP/);
    assert.deepEqual(requests, []);
    const frames = await page.evaluate(() => { qaFrames=[]; DengCommandComposer.run({id:'shell-only', name:'only SSH', body:'pwd', appendCR:true}); return qaFrames; });
    assert(frames.some(f=>f.type==='input'));
    await page.evaluate(() => { current().sftpAvailable = true; renderSessionInfo(); renderFiles(); });
    assert.equal(await page.$eval('#choose-files',e=>e.disabled), false);
    page.off('request',capture);
  });
  await check('Parameter command collapses and restores height after sending, retains reusable draft', async () => {
    const before = await page.evaluate(() => document.documentElement.style.getPropertyValue('--files-height'));
    await page.evaluate(() => DengCommandComposer.run(qaCommand));
    await page.type('[data-parameter="1"]', 'hello'); await page.click('#composer-send');
    assert(await page.$eval('#command-composer', e => e.hidden));
    assert.equal(await page.evaluate(() => document.documentElement.style.getPropertyValue('--files-height')), before);
    await page.evaluate(() => DengCommandComposer.run(qaCommand));
    assert.equal(await page.$eval('[data-parameter="1"]', e => e.value), 'hello');
  });
  await check('Switching to a direct command collapses old compact panel', async () => {
    await page.evaluate(() => { DengCommandComposer.run(qaCommand); DengCommandComposer.run({ id: 'direct', name: '普通命令', body: 'pwd', appendCR: true }); });
    assert(await page.$eval('#command-composer', e => e.hidden));
  });
  await check('Failed or refused send preserves parameter input', async () => {
    await page.evaluate(() => { DengCommandComposer.run(qaCommand); window.qaPaste = pasteTerminalText; pasteTerminalText = () => false; });
    await page.click('#composer-send'); assert.equal(await page.$eval('#command-composer', e => e.hidden), false);
    await page.evaluate(() => { pasteTerminalText = () => { throw Error('fixture send failure'); }; });
    await page.click('#composer-send'); assert.equal(await page.$eval('#command-composer', e => e.hidden), false);
    assert.equal(await page.$eval('[data-parameter="1"]', e => e.value), 'hello');
    await page.evaluate(() => { pasteTerminalText = qaPaste; });
  });
  await check('Changing command group hides the old parameter panel', async () => {
    await page.evaluate(() => { commandGroups = ['First', 'Second']; commandGroup = 'First'; renderCommands(); DengCommandComposer.run(qaCommand); });
    await page.click('[data-command-group="Second"]');
    assert(await page.$eval('#command-composer', e => e.hidden));
  });
  await check('Missing parameters or disconnected target do not dismiss the panel', async () => {
    await page.evaluate(() => DengCommandComposer.run({ ...qaCommand, id: 'missing' }));
    assert(await page.$eval('#composer-send', e => e.disabled));
    assert.equal(await page.$eval('#command-composer', e => e.hidden), false);
    await page.type('[data-parameter="1"]', 'retained');
    await page.evaluate(() => { current().connected = false; DengCommandComposer.reflect(); });
    assert(await page.$eval('#composer-send', e => e.disabled));
    assert.equal(await page.$eval('[data-parameter="1"]', e => e.value), 'retained');
    await page.evaluate(() => { current().connected = true; DengCommandComposer.reflect(); });
  });
  await check('User-adjusted pane height survives auto-collapse', async () => {
    await page.evaluate(() => { DengCommandComposer.run(qaCommand); document.documentElement.style.setProperty('--files-height', '410px'); });
    await page.click('#composer-send');
    assert(await page.$eval('#command-composer', e => e.hidden));
    assert.equal(await page.evaluate(() => document.documentElement.style.getPropertyValue('--files-height')), '410px');
  });
  await check('Explicit command editor stays open for editing', async () => {
    await page.evaluate(() => DengCommandComposer.open(qaCommand)); await page.click('#composer-send');
    assert.equal(await page.$eval('#command-composer', e => e.hidden), false);
  });
  await page.screenshot({ path: stage + '/evidence/terminal-scale-composer.png' });
  fs.writeFileSync(stage + '/evidence/terminal-scale-composer.json', JSON.stringify({ checks, failures, errors }, null, 2));
  console.log(JSON.stringify({ passed: checks.length, failures, errors }, null, 2));
  assert.deepEqual(failures, []); assert.deepEqual(errors, []);
} finally { await browser.close(); }
