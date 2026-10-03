import assert from 'node:assert/strict';
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath, pathToFileURL } from 'node:url';

// Real Chromium DOM/layout/security coverage. No model service, user settings,
// npm download, or external network is needed. Optional overrides:
// DENG_PUPPETEER_MODULE, CHROME_BIN (also CHROME_BINARY),
// DENG_AI_MARKDOWN_EVIDENCE, and DENG_HEADLESS=0 (use xvfb on Linux).
const require = createRequire(import.meta.url);
const project = fileURLToPath(new URL('..', import.meta.url));
const web = path.join(project, 'web');
const evidence = process.env.DENG_AI_MARKDOWN_EVIDENCE || fs.mkdtempSync(path.join(os.tmpdir(), 'dengshell-ai-markdown-'));
fs.mkdirSync(evidence, { recursive: true });
const read = name => fs.readFileSync(path.join(web, name), 'utf8');
const rendererSource = read('ai-markdown.js');
const checks = [], requests = [], errors = [];
let browser, origin;

async function loadPuppeteer() {
  const specified = process.env.DENG_PUPPETEER_MODULE || process.env.PUPPETEER_MODULE;
  for (const candidate of specified ? [specified] : ['puppeteer-core', 'puppeteer', path.join(os.homedir(), '.cache/dengshell-dev-tools/node_modules/puppeteer-core')]) {
    try { const module = await import(pathToFileURL(require.resolve(candidate)).href); return module.default || module; }
    catch (error) { if (specified) throw new Error(`Cannot load DENG_PUPPETEER_MODULE=${specified}: ${error.message}`); }
  }
  throw new Error('Puppeteer is unavailable. Set DENG_PUPPETEER_MODULE to an existing puppeteer-core entry/module directory; this test does not install dependencies.');
}
function chromePath() {
  const specified = process.env.CHROME_BIN || process.env.CHROME_BINARY || process.env.DENG_CHROME_BIN;
  if (specified) return specified;
  return ['/usr/bin/google-chrome', '/usr/bin/chromium', '/usr/bin/chromium-browser', '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', ...['PROGRAMFILES', 'PROGRAMFILES(X86)', 'LOCALAPPDATA'].map(key => process.env[key] && path.join(process.env[key], 'Google/Chrome/Application/chrome.exe'))].find(value => value && fs.existsSync(value));
}

const fixture = mode => `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>AI Markdown regression</title>
<link rel="stylesheet" href="/ai-assistant.css"><link rel="stylesheet" href="/ai-markdown.css">
<style>*{box-sizing:border-box}body{margin:0;background:#f5f7fa;color:#233749;font:14px/1.7 system-ui,sans-serif;--text:#233749;--text-secondary:#64748b;--border:#cbd5e1;--ai-well:#fff;--accent:#397da3}.fixture-panel{width:min(440px,100%);padding:12px}.ai-conversation{padding:0;overflow:auto;border:0}.ai-message{margin:0}.dark{background:#111b28;color:#d6e2f1;--text:#d6e2f1;--text-secondary:#9bafc5;--border:#34465c;--ai-well:#1a293b;--accent:#80bcdf}</style>
${mode === 'missing' ? '' : '<script src="/vendor/markdown-it.js"></script>'}
${mode === 'throws' ? '<script>const originalMarkdownIt=window.markdownit;window.markdownit=(...args)=>{const parser=originalMarkdownIt(...args);parser.parse=()=>{throw new Error("intentional parser failure")};return parser};</script>' : ''}
<script src="/ai-markdown.js"></script></head><body><main class="fixture-panel"><div class="ai-conversation"><article class="ai-message ai-message-assistant"><div id="output" class="ai-message-content"></div></article></div></main></body></html>`;
const allowedFiles = new Set(['vendor/markdown-it.js', 'ai-markdown.js', 'ai-markdown.css', 'ai-assistant.css']);
const server = http.createServer((req, res) => {
  const url = new URL(req.url, 'http://fixture.invalid');
  res.setHeader('Cache-Control', 'no-store');
  if (url.pathname === '/') { res.setHeader('Content-Type', 'text/html; charset=utf-8'); res.end(fixture(url.searchParams.get('parser'))); return; }
  if (url.pathname === '/favicon.ico') { res.writeHead(204); res.end(); return; }
  const name = url.pathname.slice(1);
  if (allowedFiles.has(name)) { res.setHeader('Content-Type', name.endsWith('.css') ? 'text/css' : 'text/javascript'); res.end(read(name)); return; }
  requests.push({ url: req.url, type: 'unexpected-local' }); res.writeHead(404); res.end();
});

