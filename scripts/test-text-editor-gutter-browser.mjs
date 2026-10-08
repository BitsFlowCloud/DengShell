import fs from 'node:fs';
import assert from 'node:assert/strict';
import puppeteer from '/home/bitsflow/.cache/dengshell-dev-tools/node_modules/puppeteer-core/lib/puppeteer/puppeteer-core.js';

// Start TestFontLibraryBrowserFixture with DENG_FONT_QA_DIR pointing here.
// All remote files below are intercepted fixtures; no SSH server is contacted.
const stage = process.env.DENG_EDITOR_QA_DIR;
assert(stage, 'DENG_EDITOR_QA_DIR must point to an isolated fixture directory');
const fixture = JSON.parse(fs.readFileSync(stage + '/browser-fixture.json'));
const origin = new URL(fixture.url).origin;
assert.equal(new URL(origin).hostname, '127.0.0.1');
const contents = new Map([['gutter-a', 'first\r\n\r\nthird\r'], ['gutter-b', '']]);
const saves = [], errors = [], checks = [], measurements = [], largeLayouts = [];
const browser = await puppeteer.launch({executablePath: '/usr/bin/google-chrome', headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage']});
let page;
try {
  page = await browser.newPage();
  page.on('pageerror', error => errors.push(error.message));
  await page.setRequestInterception(true);
  page.on('request', request => {
    const url = new URL(request.url()), match = url.pathname.match(/^\/api\/sessions\/(gutter-[ab])\/file-content$/);
    if (match) {
      if (request.method() === 'POST') { const data = JSON.parse(request.postData()); saves.push(data); contents.set(match[1], data.text); }
      request.respond({status: 200, contentType: 'application/json', body: JSON.stringify({text: contents.get(match[1]), encoding: 'utf-8', sha256: 'a'.repeat(64)})});
    } else if (/^https?:$/.test(url.protocol) && url.origin !== origin) request.abort();
    else request.continue();
  });
  await fetch(origin + '/api/appearance/patch', {method: 'POST', headers: {'Content-Type': 'application/json', 'X-CloudShell-Token': fixture.token}, body: JSON.stringify({startupAnimation: false, onboardingCompleted: true, uiScale: 1})});
  await page.setViewport({width: 1440, height: 1000});
  await page.goto(fixture.url, {waitUntil: 'networkidle0'});
  await page.evaluate(async () => {
    document.querySelectorAll('dialog[open]').forEach(e => e.close());
    await chooseAppearance({startupAnimation: false, onboardingCompleted: true, uiScale: 1}); setDrawer(false);
    // Capture the real native clipboard bridge without depending on OS clipboard access.
    window.go = {main: {Desktop: {
      WriteClipboard(text) {window.gutterClipboard = text; return Promise.resolve();},
      ReadClipboard() {return Promise.resolve(window.gutterPaste);},
      async Request(method, path, body) {
        const response = await fetch(endpoint(path), {method, headers: {'Content-Type': 'application/json', 'X-CloudShell-Token': boot.token}, body: body || undefined});
        if (!response.ok) throw new Error('Fixture request failed: ' + path);
        return response.text();
      }
    }}};
    for (const id of ['a', 'b']) {
      profiles.push({id: 'gutter-owner-' + id, name: '行号测试 ' + id, user: 'fixture', host: 'test-' + id, port: 22});
      const state = {id: 'gutter-' + id, profileId: 'gutter-owner-' + id, connected: true, closed: false};
      sessions.set(state.id, state); await DengTextEditors.openText(state, '/etc/config.txt');
    }
  });
  const active = '.text-editor-dialog[open] .text-editor-panel:not([hidden])';
  const area = active + ' .text-editor-area', gutter = active + ' .text-editor-gutter';
  const frames = () => page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  const key = async name => {await page.keyboard.down('Control'); await page.keyboard.press(name); await page.keyboard.up('Control'); await frames();};
  const set = (selector, value) => page.$eval(selector, (element, value) => {element.value = value; element.dispatchEvent(new Event('input', {bubbles: true}));}, value);
  const click = (selector, text) => page.$$eval(selector, (elements, text) => elements.find(e => e.textContent === text).click(), text);
  const tab = async id => {await page.evaluate(id => [...document.querySelectorAll('[role=tab]')].find(e => e.textContent.includes('行号测试 ' + id)).click(), id); await frames();};
  const numbers = async () => {await frames(); return page.$eval(gutter, e => e.textContent.trim().split(/\s+/).map(Number));};
  assert(await page.$(gutter), 'Issue #31: the editor must show a line-number gutter beside the text');
  assert.deepEqual(await numbers(), [1], 'An empty file still has line 1');
  await tab('a'); assert.deepEqual(await numbers(), [1, 2, 3, 4], 'Blank lines, CRLF/CR and a trailing newline must count');
  assert.equal(await page.$eval(gutter, e => e.getAttribute('aria-hidden')), 'true');
  checks.push('Initial empty file and mixed CRLF/CR blank/trailing lines');

  await page.focus(area); await page.keyboard.press('End'); await page.keyboard.press('Enter');
  assert.equal((await numbers()).length, 5);
  await key('z'); assert.equal((await numbers()).length, 4);
  await key('y'); assert.equal((await numbers()).length, 5);
  await page.keyboard.press('Backspace'); assert.equal((await numbers()).length, 4);
  await page.evaluate(selector => {const e = document.querySelector(selector); e.setSelectionRange(0, e.value.length); window.gutterPaste = 'pasted\r\n\nlast\r\n'; e.dispatchEvent(new Event('paste', {bubbles: true, cancelable: true}));}, area);
  await page.waitForFunction(selector => document.querySelector(selector).value.startsWith('pasted'), {}, area);
  assert.deepEqual(await numbers(), [1, 2, 3, 4]);
  await key('s'); await page.waitForFunction(selector => document.querySelector(selector + ' .primary-button').disabled, {}, active);
  assert.equal(saves.at(-1).text, 'pasted\r\n\nlast\r\n', 'Saving must retain exact newline bytes and exclude line numbers');
  await page.click(active + ' .text-editor-more summary'); await click(active + ' .text-editor-more-body button', '复制全文');
  assert.equal(await page.evaluate(() => window.gutterClipboard), saves.at(-1).text);
  await page.focus(area); await key('h'); await set(active + ' .text-editor-find', 'last'); await set(active + ' .text-editor-replacement', '');
  await click(active + ' .text-editor-replace-row button', '全部替换'); assert.deepEqual(await numbers(), [1, 2, 3, 4]);
  await page.focus(area); await key('z'); assert.equal(await page.$eval(area, e => e.value), 'pasted\n\nlast\n');
  await page.keyboard.press('Escape');
  contents.set('gutter-a', 'reloaded\nend');
  await page.click(active + ' .text-editor-more summary'); await click(active + ' .text-editor-more-body button', '重新读取');
  await page.waitForFunction(selector => document.querySelector(selector).value === 'reloaded\nend', {}, area);
  assert.deepEqual(await numbers(), [1, 2]);
  checks.push('Typing, undo/redo, raw clipboard paste/copy/save, replace-all and asynchronous reload');

  await tab('b'); assert.deepEqual(await numbers(), [1]);
  await tab('a');
  const large = Array.from({length: 200000}, (_, i) => 'line ' + (i + 1)).join('\n') + '\n';
  await set(area, large); await frames();
  assert((await numbers()).length < 100, 'A 200,001-line file must only render visible line numbers');
  await page.$eval(area, e => {e.scrollTop = 2500000;}); await frames();
  const middleNumbers = await numbers(); assert(middleNumbers[0] > 90000 && middleNumbers[0] < 150000);
  await page.$eval(area, e => {e.scrollTop = e.scrollHeight;}); await frames();
  assert.equal((await numbers()).at(-1), 200001, 'The last empty line must stay reachable');
  assert(await page.$eval(gutter, e => e.querySelectorAll('*').length < 100), 'Gutter DOM size must be bounded');
  await page.screenshot({path: stage + '/editor-gutter-200001-lines.png'});
  for (const scale of [1.1, 1.25, 1.5, 2]) {
    await page.evaluate(scale => {appearance.uiScale = scale; applyUIScale();}, scale); await frames();
    await page.$eval(area, e => {e.scrollTop = e.scrollHeight;}); await frames();
    const height = await page.$eval(area, e => {const s = getComputedStyle(e); return {actual: e.scrollHeight - parseFloat(s.paddingTop) - parseFloat(s.paddingBottom), expected: 200001 * parseFloat(s.lineHeight)};});
    const rendered = await numbers();
    assert.equal(rendered.at(-1), 200001, 'Zoom must not lose the last line in a large file: ' + JSON.stringify({scale, ...height}));
    // Range measures the actual first/last glyph boxes across the gutter;
    // compare its used line spacing with the textarea's complete extent.
    const gutterLineHeight = await page.$eval(gutter, e => {
      const text = e.firstElementChild.firstChild, value = text.textContent;
      const range = document.createRange(); range.setStart(text, 0); range.setEnd(text, 1);
      const firstY = range.getBoundingClientRect().top, last = value.lastIndexOf('\n') + 1;
      range.setStart(text, last); range.setEnd(text, last + 1);
      return (range.getBoundingClientRect().top - firstY) / (value.split('\n').length - 1) / effectiveScale;
    });
    assert(Math.abs(gutterLineHeight - height.actual / 200001) < 0.01, 'Gutter glyph spacing must match native textarea line boxes');
    largeLayouts.push({scale, actualLineHeight: height.actual / 200001, gutterLineHeight, renderedCount: rendered.length, first: rendered[0], last: rendered.at(-1)});
    if (scale === 1.1) await page.screenshot({path: stage + '/editor-gutter-200001-lines-110-percent.png'});
  }
  await page.evaluate(() => {appearance.uiScale = 1; applyUIScale();});
  checks.push('200,001-line document uses a bounded viewport and includes the last blank line');

  await set(area, Array.from({length: 300}, (_, i) => `line ${i + 1}: ` + (i === 120 ? 'long '.repeat(250) : '正文')).join('\n'));
  for (const [width, height, scale, theme] of [[1440,1000,1,'light'], [1024,768,1,'dark'], [480,640,1,'light'], [1280,900,1.25,'dark'], [1280,900,1.5,'dark'], [1440,1000,2,'light']]) {
    await page.setViewport({width, height});
    await page.evaluate(({scale, theme}) => {appearance.uiScale = scale; applyUIScale(); document.documentElement.dataset.theme = theme; window.dispatchEvent(new Event('resize'));}, {scale, theme});
    await page.$eval(area, e => {e.scrollTop = 2700.5; e.scrollLeft = 0;}); await frames();
    const before = await page.$eval(gutter, e => ({left: e.getBoundingClientRect().left, text: e.textContent}));
    await page.$eval(area, e => {e.scrollLeft = 1700;}); await frames();
    assert.deepEqual(await page.$eval(gutter, e => ({left: e.getBoundingClientRect().left, text: e.textContent})), before, 'Horizontal scroll cannot move the gutter');
    const result = await page.$eval(active, e => {
      const a = e.querySelector('textarea'), g = e.querySelector('.text-editor-gutter'), lines = g.firstElementChild;
      const style = getComputedStyle(a), rect = a.getBoundingClientRect(), zoom = rect.height / a.offsetHeight;
      const first = Number(lines.textContent.trim().split(/\s+/)[0]);
      const expectedY = rect.top + (parseFloat(style.borderTopWidth) + parseFloat(style.paddingTop) + (first - 1) * parseFloat(style.lineHeight) - a.scrollTop) * zoom;
      return {first, alignmentError: Math.abs(lines.getBoundingClientRect().top - expectedY), gutterWidth: g.getBoundingClientRect().width / zoom, areaWidth: rect.width / zoom, overflow: e.scrollWidth > e.clientWidth + 1};
    });
    assert(result.alignmentError < 2, JSON.stringify({width, height, scale, ...result}));
    assert(result.gutterWidth < 80 && result.areaWidth > 150 && !result.overflow);
    measurements.push({width, height, scale, theme, ...result});
    await page.$eval(area, e => {e.scrollLeft = 0;}); await frames();
    await page.screenshot({path: stage + `/editor-gutter-${width}-${scale}-${theme}.png`});
  }
  await page.setViewport({width: 1440, height: 1000}); await page.evaluate(() => {appearance.uiScale = 1; applyUIScale();});
  await tab('b'); assert.deepEqual(await numbers(), [1]);
  await page.click('.text-editor-window-menu summary'); await click('.text-editor-layout-tools button', '移到新窗口'); await frames();
  assert.equal(await page.$$eval('.text-editor-dialog[open]', es => es.length), 2);
  const windows = await page.$$eval('.text-editor-dialog[open]', es => es.map(e => ({text: e.querySelector('textarea').value, first: Number(e.querySelector('.text-editor-gutter').textContent.trim().split(/\s+/)[0])})));
  assert.equal(windows.find(w => !w.text).first, 1);
  assert(windows.find(w => w.text).first > 100, 'Moving another tab must preserve this document\'s gutter scroll');
  await page.evaluate(() => [...document.querySelectorAll('.text-editor-area')].find(e => !e.value).focus());
  await key('w');
  assert.equal(await page.$$eval('.text-editor-dialog[open]', es => es.length), 1);
  await page.evaluate(async () => {await DengTextEditors.openText(sessions.get('gutter-b'), '/etc/config.txt');});
  assert.deepEqual(await numbers(), [1], 'A closed document must reopen with a fresh gutter');
  checks.push('Vertical alignment, fixed horizontal gutter, narrow/zoomed layouts, light/dark themes and independent windows');
  assert.deepEqual(errors, []);
  fs.writeFileSync(stage + '/editor-gutter-browser.json', JSON.stringify({passed: true, checks, measurements, largeLayouts, errors}, null, 2));
  console.log(checks.map(check => 'PASS ' + check).join('\n'));
} catch (error) {
  if (page) {
    await page.screenshot({path: stage + '/editor-gutter-failure.png'});
    fs.writeFileSync(stage + '/editor-gutter-failure.json', JSON.stringify(await page.$$eval('.text-editor-panel', panels => panels.map(e => ({hidden: e.hidden, text: e.querySelector('textarea').value.slice(0,100), height: e.querySelector('textarea').clientHeight, lineHeight: getComputedStyle(e.querySelector('textarea')).lineHeight, gutter: e.querySelector('.text-editor-gutter')?.outerHTML}))), null, 2));
  }
  throw error;
} finally {await browser.close();}
