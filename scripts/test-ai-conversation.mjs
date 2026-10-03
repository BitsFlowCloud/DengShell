import assert from 'node:assert/strict';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import {fileURLToPath, pathToFileURL} from 'node:url';

// No application server, SSH connection, model request or saved configuration
// is used: only the production child UI and a typed snapshot transport fixture.
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const chrome = process.env.CHROME_BIN || process.argv[2] || '/usr/bin/google-chrome';
const modulePath = process.env.PUPPETEER_MODULE || process.argv[3] || '/home/bitsflow/.cache/dengshell-dev-tools/node_modules/puppeteer-core/lib/puppeteer/puppeteer-core.js';
const {default: puppeteer} = await import(path.isAbsolute(modulePath) ? pathToFileURL(modulePath).href : modulePath);
const output = process.env.AI_CONVERSATION_REPORT;
const selectedText = '保留选择_COPY_TOKEN';
const modelA = 'fixture-model-A', modelB = 'fixture-model-B';
const provider = (id, model) => ({id, name: '配置 ' + id, format: 'openai', baseURL: 'https://example.invalid/v1', model, hasKey: true, proxy: {type: 'direct'}});
const settings = {provider: 'A', timeout: 120, providers: [provider('A', modelA), provider('B', modelB)]};
const wideTable = '| ' + Array.from({length: 10}, (_, i) => '列' + i).join(' | ') + ' |\n| ' + Array(10).fill('---').join(' | ') + ' |\n| ' + Array.from({length: 10}, (_, i) => 'table-cell-' + i).join(' | ') + ' |';
const rawHTML = '<img src="https://example.invalid/tracker" onerror="window.aiUnsafe=true"><script>window.aiUnsafe=true</script>';
const markdown = `${selectedText}\n\n${wideTable}\n\n\`\`\`sh\nprintf '%s' '${'long-code-'.repeat(50)}'\n\`\`\`\n\n${Array.from({length: 24}, (_, i) => `段落 ${i}：保留阅读位置，不因操作进度更新而重建消息。`).join('\n\n')}\n\n${rawHTML}\n`;
let state = {type: 'state', enabled: true, busy: true, settingsBusy: false, settings, target: '目标：隔离的 DOM 测试', theme: 'light', logRevision: 1, closed: false, events: [{role: 'user', value: '请解释表格。' + rawHTML}, {role: 'assistant', value: markdown, model: modelA}]};
const css = ['style.css', 'theme.css', 'light-theme.css', 'ai-assistant.css', 'ai-window.css', 'ai-markdown.css'];
const js = ['vendor/markdown-it.js', 'ai-markdown.js', 'ai-assistant.js'];
const assets = new Set([...css, ...js]);
const fixtureJS = `window.DENG_AI_CHILD=true;
window.__state=${JSON.stringify(state)};
window.__markdownCalls=0;
window.__requests=[];
window.__originals={};window.__sourceChunks=[];window.__heldOriginals=[];window.__holdOriginal=false;
window.DengAIWindowTransport={
  subscribe(fn){window.__receive=fn;fn(window.__state);},
  ready(){return Promise.resolve({ok:true});},
  request(action,body){
    window.__requests.push({action,body});
    if(action==='get_original')return new Promise((resolve,reject)=>{
      const reply=()=>{
        const source=window.__originals[body.sourceId];
        if(typeof source!=='string'){reject(new Error('此回复原文已从当前对话缓存移除。'));return;}
        let next=Math.min(body.offset+64000,source.length);
        if(next<source.length&&source.charCodeAt(next-1)>=0xd800&&source.charCodeAt(next-1)<=0xdbff&&source.charCodeAt(next)>=0xdc00&&source.charCodeAt(next)<=0xdfff)next--;
        const text=source.slice(body.offset,next);
        window.__sourceChunks.push({offset:body.offset,next});
        resolve({text,next,total:source.length,done:next===source.length});
      };
      if(window.__holdOriginal)window.__heldOriginals.push(reply);else reply();
    });
    return Promise.resolve(action==='get_settings'?window.__state.settings:{ok:true});
  },
  onClosed(fn){window.__closedReceive=fn;},hasNativeWindowControls(){return false;},close(){}
};
window.__deliver=value=>{window.__state=value;window.__receive(value);};`;
const html = `<!doctype html><html lang="zh-CN" data-theme="light"><head><meta charset="utf-8">${css.map(name => `<link rel="stylesheet" href="/${name}">`).join('')}</head><body class="ai-window-body"><script src="/fixture.js"></script><script src="/vendor/markdown-it.js"></script><script src="/ai-markdown.js"></script><script>const renderer=window.DengAIMarkdown;window.DengAIMarkdown={render(...args){window.__markdownCalls++;return renderer.render(...args);}};</script><script src="/ai-assistant.js"></script></body></html>`;
const server = http.createServer((request, response) => {
  const name = new URL(request.url, 'http://localhost').pathname.slice(1);
  if (!name) {response.setHeader('Content-Type', 'text/html; charset=utf-8'); response.end(html); return;}
  if (name === 'fixture.js') {response.setHeader('Content-Type', 'text/javascript; charset=utf-8'); response.end(fixtureJS); return;}
  if (!assets.has(name)) {response.writeHead(404); response.end(); return;}
  response.setHeader('Content-Type', name.endsWith('.css') ? 'text/css; charset=utf-8' : 'text/javascript; charset=utf-8');
  response.end(fs.readFileSync(path.join(root, 'web', name)));
});
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
const origin = 'http://127.0.0.1:' + server.address().port;
let browser;
const report = {passed: false, checks: [], measurements: {}, errors: []};
try {
  browser = await puppeteer.launch({executablePath: chrome, headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage']});
  const page = await browser.newPage();
  await page.setViewport({width: 450, height: 760});
  page.on('pageerror', error => report.errors.push(error.message));
  await page.setRequestInterception(true);
  page.on('request', request => {
    if (request.url().startsWith(origin + '/')) void request.continue();
    else {report.errors.push('Unexpected external request: ' + request.url()); void request.abort();}
  });
  await page.goto(origin, {waitUntil: 'load'});
  await page.waitForSelector('.ai-message-assistant .ai-markdown-table');
  const frames = () => page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  await frames();
  const initial = await page.evaluate(text => {
    const conversation = document.querySelector('#ai-conversation');
    const article = document.querySelector('.ai-message-assistant');
    const table = article.querySelector('.ai-markdown-table');
    const code = article.querySelector('.ai-markdown-code');
    const paragraph = [...article.querySelectorAll('p')].find(item => item.textContent.includes(text));
    conversation.tabIndex = -1; conversation.focus({preventScroll: true});
    const node = paragraph.firstChild;
    const start = node.textContent.indexOf(text);
    const range = document.createRange(); range.setStart(node, start); range.setEnd(node, start + text.length);
    window.getSelection().removeAllRanges(); window.getSelection().addRange(range);
    table.scrollLeft = 120; code.scrollLeft = 100; conversation.scrollTop = 0;
    window.__oldArticle = article; window.__oldTable = table; window.__oldCode = code;
    window.__copyEvents = [];
    document.addEventListener('copy', () => window.__copyEvents.push(window.getSelection().toString()));
    return {selected: window.getSelection().toString(), tableLeft: table.scrollLeft, codeLeft: code.scrollLeft, vertical: conversation.scrollTop, conversationHeight: conversation.clientHeight, conversationOverflow: conversation.scrollHeight - conversation.clientHeight, tableOverflow: table.scrollWidth - table.clientWidth, codeOverflow: code.scrollWidth - code.clientWidth, parses: window.__markdownCalls, caption: article.querySelector('.ai-message-caption').textContent};
  }, selectedText);
  assert.equal(initial.selected, selectedText);
  assert(initial.tableLeft > 0 && initial.codeLeft > 0, 'Both table and code must have genuine internal overflow');
  assert(initial.conversationHeight > 0 && initial.conversationOverflow > 0, 'Conversation must genuinely overflow vertically');
  assert.equal(initial.parses, 1);
  assert(initial.caption.includes(modelA));
  report.measurements.initial = initial;
  async function deliver(next) {state = next; await page.evaluate(value => window.__deliver(value), state); await frames();}
  async function preserved(expectedParses = 1) {
    const value = await page.evaluate(() => ({sameArticle: window.__oldArticle === document.querySelector('.ai-message-assistant'), sameTable: window.__oldTable === document.querySelector('.ai-markdown-table'), selected: window.getSelection().toString(), tableLeft: window.__oldTable.scrollLeft, codeLeft: window.__oldCode.scrollLeft, vertical: document.querySelector('#ai-conversation').scrollTop, parses: window.__markdownCalls}));
    assert.equal(value.sameArticle, true); assert.equal(value.sameTable, true);
    assert.equal(value.selected, selectedText);
    assert.equal(value.tableLeft, initial.tableLeft); assert.equal(value.codeLeft, initial.codeLeft);
    assert.equal(value.vertical, initial.vertical); assert.equal(value.parses, expectedParses);
    return value;
  }
  await deliver({...state, logRevision: 2, events: [...state.events, {role: 'operation', value: {id: 'fixture-operation', name: 'read_terminal', arguments: '{}', status: 'running'}}]});
  await preserved();
  await page.$eval('.ai-operation', element => {element.open = true;});
  await deliver({...state, logRevision: 3, events: [...state.events, {role: 'operation-result', value: {id: 'fixture-operation', content: '完成：只读 DOM fixture', status: 'done'}}]});
  report.measurements.afterOperation = await preserved();
  assert.equal(await page.$eval('.ai-operation', element => element.open && element.dataset.status === 'done'), true);
  report.checks.push('Operation append/result preserves article identity, selected text, table/code horizontal positions, vertical reading position and expanded operation');

  // Exercise the browser default copy action with a real keyboard event. The
  // copy listener only observes selection; it does not replace clipboard data.
  await page.keyboard.down('Control'); await page.keyboard.press('KeyC'); await page.keyboard.up('Control');
  assert.deepEqual(await page.evaluate(() => window.__copyEvents), [selectedText]);
  report.checks.push('Selected rendered text remains available to a genuine Ctrl+C copy event');
  await deliver({...state, busy: false, settings: {...settings, provider: 'B'}, target: '目标：仅状态更新'});
  await preserved();
  assert((await page.$eval('.ai-message-assistant .ai-message-caption', element => element.textContent)).includes(modelA));
  report.checks.push('Same log revision does not parse or rebuild messages when status/active configuration changes');

  await deliver({...state, logRevision: 4, events: [...state.events, {role: 'user', value: '使用配置 B'}, {role: 'assistant', model: modelB, value: '**第二个模型**\n\n' + rawHTML}]});
  await preserved(2);
  const captions = await page.$$eval('.ai-message-assistant .ai-message-caption', elements => elements.map(element => element.textContent));
  assert.equal(captions.length, 2); assert(captions[0].includes(modelA)); assert(!captions[0].includes(modelB)); assert(captions[1].includes(modelB));
  await deliver({...state, settings: {...settings, provider: 'A'}});
  assert.deepEqual(await page.$$eval('.ai-message-assistant .ai-message-caption', elements => elements.map(element => element.textContent)), captions);
  assert.equal(await page.evaluate(() => window.__markdownCalls), 2);
  const safety = await page.evaluate(() => ({unsafe: !!window.aiUnsafe, elements: document.querySelectorAll('.ai-message-content :is(script,img,iframe,object,embed)').length, userText: document.querySelector('.ai-message-user .ai-message-content').textContent, assistantText: document.querySelector('.ai-message-assistant .ai-message-content').textContent, calls: window.__requests}));
  assert.equal(safety.unsafe, false); assert.equal(safety.elements, 0); assert(safety.userText.includes(rawHTML)); assert(safety.assistantText.includes(rawHTML));
  assert(safety.calls.every(item => item.action === 'get_settings'), 'Fixture must never send a model or tool request');
  report.checks.push('Model A/B captions remain attached to their response after provider changes; literal HTML stays selectable text with no active elements');

  // Clipboard writes are captured after the genuine selection-copy check.
  // Rendering and switching views must never copy or execute an answer.
  await page.evaluate(() => {
    window.__sourceCopies = [];
    Object.defineProperty(navigator, 'clipboard', {configurable: true, value: {writeText: async value => {window.__sourceCopies.push(value);}}});
  });
  assert.deepEqual(await page.evaluate(() => window.__sourceCopies), []);
  const firstToggle = '.ai-message-assistant .ai-message-source-toggle';
  const firstCopy = '.ai-message-assistant .ai-message-copy';
  assert.equal(await page.$eval(firstToggle, element => element.getAttribute('aria-pressed')), 'false');
  await page.click(firstToggle);
  await page.waitForFunction(() => document.querySelector('.ai-message-assistant .ai-message-source-toggle').getAttribute('aria-pressed') === 'true');
  assert.equal(await page.$eval('.ai-message-assistant .ai-message-content', element => element.textContent), markdown);
  assert.deepEqual(await page.evaluate(() => window.__sourceCopies), []);
  await page.click(firstCopy);
  await page.waitForFunction(() => window.__sourceCopies.length === 1);
  assert.deepEqual(await page.evaluate(() => window.__sourceCopies), [markdown]);
  await page.click(firstToggle);
  await page.waitForSelector('.ai-message-assistant .ai-markdown-table');
  assert.equal(await page.$eval(firstToggle, element => element.getAttribute('aria-pressed')), 'false');
  await page.click(firstCopy);
  await page.waitForFunction(() => window.__sourceCopies.length === 2);
  assert.deepEqual(await page.evaluate(() => window.__sourceCopies), [markdown, markdown]);
  assert.equal(await page.evaluate(() => !!window.aiUnsafe), false);
  report.checks.push('Source/format toggle preserves exact Markdown including trailing newline; copy in either view writes only on click and copies the same original response');

  // Keep both short and wide responses inside the actual child layout.
  report.measurements.layouts = [];
  for (const width of [450, 320]) {
    await page.setViewport({width, height: 760}); await frames();
    const layout = await page.evaluate(() => {
      const conversation = document.querySelector('#ai-conversation');
      const user = document.querySelector('.ai-message-user').getBoundingClientRect();
      const assistant = document.querySelector('.ai-message-assistant').getBoundingClientRect();
      const c = conversation.getBoundingClientRect();
      return {width: window.innerWidth, bodyOverflow: document.documentElement.scrollWidth - window.innerWidth, conversationOverflow: conversation.scrollWidth - conversation.clientWidth, userLeft: user.left, assistantLeft: assistant.left, userRight: user.right, assistantRight: assistant.right, conversationLeft: c.left, conversationRight: c.right};
    });
    assert(layout.bodyOverflow <= 1 && layout.conversationOverflow <= 1, 'Wide content must scroll inside its own table/code, not the child window');
    assert(layout.userLeft > layout.assistantLeft, 'User bubble must be aligned to the right');
    assert(layout.userRight > layout.assistantRight, 'Assistant bubble must leave space on its right');
    report.measurements.layouts.push(layout);
  }
  report.checks.push('At 450px and 320px child widths, user/right and assistant/left bubbles retain internal code/table scrolling');

  // Trim/clear are allowed to rebuild; ordinary progress updates are not.
  await deliver({...state, logRevision: 5, events: state.events.slice(-2)});
  assert.equal(await page.$$eval('.ai-message-assistant', elements => elements.length), 1);
  assert((await page.$eval('.ai-message-assistant .ai-message-caption', element => element.textContent)).includes(modelB));
  await deliver({...state, logRevision: 6, events: []});
  assert.equal(await page.$$eval('.ai-message', elements => elements.length), 0);
  assert.equal(await page.$eval('.ai-welcome', element => element.hidden), false);
  report.checks.push('Truncated history and explicit clear rebuild accurately without stale messages');

  const longOriginal = 'X'.repeat(63999) + '😀' + '\n中文末尾\n'.repeat(12000) + '\n';
  const sourceID = 'long-original-fixture';
  const longEvent = {role: 'assistant', value: longOriginal.slice(0, 16000), model: modelA, sourceId: sourceID, sourceLength: longOriginal.length, truncated: true, sourceAvailable: true};
  await page.evaluate(({id, text}) => {window.__originals[id] = text; window.__sourceCopies = []; window.__requests = [];}, {id: sourceID, text: longOriginal});
  await deliver({...state, logRevision: 7, events: [longEvent]});
  assert.equal(await page.$eval('.ai-message-assistant .ai-message-content', element => element.textContent), longOriginal.slice(0, 16000));
  assert.equal(await page.evaluate(() => window.__requests.length), 0, 'Rendering a preview must not fetch the original');
  await page.click(firstToggle);
  await page.waitForFunction(length => document.querySelector('.ai-message-assistant .ai-message-content').textContent.length === length, {}, longOriginal.length);
  assert.equal(await page.$eval('.ai-message-assistant .ai-message-content', element => element.textContent), longOriginal);
  const chunks = await page.evaluate(() => window.__requests.filter(item => item.action === 'get_original').map(item => item.body.offset));
  const chunkBounds = await page.evaluate(() => window.__sourceChunks);
  assert(chunkBounds.length > 1); assert.equal(chunkBounds[0].offset, 0);
  for (let index = 0; index < chunkBounds.length; index += 1) {
    const chunk = chunkBounds[index];
    assert.equal(chunks[index], chunk.offset);
    assert(chunk.next > chunk.offset && chunk.next - chunk.offset <= 64000);
    if (index > 0) assert.equal(chunk.offset, chunkBounds[index - 1].next);
  }
  assert.equal(chunkBounds.at(-1).next, longOriginal.length);
  assert.deepEqual(await page.evaluate(() => window.__sourceCopies), []);
  await page.click(firstCopy);
  await page.waitForFunction(() => window.__sourceCopies.length === 1);
  assert.deepEqual(await page.evaluate(() => window.__sourceCopies), [longOriginal]);
  assert.equal(await page.evaluate(() => window.__requests.length), chunks.length, 'A loaded source should be reused when copying');
  report.measurements.longOriginal = {characters: longOriginal.length, previewCharacters: 16000, requestedOffsets: chunks, exactTrailingNewline: longOriginal.endsWith('\n')};
  report.checks.push('A >16000-character reply is fetched in 64000-character chunks, reassembled across a surrogate-pair boundary, and copied exactly with trailing newlines');

  // A missing owner cache must report failure instead of copying the preview.
  for (const available of [false, true]) {
    await page.evaluate(() => {window.__originals = {}; window.__sourceCopies = []; window.__requests = [];});
    await deliver({...state, logRevision: state.logRevision + 1, events: [{...longEvent, sourceId: 'missing-source', sourceAvailable: available}]});
    await page.click(firstCopy);
    await page.waitForFunction(() => !document.querySelector('.ai-message-copy').disabled && /缓存移除/.test(document.querySelector('.ai-message-feedback').textContent));
    assert.deepEqual(await page.evaluate(() => window.__sourceCopies), []);
    assert.equal(await page.evaluate(() => window.__requests.filter(item => item.action === 'get_original').length), available ? 1 : 0);
    assert.equal(await page.$eval('.ai-message-content', element => element.textContent), longOriginal.slice(0, 16000));
  }
  report.checks.push('Known-evicted and unexpectedly missing originals show an explicit error and never copy truncated preview text');

  for (const lifecycle of ['close', 'lock']) {
    await page.reload({waitUntil: 'load'}); await frames();
    await page.evaluate(({id, text}) => {
      window.__originals[id] = text; window.__sourceCopies = []; window.__requests = []; window.__holdOriginal = true;
      Object.defineProperty(navigator, 'clipboard', {configurable: true, value: {writeText: async value => {window.__sourceCopies.push(value);}}});
    }, {id: sourceID, text: longOriginal});
    await deliver({...state, logRevision: state.logRevision + 1, closed: false, events: [longEvent]});
    await page.click(firstCopy);
    await page.waitForFunction(() => window.__heldOriginals.length === 1);
    await page.evaluate(mode => {
      if (mode === 'lock') window.__closedReceive('工作区已锁定，AI 已停止。');
      else window.__deliver({...window.__state, closed: true});
      window.__holdOriginal = false;
      window.__heldOriginals.splice(0).forEach(reply => reply());
    }, lifecycle);
    await frames();
    assert.deepEqual(await page.evaluate(() => window.__sourceCopies), [], lifecycle + ' must invalidate pending copy');
    assert.equal(await page.evaluate(() => window.__requests.filter(item => item.action === 'get_original').length), 1, lifecycle + ' must prevent the next chunk request');
  }
  report.checks.push('Closing or locking during a pending source chunk prevents clipboard writes and prevents additional chunk requests');
  assert.deepEqual(report.errors, []);
  report.passed = true;
} catch (error) {
  report.failure = error.stack || String(error);
  throw error;
} finally {
  if (output) {fs.mkdirSync(path.dirname(output), {recursive: true}); fs.writeFileSync(output, JSON.stringify(report, null, 2) + '\n');}
  if (browser) await browser.close();
  await new Promise(resolve => server.close(resolve));
}
console.log('PASS: AI child Markdown conversation, model labels, stable snapshots, selection/copy, table/code scrolling, bubble alignment and clear/trim.');