async function newPage(mode = '') {
  const page = await browser.newPage();
  await page.setViewport({ width: 460, height: 1000, deviceScaleFactor: 1 });
  await page.setRequestInterception(true);
  page.on('request', request => {
    if (request.url().startsWith(origin + '/')) void request.continue();
    else { requests.push({ url: request.url(), type: request.resourceType() }); void request.abort(); }
  });
  page.on('pageerror', error => errors.push(error.message));
  page.on('dialog', dialog => { errors.push('unexpected JavaScript dialog'); void dialog.dismiss(); });
  await page.evaluateOnNewDocument(() => {
    window.__markdownXSS = 0; window.__markdownSinkHits = []; window.__markdownOpened = [];
    const fail = name => { window.__markdownSinkHits.push(name); throw new Error('Forbidden HTML/execution sink: ' + name); };
    for (const property of ['innerHTML', 'outerHTML']) {
      const descriptor = Object.getOwnPropertyDescriptor(Element.prototype, property);
      Object.defineProperty(Element.prototype, property, { ...descriptor, set() { fail(property); } });
    }
    Element.prototype.insertAdjacentHTML = () => fail('insertAdjacentHTML');
    Range.prototype.createContextualFragment = () => fail('createContextualFragment');
    Document.prototype.write = () => fail('document.write'); Document.prototype.writeln = () => fail('document.writeln');
    window.open = url => { window.__markdownOpened.push(String(url)); return null; };
  });
  await page.goto(origin + '/?parser=' + mode, { waitUntil: 'networkidle0' });
  await page.waitForFunction(() => typeof window.DengAIMarkdown?.render === 'function');
  return page;
}

