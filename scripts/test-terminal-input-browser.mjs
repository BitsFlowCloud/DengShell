import fs from 'node:fs';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
const [stage, chrome, modulePath] = process.argv.slice(2);
const { default: puppeteer } = await import(modulePath);
const fixture = JSON.parse(fs.readFileSync(stage + '/fixture.json'));
const browser = await puppeteer.launch({ executablePath: chrome, headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'], defaultViewport: { width: 1440, height: 900 } });
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
const root = '/tmp/dengshell-terminal-input';
const read = name => execFileSync('docker', ['exec', fixture.container, 'cat', root + '/' + name], { encoding: 'utf8' });
const exists = name => {
  try { execFileSync('docker', ['exec', fixture.container, 'test', '-e', root + '/' + name], { stdio: 'ignore' }); return true; }
  catch { return false; }
};
let page;
const checks = [], errors = [];
try {
  await browser.defaultBrowserContext().overridePermissions(new URL(fixture.url).origin, ['clipboard-read', 'clipboard-write', 'clipboard-sanitized-write']);
  page = await browser.newPage();
  page.on('pageerror', error => errors.push(error.message));
  await page.goto(fixture.url, { waitUntil: 'networkidle0' });
  await page.waitForFunction(() => window.DengQuickConnect && !document.documentElement.dataset.lightSwitch);
  await page.evaluate(() => document.querySelectorAll('dialog[open]').forEach(d => d.close()));
  async function connect() {
    const previous = await page.evaluate(() => current()?.id);
    await page.click('#add-session'); await page.type('#quick-connect-command', 'ssh -p 22451 demo@127.0.0.1'); await page.click('#quick-connect-submit');
    await page.waitForSelector('#action-dialog[open] #action-input', { visible: true });
    await page.type('#action-input', fixture.password); await page.click('#action-confirm');
    await page.waitForFunction(previous => current()?.ready && current().id !== previous || document.querySelector('#action-dialog').open && document.querySelector('#action-description').textContent.includes('SHA256'), {}, previous);
    if (await page.$eval('#action-dialog', d => d.open)) {
      assert((await page.$eval('#action-description', e => e.textContent)).includes(fixture.fingerprint));
      await page.click('#action-confirm');
    }
    await page.waitForFunction(previous => current()?.ready && current().id !== previous, {}, previous); await prompt();
    return page.evaluate(() => current().id);
  }
  async function prompt() { await page.waitForFunction(() => current()?.ready && current().shellIntegration?.ready && current().shellIntegration.atPrompt, { timeout: 20000 }); }
  async function command(text, wait = true) {
    await page.$eval('#command-input', (e, text) => { e.value = text; e.dispatchEvent(new Event('input', { bubbles: true })); }, text);
    await page.focus('#command-input'); await page.keyboard.press('Enter');
    if (wait) await prompt();
  }
  async function capture() {
    await page.evaluate(() => {
      window.terminalInputFrames = [];
      const state = current();
      if (state.inputCaptureInstalled) return;
      state.inputCaptureInstalled = true;
      const send = state.ws.send.bind(state.ws);
      state.ws.send = value => { const message = JSON.parse(value); if (message.type === 'input') terminalInputFrames.push(message.data); return send(value); };
    });
  }
  async function clipboard(text, kind = 'shortcut') {
    await page.evaluate(text => navigator.clipboard.writeText(text), text);
    await page.evaluate(() => current().term.focus());
    if (kind === 'native') {
      // Invoke Chromium's real editing command. Terminal Ctrl+V is normally
      // readline's quoted-insert shortcut, so it is not a native paste trigger.
      const cdp = await page.createCDPSession();
      await cdp.send('Input.dispatchKeyEvent', { type: 'rawKeyDown', key: 'Unidentified', commands: ['Paste'] });
      await cdp.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Unidentified' });
      await cdp.detach();
    } else if (kind === 'menu') {
      await page.click('.terminal-session:not([hidden]) .xterm-screen', { button: 'right' });
      await page.click('#paste-terminal-clipboard');
    } else {
      await page.keyboard.down('Control');
      if (kind === 'shortcut') await page.keyboard.down('Shift');
      await page.keyboard.press('KeyV');
      if (kind === 'shortcut') await page.keyboard.up('Shift');
      await page.keyboard.up('Control');
    }
  }
  async function interrupt() {
    await page.evaluate(() => current().term.focus());
    await page.keyboard.down('Control'); await page.keyboard.press('KeyC'); await page.keyboard.up('Control'); await prompt();
  }
  async function received(name) {
    for (let attempt = 0; attempt < 100 && !exists(name); attempt++) await pause(100);
    assert(exists(name), 'Remote execution did not produce ' + name);
    await prompt();
  }
  const firstID = await connect();
  assert(await page.evaluate(() => current().term.modes.bracketedPasteMode));
  const content = '[renewalparams]\npre_hook = /usr/bin/docker compose -f /root/my-lookingglass/docker-compose.yml stop nginx\npost_hook = /usr/bin/docker compose -f /root/my-lookingglass/docker-compose.yml start nginx\n';
  const script = `mkdir -p ${root}\ncat > ${root}/renewal.conf <<'EOF'\n${content}EOF\n\ncat ${root}/renewal.conf # verify heredoc contents\n\nprintf '%s' done > ${root}/heredoc-done`;
  await capture(); await clipboard(script); await received('heredoc-done');
  assert.equal(read('renewal.conf'), content);
  assert.equal(await page.evaluate(() => terminalInputFrames.filter(x => x === '\r').length), 1);
  checks.push('Ctrl+Shift+V executes a complete heredoc and final command without Enter, preserving exact file contents');

  for (const kind of ['native', 'menu']) {
    await capture(); await clipboard(`printf first >> ${root}/${kind}\r\nprintf second >> ${root}/${kind}\r\n`, kind); await received(kind);
    assert.equal(read(kind), 'firstsecond');
    assert.equal(await page.evaluate(() => terminalInputFrames.filter(x => x === '\r').length), 1);
  }
  checks.push('Native browser paste and right-click paste execute CRLF batches once, with a single submission outside bracketed-paste markers');

  const large = Array.from({ length: 1800 }, (_, i) => `${i} 中文 🚀 terminal paste`).join('\n') + '\n';
  await capture(); await clipboard(`cat > ${root}/large.txt <<'END_LARGE'\n${large}END_LARGE\nprintf done > ${root}/large-done`); await received('large-done');
  assert.equal(read('large.txt'), large);
  assert((await page.evaluate(() => terminalInputFrames.length)) > 2);
  assert.equal(await page.evaluate(() => terminalInputFrames.filter(x => x === '\r').length), 1);
  checks.push('Large UTF-8 heredoc crosses transport chunks intact and executes its last command once');

  await capture(); await clipboard(`printf single > ${root}/single`); await pause(350);
  assert.equal(exists('single'), false); assert.equal(await page.evaluate(() => terminalInputFrames.filter(x => x === '\r').length), 0); await interrupt();
  const bar = `printf first > ${root}/bar\nprintf second >> ${root}/bar`;
  await capture(); await page.focus('#command-input'); await page.evaluate(text => navigator.clipboard.writeText(text), bar);
  await page.keyboard.down('Control'); await page.keyboard.press('KeyV'); await page.keyboard.up('Control'); await pause(350);
  assert.equal(exists('bar'), false); assert.equal(await page.evaluate(() => terminalInputFrames.length), 0);
  await page.keyboard.press('Enter'); await received('bar'); assert.equal(read('bar'), 'firstsecond');
  await page.evaluate(body => DengCommandComposer.open(null, body), `printf first > ${root}/composer\nprintf second >> ${root}/composer`);
  assert.equal(await page.$eval('#composer-append-cr', e => e.checked), false);
  await capture(); await page.click('#composer-send'); await pause(350);
  assert.equal(exists('composer'), false); assert.equal(await page.evaluate(() => terminalInputFrames.filter(x => x === '\r').length), 0); await interrupt();
  checks.push('Single-line terminal paste, bottom input bar, and composer no-CR editing retain explicit execution');

  await command(`cat > ${root}/stdin`, false); await pause(400); await capture(); await clipboard('first\nsecond'); await pause(200);
  assert.equal(await page.evaluate(() => terminalInputFrames.filter(x => x === '\r').length), 0); await interrupt();
  checks.push('Pasting into a running program does not inject a submission Enter');

  await command('seq 1 700; sleep 60', false);
  await page.waitForFunction(() => current().term.buffer.active.baseY > 400);
  await page.evaluate(() => { current().term.scrollToTop(); current().term.select(0, 0, 5); });
  assert(await page.evaluate(() => current().term.buffer.active.viewportY < current().term.buffer.active.baseY));
  await capture(); await interrupt();
  assert(await page.evaluate(() => current().term.buffer.active.viewportY === current().term.buffer.active.baseY));
  assert.equal(await page.evaluate(() => current().term.hasSelection()), false);
  assert.equal(await page.evaluate(() => terminalInputFrames.filter(x => x === '\x03').length), 1);
  await page.evaluate(() => { current().term.scrollToTop(); current().term.select(0, 0, 5); current().term.focus(); });
  await capture(); const position = await page.evaluate(() => current().term.buffer.active.viewportY);
  await page.keyboard.down('Control'); await page.keyboard.down('Shift'); await page.keyboard.press('KeyC'); await page.keyboard.up('Shift'); await page.keyboard.up('Control');
  assert.equal(await page.evaluate(() => current().term.buffer.active.viewportY), position);
  assert.equal(await page.evaluate(() => terminalInputFrames.length), 0);
  checks.push('Ctrl+C clears selection, interrupts real SSH sleep, and follows the bottom; Ctrl+Shift+C does not scroll or interrupt');

  await connect(); await command('seq 1 500; sleep 60', false); await page.waitForFunction(() => current().term.buffer.active.baseY > 300);
  await page.evaluate(() => current().term.scrollToTop()); await interrupt();
  assert.equal(await page.evaluate(id => sessions.get(id).term.buffer.active.viewportY, firstID), position);
  checks.push('Ctrl+C affects only the focused SSH tab; the other tab keeps its scroll position');
  assert.deepEqual(errors, []);
  await page.screenshot({ path: stage + '/evidence/terminal-input-passed.png' });
  fs.writeFileSync(stage + '/evidence/browser-results.json', JSON.stringify({ checks, errors }, null, 2));
  console.log(JSON.stringify({ checks, errors }, null, 2));
} catch (error) {
  if (page) await page.screenshot({ path: stage + '/evidence/terminal-input-failure.png' });
  console.error({ checks, errors }); throw error;
} finally { await browser.close(); }
