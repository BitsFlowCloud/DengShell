import fs from 'node:fs';
import assert from 'node:assert/strict';
import puppeteer from '/home/bitsflow/.cache/dengshell-dev-tools/node_modules/puppeteer-core/lib/puppeteer/puppeteer-core.js';

// Opt-in TestFontLibraryBrowserFixture, isolated data and intercepted remote files.
// All long values below are synthetic; this test never contacts an SSH server.
const stage = process.env.DENG_EDITOR_QA_DIR;
assert(stage, 'DENG_EDITOR_QA_DIR must name an isolated fixture directory');
const fixture = JSON.parse(fs.readFileSync(stage + '/browser-fixture.json'));
const origin = new URL(fixture.url).origin;
assert.equal(new URL(origin).hostname, '127.0.0.1');
const synthetic = 'sampleABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789'.repeat(80);
const raw = `TEST_VALUE=${synthetic}\r\n\r\n中文\t组合 e\u0301 👨‍👩‍👧‍👦 ${'中英 mixed\t'.repeat(60)}\r${'مرحبا بالعالم mixed 123 שלום\t'.repeat(25)}\nTAIL\n`;
const contents = new Map([['wrap-a', raw], ['wrap-b', '']]);
const saves = [], errors = [], checks = [], layouts = [], largeLayouts = [];
const browser = await puppeteer.launch({executablePath: '/usr/bin/google-chrome', headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage']});
let page;
try {
  page = await browser.newPage();
  page.on('pageerror', error => errors.push(error.message));
  await page.setRequestInterception(true);
  page.on('request', request => {
    const url = new URL(request.url()), match = url.pathname.match(/^\/api\/sessions\/(wrap-[ab])\/file-content$/);
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
    window.go = {main: {Desktop: {
      WriteClipboard(text) { window.wrapClipboard = text; return Promise.resolve(); },
      ReadClipboard() { return Promise.resolve(window.wrapPaste); },
      async Request(method, path, body) {
        const response = await fetch(endpoint(path), {method, headers: {'Content-Type': 'application/json', 'X-CloudShell-Token': boot.token}, body: body || undefined});
        if (!response.ok) throw new Error('Fixture request failed: ' + path);
        return response.text();
      }
    }}};
    for (const id of ['a', 'b']) {
      profiles.push({id: 'wrap-owner-' + id, name: '折行测试 ' + id, user: 'fixture', host: 'test-' + id, port: 22});
      const state = {id: 'wrap-' + id, profileId: 'wrap-owner-' + id, connected: true, closed: false};
      sessions.set(state.id, state); await DengTextEditors.openText(state, '/test/config.txt');
    }
  });
  const active = '.text-editor-dialog[open] .text-editor-panel:not([hidden])';
  const area = active + ' .text-editor-area', gutter = active + ' .text-editor-gutter';
  const frames = () => page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  const key = async name => { await page.keyboard.down('Control'); await page.keyboard.press(name); await page.keyboard.up('Control'); await frames(); };
  const set = async (selector, value) => { await page.$eval(selector, (element, value) => { element.value = value; element.dispatchEvent(new Event('input', {bubbles: true})); }, value); await frames(); };
  const click = (selector, label) => page.$$eval(selector, (elements, label) => elements.find(e => e.textContent === label).click(), label);
  const tab = async id => { await page.evaluate(id => [...document.querySelectorAll('[role=tab]')].find(e => e.textContent.includes('折行测试 ' + id)).click(), id); await frames(); };
  const numbers = async () => { await frames(); return page.$eval(gutter, e => [...e.querySelectorAll('[data-line]')].map(e => Number(e.dataset.line))); };

  // Independent reference: one block per logical line, not the production
  // single-text-node mirror or its viewport search. Only small samples use it.
  async function alignment(offset = null) {
    await frames();
    return page.$eval(active, (panel, offset) => {
      const a = panel.querySelector('textarea'), s = getComputedStyle(a), code = a.parentElement;
      const reference = document.createElement('div');
      const properties = ['fontFamily', 'fontSize', 'fontWeight', 'fontStyle', 'fontStretch', 'fontVariant', 'lineHeight', 'letterSpacing', 'wordSpacing', 'tabSize', 'textIndent', 'textTransform', 'textRendering', 'whiteSpace', 'wordBreak', 'overflowWrap', 'lineBreak', 'direction', 'unicodeBidi', 'boxSizing', 'paddingTop', 'paddingRight', 'paddingBottom', 'paddingLeft', 'borderTopWidth', 'borderRightWidth', 'borderBottomWidth', 'borderLeftWidth', 'borderStyle'];
      for (const property of properties) reference.style[property] = s[property];
      Object.assign(reference.style, {position: 'fixed', left: '-20000px', top: '0', width: s.width, height: 'auto', minHeight: '0', margin: '0', visibility: 'hidden', pointerEvents: 'none', overflowY: 'scroll', overflowX: 'hidden'});
      const values = a.value.split('\n');
      for (const value of values) { const row = document.createElement('div'); row.textContent = value || '\u200b'; reference.append(row); }
      code.append(reference);
      const referenceRect = reference.getBoundingClientRect(), areaRect = a.getBoundingClientRect(), zoom = typeof effectiveScale === 'number' ? effectiveScale : 1;
      const range = document.createRange();
      const glyph = (row, start = 0) => { const text = row.firstChild; range.setStart(text, Math.min(start, text.length - 1)); range.setEnd(text, Math.min(start + 1, text.length)); return range.getBoundingClientRect(); };
      const rows = [...panel.querySelectorAll('.text-editor-gutter [data-line]')].map(number => {
        const logicalLine = Number(number.dataset.line), expected = glyph(reference.children[logicalLine - 1]);
        range.selectNodeContents(number); const actual = range.getBoundingClientRect();
        return {line: logicalLine, error: Math.abs(actual.top - (areaRect.top + expected.top - referenceRect.top - a.scrollTop * zoom))};
      });
      let selected = null;
      if (offset !== null) {
        const prefix = a.value.slice(0, offset), logicalLine = prefix.split('\n').length - 1, column = offset - prefix.lastIndexOf('\n') - 1;
        const r = glyph(reference.children[logicalLine], column), y = areaRect.top + r.top - referenceRect.top - a.scrollTop * zoom;
        selected = {y, visible: y >= areaRect.top - 2 && y + r.height <= areaRect.bottom + 2, logicalLine: logicalLine + 1};
      }
      const result = {rows, selected, horizontalOverflow: a.scrollWidth - a.clientWidth, textScrollHeight: a.scrollHeight, referenceHeight: referenceRect.height / zoom, viewportHeight: a.clientHeight, content: a.value, renderedNumbers: rows.length, gutterNodes: panel.querySelectorAll('.text-editor-gutter *').length};
      reference.remove();
      return result;
    }, offset);
  }
  const assertAlignment = result => {
    assert(result.horizontalOverflow <= 1, 'Soft wrapping must not produce horizontal scrolling');
    assert(result.rows.every(row => row.error < 2), 'Logical line numbers must align after wrapping: ' + JSON.stringify(result.rows));
    assert(Math.abs(Math.max(result.viewportHeight, result.referenceHeight) - result.textScrollHeight) < 2, 'Native textarea and independent wrapped layout must agree');
  };

  assert.deepEqual(await numbers(), [1], 'An empty document retains logical line 1');
  await tab('a');
  assert.equal(await page.$eval(area, e => e.wrap), 'soft', 'Long lines should wrap automatically by default');
  assert.equal(await page.$eval(area, e => e.value), raw.replace(/\r\n|\r/g, '\n'));
  const initial = await alignment(); assertAlignment(initial);
  assert(initial.textScrollHeight > 5 * 24 + 20, 'A long value must visibly continue on multiple rows');
  await page.screenshot({path: stage + '/editor-wrap-long-value.png'});
  await page.click(active + ' .text-editor-more summary'); await click(active + ' .text-editor-more-body button', '复制全文');
  assert.equal(await page.evaluate(() => window.wrapClipboard), raw, 'Soft wrapping cannot modify copied raw newlines');
  checks.push('Automatic wrapping of long unbroken values, blank/trailing lines, tabs, Chinese, emoji, combining marks and RTL mixtures');

  // Edit and undo the very long first line, then raw paste/save and select-copy.
  await page.focus(area); await page.$eval(area, e => e.setSelectionRange(10, 10)); await page.keyboard.sendCharacter('EDIT'); await frames();
  await key('z'); assert.equal(await page.$eval(area, e => e.value), raw.replace(/\r\n|\r/g, '\n'));
  await key('y'); assert((await page.$eval(area, e => e.value)).includes('EDIT')); await key('z');
  const pasted = `START=${synthetic}\r\n\r\n${'中文\ttab '.repeat(90)}\rTRAIL\n`;
  await page.evaluate(({selector, text}) => { const a = document.querySelector(selector); a.setSelectionRange(0, a.value.length); window.wrapPaste = text; a.dispatchEvent(new Event('paste', {bubbles: true, cancelable: true})); }, {selector: area, text: pasted});
  await page.waitForFunction(selector => document.querySelector(selector).value.startsWith('START='), {}, area); await frames();
  await key('s'); await page.waitForFunction(selector => document.querySelector(selector + ' .primary-button').disabled, {}, active);
  assert.equal(saves.at(-1).text, pasted, 'Saving must preserve exact raw text and mixed CRLF/CR/LF');
  await page.$eval(area, e => { e.setSelectionRange(0, e.value.length); e.dispatchEvent(new Event('copy', {bubbles: true, cancelable: true})); });
  assert.equal(await page.evaluate(() => window.wrapClipboard), pasted);
  checks.push('Typing, undo/redo, raw clipboard paste/copy and save do not insert display wrap newlines');

  const mixed = Array.from({length: 730}, (_, i) => i === 0 ? 'LONG=' + synthetic : i === 63 ? 'prefix ' + 'unbroken'.repeat(170) + ' TARGET_IN_WRAPPED_ROW ' + '后续\t'.repeat(45) : [255, 511].includes(i) ? '边界\t' + '长行abc'.repeat(60) : i % 17 === 0 ? 'مرحبا 123 שלום abc '.repeat(30) : i % 9 === 0 ? '段落\t' + '中文 e\u0301 😀 '.repeat(25) : i % 13 === 0 ? '' : `line ${i + 1}: normal text`).join('\n') + '\n';
  await set(area, mixed);
  for (const [width, height, scale, theme] of [[1440,1000,1,'dark'], [1024,768,1,'light'], [480,640,1,'dark'], [1280,900,1.1,'light'], [1280,900,1.25,'dark'], [1280,900,1.5,'light'], [1440,1000,2,'dark']]) {
    await page.setViewport({width, height});
    await page.evaluate(({scale, theme}) => { appearance.uiScale = scale; applyUIScale(); document.documentElement.dataset.theme = theme; window.dispatchEvent(new Event('resize')); }, {scale, theme});
    await frames();
    for (const fraction of [0, 0.35, 0.7, 1]) {
      await page.$eval(area, (e, fraction) => { e.scrollTop = (e.scrollHeight - e.clientHeight) * fraction; }, fraction); await frames();
      const measured = await alignment(); assertAlignment(measured);
      if (fraction === 1) assert.equal((await numbers()).at(-1), 731, 'The last blank logical line must remain reachable');
      layouts.push({width, height, scale, theme, fraction, maxAlignmentError: Math.max(0, ...measured.rows.map(row => row.error)), renderedNumbers: measured.renderedNumbers, horizontalOverflow: measured.horizontalOverflow});
    }
    await page.screenshot({path: stage + `/editor-wrap-${width}-${scale}-${theme}.png`});
  }
  checks.push('Logical line alignment after wrapping, resizing, scrolling and 100/110/125/150/200 percent scaling in both themes');

  // Appearance changes should invalidate cached positions without editing text.
  const priorFont = await page.evaluate(() => document.documentElement.style.getPropertyValue('--terminal-family'));
  for (const family of ['serif', 'monospace']) {
    await page.evaluate(family => { document.documentElement.style.setProperty('--terminal-family', family); }, family);
    await frames();
    await page.$eval(area, e => { e.scrollTop = (e.scrollHeight - e.clientHeight) * 0.45; });
    const measured = await alignment(); assertAlignment(measured);
    layouts.push({family, maxAlignmentError: Math.max(0, ...measured.rows.map(row => row.error)), renderedNumbers: measured.renderedNumbers, horizontalOverflow: measured.horizontalOverflow});
  }
  await page.evaluate(value => { if (value) document.documentElement.style.setProperty('--terminal-family', value); else document.documentElement.style.removeProperty('--terminal-family'); }, priorFont);
  await frames();
  checks.push('Switching between proportional and monospace appearance fonts invalidates wrapped positions');

  // Search a match deep inside a wrapped logical line, then jump past it.
  await page.setViewport({width: 1280, height: 900});
  await page.evaluate(() => { appearance.uiScale = 1.25; applyUIScale(); }); await frames();
  await page.focus(area); await key('f'); await set(active + ' .text-editor-find', 'TARGET_IN_WRAPPED_ROW'); await page.keyboard.press('Enter'); await frames();
  let selected = await page.$eval(area, e => ({start: e.selectionStart, text: e.value.slice(e.selectionStart, e.selectionEnd)}));
  assert.equal(selected.text, 'TARGET_IN_WRAPPED_ROW');
  let measured = await alignment(selected.start); assertAlignment(measured); assert(measured.selected.visible, 'Search must reveal a match inside a wrapped logical line');
  await page.focus(area); await key('g'); await set(active + ' .text-editor-line', '100'); await page.keyboard.press('Enter'); await frames();
  selected = await page.$eval(area, e => ({start: e.selectionStart, text: e.value.slice(e.selectionStart, e.selectionEnd)}));
  assert.equal(selected.text, mixed.split('\n')[99]);
  measured = await alignment(selected.start); assertAlignment(measured); assert(measured.selected.visible, 'Go-to line must account for preceding wrapped rows');
  for (const logicalLine of [255, 256, 257, 511, 512, 513, 730]) {
    await page.focus(active + ' .text-editor-line'); await set(active + ' .text-editor-line', String(logicalLine)); await page.keyboard.press('Enter'); await frames();
    const selected = await page.$eval(area, e => ({start: e.selectionStart, text: e.value.slice(e.selectionStart, e.selectionEnd)}));
    assert.equal(selected.text, mixed.split('\n')[logicalLine - 1]);
    const measured = await alignment(selected.start); assertAlignment(measured);
    assert(measured.selected.visible, `Logical line ${logicalLine} must be visible across layout chunk boundaries`);
  }
  await page.keyboard.press('Escape');
  checks.push('Search reveals wrapped matches; go-to remains exact at logical lines 100/255/256/257/511/512/513/730');

  // Keep the large-file test independent of a huge second reference DOM.
  await page.setViewport({width: 1440, height: 1000}); await page.evaluate(() => { appearance.uiScale = 1; applyUIScale(); });
  const large = Array.from({length: 200000}, (_, i) => i % 10001 === 0 ? 'long ' + 'x'.repeat(600) : `line ${i + 1}`).join('\n') + '\n';
  const start = performance.now(); await set(area, large); const loadMs = performance.now() - start;
  assert(loadMs < 15000, 'A 200,001-line fixture must remain usable');
  for (const scale of [1, 1.1, 1.25, 2]) {
    await page.evaluate(scale => { appearance.uiScale = scale; applyUIScale(); }, scale); await frames();
    for (const fraction of [0.5, 1]) {
      const before = performance.now(); await page.$eval(area, (e, fraction) => { e.scrollTop = (e.scrollHeight - e.clientHeight) * fraction; }, fraction); await frames();
      const rendered = await numbers();
      const state = await page.$eval(active, panel => { const a = panel.querySelector('textarea'); return {nodes: panel.querySelectorAll('.text-editor-gutter *').length, allEditorNodes: panel.querySelectorAll('*').length, overflow: a.scrollWidth - a.clientWidth}; });
      assert(rendered.length < 150 && state.nodes < 200 && state.allEditorNodes < 500, '200k lines must retain bounded editor and gutter DOM');
      assert(state.overflow <= 1);
      if (fraction === 1) assert.equal(rendered.at(-1), 200001, 'Large-file trailing empty line must survive wrapping and zoom');
      else assert(rendered[0] > 90000 && rendered[0] < 110000, 'Middle scroll must show the correct logical line region');
      const scrollMs = performance.now() - before; assert(scrollMs < 4000, 'Large-file scrolling must not remeasure each logical line');
      largeLayouts.push({scale, fraction, loadMs, scrollMs, first: rendered[0], last: rendered.at(-1), rendered: rendered.length, ...state});
    }
    const nearbyScrollMs = await page.$eval(area, async e => {
      const times = [];
      for (let step = 0; step < 5; step++) {
        const start = performance.now(); e.scrollTop -= 24;
        await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
        times.push(performance.now() - start);
      }
      return times;
    });
    assert(nearbyScrollMs.every(ms => ms < 2000), 'Nearby scrolling must reuse cached line positions');
    largeLayouts.push({scale, nearbyScrollMs});
    if (scale === 1) {
      const nativeScrollMs = await page.$eval(area, async original => {
        // Clone only the native textarea, without editor listeners or a gutter
        // refresh, to distinguish its rendering cost from layout measurement.
        const copy = original.cloneNode(); copy.value = original.value;
        const previousDisplay = original.style.display;
        original.before(copy); original.style.display = 'none'; copy.scrollTop = copy.scrollHeight;
        await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
        const times = [];
        for (let step = 0; step < 5; step++) {
          const start = performance.now(); copy.scrollTop -= 24;
          await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
          times.push(performance.now() - start);
        }
        copy.remove(); original.style.display = previousDisplay;
        return times;
      });
      largeLayouts.push({scale, nativeScrollMs}); await frames();
    }
    await page.$eval(area, e => { e.scrollTop = e.scrollHeight; }); await frames();
  }
  await page.screenshot({path: stage + '/editor-wrap-200001-lines.png'});
  checks.push('200,001 logical lines keep bounded DOM, reachable last empty line and usable scrolling after scaling');
  await tab('b'); assert.deepEqual(await numbers(), [1]);
  await page.click('.text-editor-window-menu summary'); await click('.text-editor-layout-tools button', '移到新窗口'); await frames();
  assert.equal(await page.$$eval('.text-editor-dialog[open]', es => es.length), 2);
  const states = await page.$$eval('.text-editor-dialog[open]', es => es.map(e => ({empty: !e.querySelector('textarea').value, numbers: [...e.querySelectorAll('.text-editor-gutter [data-line]')].map(e => Number(e.dataset.line))})));
  assert.deepEqual(states.find(s => s.empty).numbers, [1]); assert.equal(states.find(s => !s.empty).numbers.at(-1), 200001);
  checks.push('Independent tabs and detached windows retain their own wrapped layout and scroll position');
  assert.deepEqual(errors, []);
  fs.writeFileSync(stage + '/editor-wrap-browser.json', JSON.stringify({passed: true, checks, layouts, largeLayouts, errors}, null, 2));
  console.log(checks.map(check => 'PASS ' + check).join('\n'));
} catch (error) {
  if (page) await page.screenshot({path: stage + '/editor-wrap-failure.png'});
  fs.writeFileSync(stage + '/editor-wrap-browser.json', JSON.stringify({passed: false, error: error.message, checks, layouts, largeLayouts, errors}, null, 2));
  throw error;
} finally { await browser.close(); }