async function render(page, text) {
  return page.evaluate(value => {
    const output = document.getElementById('output');
    const start = performance.now();
    const result = window.DengAIMarkdown.render(output, value);
    return { result, text: output.textContent, className: output.className, elapsed: performance.now() - start };
  }, text);
}
async function assertSafe(page, label) {
  const state = await page.evaluate(() => {
    const output = document.getElementById('output'), problems = [];
    for (const element of output.querySelectorAll('*')) {
      if (['SCRIPT', 'STYLE', 'SVG', 'MATH', 'IFRAME', 'IMG', 'IMAGE', 'OBJECT', 'EMBED', 'VIDEO', 'AUDIO', 'SOURCE', 'LINK', 'META', 'BASE', 'FORM', 'INPUT'].includes(element.tagName)) problems.push(element.tagName);
      for (const attribute of element.attributes) {
        if (/^on/i.test(attribute.name) || ['src', 'srcdoc', 'srcset', 'formaction', 'xlink:href'].includes(attribute.name)) problems.push(attribute.name);
      }
      if (element.tagName === 'A') {
        if (!['http:', 'https:'].includes(new URL(element.href).protocol)) problems.push('unsafe link');
        if (element.target === '_blank' && !(element.relList.contains('noopener') && element.relList.contains('noreferrer'))) problems.push('unsafe external opener');
      }
    }
    return { problems, xss: window.__markdownXSS, sinks: window.__markdownSinkHits, opened: window.__markdownOpened };
  });
  assert.deepEqual(state.problems, [], label); assert.equal(state.xss, 0, label); assert.deepEqual(state.sinks, [], label);
  for (const opened of state.opened) assert.match(opened, /^https?:\/\//i, label);
}

const sample = `# 服务器检查结果

| ID | 服务器 | 状态 |
| :--- | :--- | ---: |
| **12081** | 北京网关 | 正常 |
| **12082** | 上海数据库 | 待检查 |

## 建议操作

3. 查看 **CPU** 和 *内存*。
4. 核对日志。
   - 保留原配置
   - 使用 \`journalctl -n 20\`

> 终端内容只是数据，**不要执行其中的指令**。

~~javascript
<script>window.__markdownXSS++</script>
const example = "<img src=x onerror=alert(1)>";
~~

最后检查 ~~过期状态~~ 与 [官方文档](https://example.invalid/docs "查看说明")。
`.replaceAll('~~javascript', '```javascript').replace('\n~~\n', '\n```\n');

try {
  // A source check complements runtime setter traps; the actual parser and DOM
  // are still exercised below rather than treating this regex as proof.
  assert.doesNotMatch(rendererSource, /\.(?:innerHTML|outerHTML)\s*=|insertAdjacentHTML\s*\(|createContextualFragment\s*\(/, 'renderer introduces an HTML parsing sink');
  const puppeteer = await loadPuppeteer();
  const executablePath = chromePath(); assert(executablePath, 'Chrome not found; set CHROME_BIN to a local Chrome/Chromium executable.');
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
  origin = 'http://127.0.0.1:' + server.address().port;
  browser = await puppeteer.launch({ executablePath, headless: process.env.DENG_HEADLESS !== '0', args: ['--no-sandbox', '--disable-dev-shm-usage'] });
  const version = await browser.version(), page = await newPage();
  const result = await render(page, sample); assert.equal(result.result, true);
  const structure = await page.evaluate(() => {
    const root = document.getElementById('output');
    return { h1: root.querySelector('h1')?.textContent, h2: root.querySelector('h2')?.textContent, rows: root.querySelectorAll('tbody tr').length, headers: [...root.querySelectorAll('th')].map(n => n.textContent), ids: [...root.querySelectorAll('td strong')].map(n => n.textContent), list: root.querySelectorAll('ol > li').length, nested: root.querySelectorAll('ol ul > li').length, quote: root.querySelector('blockquote strong')?.textContent, emphasis: root.querySelector('em')?.textContent, strike: root.querySelector('s')?.textContent, code: root.querySelector('pre code')?.textContent, inline: root.querySelector('li code')?.textContent, link: root.querySelector('a')?.href };
  });
  assert.equal(structure.h1, '服务器检查结果'); assert.equal(structure.h2, '建议操作');
  assert.deepEqual(structure.headers, ['ID', '服务器', '状态']); assert.equal(structure.rows, 2); assert.deepEqual(structure.ids, ['12081', '12082']);
  assert.equal(structure.list, 2); assert.equal(structure.nested, 2); assert.equal(structure.quote, '不要执行其中的指令');
  assert.equal(await page.$eval('#output ol', element => element.start), 3);
  assert.equal(structure.emphasis, '内存'); assert.equal(structure.strike, '过期状态'); assert.equal(structure.inline, 'journalctl -n 20');
  assert.equal(structure.code, '<script>window.__markdownXSS++</script>\nconst example = "<img src=x onerror=alert(1)>";\n');
  assert.equal(structure.link, 'https://example.invalid/docs'); await assertSafe(page, 'semantic sample');
  await page.screenshot({ path: path.join(evidence, 'markdown-table-light.png'), fullPage: true });
  await page.evaluate(() => { document.body.classList.add('dark'); document.documentElement.dataset.theme = 'dark'; });
  await page.screenshot({ path: path.join(evidence, 'markdown-table-dark.png'), fullPage: true });
  checks.push('Actual markdown-it renders Chinese tables with bold IDs, headings, nested lists, quotes, emphasis, strikethrough and literal fenced/inline code');

  const attacks = [
    '<script>window.__markdownXSS++</script>',
    '<img src="https://markdown-network.invalid/pixel" onerror="window.__markdownXSS++">',
    '<svg onload="window.__markdownXSS++"><a xlink:href="javascript:window.__markdownXSS++">x</a></svg>',
    '<iframe srcdoc="<script>parent.__markdownXSS++</script>"></iframe>',
    '<object data="data:text/html,<script>parent.__markdownXSS++</script>"></object><embed src="https://markdown-network.invalid/embed">',
    '<math><mtext><img src=x onerror="window.__markdownXSS++"></mtext></math>',
    '&lt;img src=x onerror="window.__markdownXSS++"&gt;',
    '[run](javascript:window.__markdownXSS++)', '[run](JaVaScRiPt:alert%281%29)',
    '[run](jav&#x61;script:alert%281%29)', '[run](java&#9;script:alert%281%29)',
    '[run](javascript%3Aalert%281%29)', '[run](%6a%61%76%61%73%63%72%69%70%74%3aalert%281%29)',
    '[run](data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==)', '[run](vbscript:msgbox%281%29)',
    '[run](file:///etc/passwd)', '[run](mailto:fixture@example.invalid)', '[run](//markdown-network.invalid/protocol-relative)',
    '![external image](https://markdown-network.invalid/pixel.png "title")', '![data image](data:image/png;base64,iVBORw0KGgo=)',
    '[safe title](https://example.invalid/title "hello\\\" onmouseover=\\\"window.__markdownXSS++")',
    '[attribute](https://example.invalid/%22%20onmouseover%3D%22window.__markdownXSS++)',
    '```html\" onmouseover=\"window.__markdownXSS++\n<img src=x onerror="window.__markdownXSS++">\n```',
  ];
  for (const [index, payload] of attacks.entries()) {
    await render(page, payload); await assertSafe(page, 'attack ' + index);
    const anchors = await page.$$eval('#output a', elements => elements.map(element => element.href));
    if (index >= 7 && index <= 17) assert.deepEqual(anchors, [], 'unsafe/encoded link case ' + index);
  }
  assert.equal(requests.length, 0, 'rendered content attempted automatic network access');
  checks.push('HTML/SVG/iframe/object/math/image payloads, event/title attribute injection and encoded unsafe URLs cannot create executable content or fetch remote media');

  await render(page, '[HTTP](http://example.invalid/read) and [HTTPS](https://example.invalid/read)');
  await page.evaluate(() => {
    window.__markdownNativeLinks = [];
    window.runtime = { BrowserOpenURL: url => window.__markdownNativeLinks.push(url) };
    for (const link of document.querySelectorAll('#output a')) link.click();
  });
  assert.deepEqual(await page.evaluate(() => window.__markdownNativeLinks), [], 'synthetic link click opened native browser');
  const links = await page.$$('#output a');
  await links[0].click(); await links[1].click({ button: 'middle' });
  assert.deepEqual(await page.evaluate(() => window.__markdownNativeLinks), ['http://example.invalid/read', 'https://example.invalid/read']);
  await links[0].click({ button: 'right' });
  assert.equal(await page.evaluate(() => window.__markdownNativeLinks.length), 2, 'right click opened native browser');
  await assertSafe(page, 'link clicks');
  checks.push('Only trusted left/middle clicks on HTTP/HTTPS links invoke the native external-browser bridge; synthetic/right clicks do not');

  for (const incomplete of ['**unfinished emphasis', '[unfinished link](javascript:', '```js\n<script>window.__markdownXSS++</script>', '| ID | 状态\n| **42** | 正常']) {
    const rendered = await render(page, incomplete); assert(rendered.text.length > 0, 'lost source text: ' + JSON.stringify({ input: incomplete, result: rendered })); await assertSafe(page, 'incomplete/nested markdown');
  }
  for (const deeplyNested of ['> '.repeat(100) + 'nested text', '> - '.repeat(40) + 'mixed nested text', Array.from({ length: 40 }, (_, i) => '  '.repeat(i) + '- nested ' + i).join('\n')]) {
    const nested = await render(page, deeplyNested); assert.equal(nested.result, false, 'parser nesting limit was reported as successful rendering'); assert.equal(nested.text, deeplyNested, 'deep content was truncated'); await assertSafe(page, 'deep nesting fallback');
  }
  const huge = '长内容 <img src=x onerror="window.__markdownXSS++">\n'.repeat(5000);
  const hugeResult = await render(page, huge); assert.equal(hugeResult.result, false); assert.equal(hugeResult.text, huge); assert.match(hugeResult.className, /ai-markdown-fallback/); await assertSafe(page, 'oversize fallback');
  const dense = '- item\n'.repeat(3000);
  const denseResult = await render(page, dense); assert.equal(denseResult.result, false); assert.equal(denseResult.text, dense); await assertSafe(page, 'token/node budget');
  const restored = await render(page, '**valid again**'); assert.equal(restored.result, true); assert.equal(await page.$eval('#output strong', n => n.textContent), 'valid again');
  assert.doesNotMatch(restored.className, /ai-markdown-fallback/);
  const nearLimit = '长内容及标点 '.repeat(10000);
  const nearLimitResult = await render(page, nearLimit); assert.equal(nearLimitResult.result, true); assert(nearLimitResult.text.includes('长内容及标点'));
  assert(nearLimitResult.elapsed < 5000, `bounded plain-text rendering took ${nearLimitResult.elapsed}ms`);
  checks.push('Incomplete/deep nesting stays safe; oversized and excessive-node input falls back to exact text, then subsequent valid rendering recovers');

  const wide = '| ' + Array.from({ length: 12 }, (_, i) => '列' + i).join(' | ') + ' |\n|' + Array(12).fill('---').join('|') + '|\n| ' + Array(12).fill('**中文编号** ' + 'LongUnbrokenIdentifier'.repeat(8)).join(' | ') + ' |\n\n```text\n' + 'very_long_terminal_output_'.repeat(150) + '\n```\n\n' + 'longprose'.repeat(300);
  for (const width of [360, 460]) {
    await page.setViewport({ width, height: 900, deviceScaleFactor: 1 });
    for (const depth of [20, 30]) {
      await render(page, '> '.repeat(depth) + 'nested content');
      const nestingWidth = await page.$eval('#output', node => ({ width: node.clientWidth, scrollWidth: node.scrollWidth }));
      assert(nestingWidth.scrollWidth <= nestingWidth.width + 1, `nested quote padding escaped ${width}px window at depth ${depth}: ${JSON.stringify(nestingWidth)}`);
    }
    await render(page, wide);
    const layout = await page.evaluate(() => {
      const root = document.getElementById('output');
      return { viewport: innerWidth, documentWidth: document.documentElement.scrollWidth, contentWidth: root.clientWidth, table: [...root.querySelectorAll('.ai-markdown-table')].map(n => ({ width: n.clientWidth, scrollWidth: n.scrollWidth, overflow: getComputedStyle(n).overflowX })), code: [...root.querySelectorAll('.ai-markdown-code')].map(n => ({ width: n.clientWidth, scrollWidth: n.scrollWidth, overflow: getComputedStyle(n).overflowX })) };
    });
    assert(layout.documentWidth <= width + 1, `content escaped ${width}px viewport: ${JSON.stringify(layout)}`);
    assert(layout.table.length && layout.code.length, 'table/code scrolling containers missing');
    for (const box of [...layout.table, ...layout.code]) { assert(box.width <= layout.contentWidth + 1); assert(['auto', 'scroll'].includes(box.overflow)); }
    assert(layout.table.some(box => box.scrollWidth > box.width), 'wide table does not expose horizontal scrolling');
    await page.screenshot({ path: path.join(evidence, `markdown-wide-${width}.png`), fullPage: true });
  }
  checks.push('Long prose/code and wide tables remain within 360px/460px windows with local horizontal scrolling');

  for (const mode of ['missing', 'throws']) {
    const fallbackPage = await newPage(mode), text = '**原样内容**\n<script>window.__markdownXSS++</script>';
    const fallback = await render(fallbackPage, text); assert.equal(fallback.result, false); assert.equal(fallback.text, text); assert.match(fallback.className, /ai-markdown-fallback/); await assertSafe(fallbackPage, mode + ' parser');
    await fallbackPage.close();
  }
  checks.push('Missing or throwing parser produces exact safe text in a real browser');
  await assertSafe(page, 'final'); assert.deepEqual(requests, []); assert.deepEqual(errors, []);
  const summary = { version, checks, passed: checks.length, externalOrUnexpectedRequests: requests, pageErrors: errors, evidence, screenshotFiles: ['markdown-table-light.png', 'markdown-table-dark.png', 'markdown-wide-360.png', 'markdown-wide-460.png'] };
  fs.rmSync(path.join(evidence, 'failure.json'), { force: true });
  fs.writeFileSync(path.join(evidence, 'result.json'), JSON.stringify(summary, null, 2) + '\n');
  console.log(JSON.stringify(summary, null, 2));
} catch (error) {
  fs.rmSync(path.join(evidence, 'result.json'), { force: true });
  fs.writeFileSync(path.join(evidence, 'failure.json'), JSON.stringify({ error: error.stack || String(error), checks, requests, pageErrors: errors }, null, 2) + '\n');
  throw error;
} finally {
  if (browser) await browser.close();
  await new Promise(resolve => server.close(resolve));
}
