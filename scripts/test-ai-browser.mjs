import fs from 'node:fs';
import http from 'node:http';
import net from 'node:net';
import assert from 'node:assert/strict';
const [stage, chrome, modulePath] = process.argv.slice(2);
const {default: puppeteer} = await import(modulePath);
const fixture = JSON.parse(fs.readFileSync(stage + '/browser-fixture.json'));
assert.equal(new URL(fixture.url).hostname, '127.0.0.1');
const headers = {'Content-Type':'application/json', 'X-CloudShell-Token':fixture.token};
async function api(path, body, method = body === undefined ? 'GET' : 'POST') {
  const response = await fetch(new URL(path, fixture.url), {method, headers, body:body === undefined ? undefined : JSON.stringify(body)});
  const data = await response.json(); assert(response.ok, `${path}: ${JSON.stringify(data)}`); return data;
}
let mode = 'functional', step = 0, requestCount = 0, completedResponses = 0, session1, session2;
const requests = [], results = [], errors = [], checks = [], receivedAuth = [];
const originalRequests = [];
const functionalReply = [
  '真实终端、交互界面及文件修改完成。', '',
  '| 检查项目 | 结果 |', '| :--- | :--- |',
  '| **终端** | `AI_REAL_TERMINAL_OK` |', '| **文件** | 修改完成 |', '',
  '```sh', "printf 'AI_REAL_TERMINAL_OK\\n'", '```', '',
  '<img src=x onerror="window.aiInjected=true">', ''
].join('\n');
const selectPrefix = '会话切换完成。\n';
// Put an emoji across the nominal 64,000 UTF-16 boundary, then require at least
// three real relay requests. Only the clipboard spy below receives this text.
const longSelectReply = selectPrefix + '中'.repeat(64000 - selectPrefix.length - 1) + '🙂' + '中文原文分段🧪\n'.repeat(9000) + '完整回复末尾🙂\n';
assert(longSelectReply.length > 128000);
const tokenOne = 'isolated-ai-token-one', tokenTwo = 'isolated-ai-token-two';
const tool = (name, args) => ({role:'assistant',content:null,tool_calls:[{id:'call_'+requestCount,type:'function',function:{name,arguments:JSON.stringify(args)}}]});
const provider = http.createServer(async (request, response) => {
  receivedAuth.push({path:request.url, authorization:request.headers.authorization});
  if (request.url === '/v1/models') {response.setHeader('Content-Type','application/json'); response.end(JSON.stringify({data:[{id:'fixture-tools'}]})); return;}
  try {
    assert.equal(request.url, '/v1/chat/completions');
    const chunks=[]; for await (const chunk of request) chunks.push(chunk);
    const body=JSON.parse(Buffer.concat(chunks)); requests.push(body); requestCount++;
    const previous=body.messages.filter(item=>item.role==='tool').map(item=>JSON.parse(item.content));
    results.push(...previous.slice(-1));
    let message;
    if (mode === 'functional') {
      const actions = [
        () => tool('read_terminal',{session_id:session1,max_lines:60}),
        () => tool('terminal_input',{session_id:session1,text:"printf 'AI_REAL_TERMINAL_OK\\n'; printf 'before\\n' > /home/bitsflow/ai-fixture.txt",execute:true,wait_ms:1200}),
        () => tool('read_file',{session_id:session1,path:'/home/bitsflow/ai-fixture.txt'}),
        () => {const read=previous.at(-1); assert(!read.error, JSON.stringify(read)); return tool('write_file',{session_id:session1,path:'/home/bitsflow/ai-fixture.txt',text:'after AI 修改\n',sha256:read.sha256,encoding:read.encoding});},
        () => tool('terminal_input',{session_id:session1,text:"printf '\\033[?1049hAI_TUI_READY'; read -r -n1 ai_key; printf '\\033[?1049l'; printf 'AI_TUI_KEY_%s\\n' \"$ai_key\"",execute:true,wait_ms:700}),
        () => tool('read_terminal',{session_id:session1,max_lines:40}),
        () => tool('terminal_input',{session_id:session1,text:'q',execute:false,wait_ms:1000}),
        () => tool('read_terminal',{session_id:session1,max_lines:100}),
      ];
      message = step < actions.length ? actions[step++]() : {role:'assistant',content:functionalReply};
    } else if (mode === 'select') {
      const actions = [() => tool('select_session',{session_id:session2}), () => tool('terminal_input',{session_id:session2,text:"printf 'AI_SECOND_SESSION_OK\\n'",execute:true,wait_ms:900})];
      message=step < actions.length ? actions[step++]() : {role:'assistant',content:longSelectReply};
    } else {
      await new Promise(resolve=>setTimeout(resolve,1200));
      message=tool('terminal_input',{session_id:session1,text:'touch /home/bitsflow/ai-must-not-run',execute:true,wait_ms:100});
    }
    response.setHeader('Content-Type','application/json'); response.end(JSON.stringify({choices:[{message,finish_reason:message.tool_calls?'tool_calls':'stop'}]})); completedResponses++;
  } catch (error) {errors.push('provider: '+error.message); response.statusCode=500;response.end(JSON.stringify({error:{message:error.message}}));}
});
await new Promise(resolve=>provider.listen(0,'127.0.0.1',resolve));
const modelURL='http://127.0.0.1:'+provider.address().port+'/v1';
let proxyRequests = 0;
const proxyPassword = 'isolated-proxy-password';
const proxyAuthorization = 'Basic '+Buffer.from('fixture:'+proxyPassword).toString('base64');
const proxy = http.createServer((request,response) => {
  proxyRequests++;
  if(request.headers['proxy-authorization']!==proxyAuthorization) {response.writeHead(407);response.end();return;}
  const target=new URL(request.url);
  if(target.origin!==new URL(modelURL).origin) {response.writeHead(403);response.end();return;}
  const forwardedHeaders={...request.headers};delete forwardedHeaders['proxy-authorization'];
  const upstream=http.request(target,{method:request.method,headers:forwardedHeaders},r=>{response.writeHead(r.statusCode,r.headers);r.pipe(response);});
  upstream.on('error',()=>{response.writeHead(502);response.end();});request.pipe(upstream);
});
proxy.on('connect',(request,socket,head)=>{
  proxyRequests++;
  if(request.headers['proxy-authorization']!==proxyAuthorization||request.url!==new URL(modelURL).host) {socket.end('HTTP/1.1 407 Proxy Authentication Required\r\n\r\n');return;}
  const upstream=net.connect(provider.address().port,'127.0.0.1',()=>{socket.write('HTTP/1.1 200 Connection Established\r\n\r\n');if(head.length)upstream.write(head);socket.pipe(upstream);upstream.pipe(socket);});
  upstream.on('error',()=>socket.destroy());socket.on('error',()=>upstream.destroy());socket.on('close',()=>upstream.destroy());
});
await new Promise(resolve=>proxy.listen(0,'127.0.0.1',resolve));
await api('/api/appearance/patch',{onboardingCompleted:true,startupAnimation:false,uiScale:1});
const managed=await api('/api/keys',{name:'AI isolated test',sourcePath:fixture.key});
const profile=await api('/api/profiles',{name:'AI isolated SSH',host:'127.0.0.1',port:fixture.port,user:'bitsflow',auth:'key',keyId:managed.id});
const trusted=await api('/api/sessions',{profileId:profile.id,hostKeyApproval:{host:'127.0.0.1:'+fixture.port,fingerprint:fixture.fingerprint}});
await api('/api/sessions/'+trusted.id,undefined,'DELETE');
const emptySettings=await api('/api/ai/settings');assert.deepEqual(emptySettings.providers,[]);assert.equal(emptySettings.provider,'');assert.equal(emptySettings.presets,undefined);
const sshProxyState=config=>JSON.stringify({proxies:config.proxies,servers:(config.servers||[]).map(item=>({id:item.id,proxyId:item.proxyId,proxy:item.proxy}))});
const sshBefore=sshProxyState(await api('/api/config'));
const browser=await puppeteer.launch({executablePath:chrome,headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
let page, assistant;
async function send(text) {
  await assistant.waitForSelector('#ai-send:not([disabled])');
  await assistant.$eval('#ai-prompt',(element,value)=>element.value=value,text); await assistant.click('#ai-send');
  await page.waitForFunction(()=>DengShellAI.busy);
}
async function idle() {await page.waitForFunction(()=>!DengShellAI.busy,{timeout:45000});await new Promise(r=>setTimeout(r,700));}
try {
  page=await browser.newPage();page.on('pageerror',error=>errors.push(error.message));
  await page.emulateMediaFeatures([{name:'prefers-reduced-motion',value:'reduce'}]);
  await page.setViewport({width:1440,height:1000});
  await page.goto(fixture.url,{waitUntil:'networkidle0'});
  await page.evaluate(()=>{document.querySelectorAll('dialog[open]').forEach(d=>d.close());setDrawer(false);});
  session1=await page.evaluate(async id=>(await connect(id)).id,profile.id);
  await page.waitForFunction(id=>sessions.get(id)?.ready&&sessions.get(id)?.shellIntegration?.atPrompt,{},session1);
  session2=await page.evaluate(async id=>(await connect(id)).id,profile.id);
  await page.waitForFunction(id=>sessions.get(id)?.ready&&sessions.get(id)?.shellIntegration?.atPrompt,{},session2);
  await page.evaluate(id=>activate(id),session1);
  const mainSize = await page.evaluate(()=>{const r=document.querySelector('.app-shell').getBoundingClientRect(); return {width:r.width,height:r.height};});
  async function openAI(initial = false) {
    const popup = new Promise(resolve=>page.once('popup',resolve));
    await page.click('#ai-button'); assistant=await popup;
    assistant.on('pageerror',error=>errors.push('AI popup: '+error.message));
    assistant.on('request',request=>{
      if(request.method()!=='POST'||!request.url().endsWith('/messages'))return;
      try {const data=JSON.parse(request.postData()||'null');if(data?.side==='assistant'&&data.payload?.action==='get_original')originalRequests.push(data.payload.body);} catch {}
    });
    await assistant.waitForSelector('#ai-enabled');
    if(!initial)await assistant.waitForFunction(()=>[...document.querySelectorAll('#ai-provider option')].some(item=>item.textContent.includes('本机测试模型')));
  }
  await openAI(true);
  assert.deepEqual(await page.evaluate(()=>{const r=document.querySelector('.app-shell').getBoundingClientRect(); return {width:r.width,height:r.height};}),mainSize);
  assert.equal(await page.$eval('#ai-assistant',e=>getComputedStyle(e).display),'none');
  const pageCount=(await browser.pages()).length;await page.click('#ai-button');await new Promise(r=>setTimeout(r,400));assert.equal((await browser.pages()).length,pageCount);
  assert.equal(await assistant.$('#ai-pin'),null);
  checks.push('Separate AI popup preserves entire SSH workspace, repeated opening reuses it, browser does not pretend to support native titlebar controls');
  await assistant.waitForSelector('#ai-settings:not([hidden])');
  assert.equal(await assistant.$('#ai-presets'),null);
  async function fill(selector,value) {await assistant.$eval(selector,(element,value)=>{element.value=value;element.dispatchEvent(new Event('input',{bubbles:true}));element.dispatchEvent(new Event('change',{bubbles:true}));},String(value));}
  await assistant.click('#ai-add-provider');
  await fill('#ai-provider-name','本机测试模型 · 代理');await fill('#ai-base-url',modelURL);await fill('#ai-api-key',tokenOne);await fill('#ai-model','fixture-tools');
  await assistant.select('#ai-proxy-type','http');await fill('#ai-proxy-host','127.0.0.1');await fill('#ai-proxy-port',proxy.address().port);await fill('#ai-proxy-user','fixture');await fill('#ai-proxy-password',proxyPassword);
  const firstID=await assistant.$eval('#ai-settings-provider',e=>e.value);
  await assistant.click('#ai-add-provider');
  await fill('#ai-provider-name','本机测试模型 · 直连');await fill('#ai-base-url',modelURL);await fill('#ai-api-key',tokenTwo);await fill('#ai-model','fixture-tools');await assistant.select('#ai-proxy-type','direct');
  const secondID=await assistant.$eval('#ai-settings-provider',e=>e.value);
  await assistant.select('#ai-settings-provider',firstID);
  assert.equal(await assistant.$eval('#ai-api-key',e=>e.value),tokenOne);assert.equal(await assistant.$eval('#ai-proxy-password',e=>e.value),proxyPassword);
  await assistant.click('#ai-fetch-models');
  await assistant.waitForSelector('#ai-models:not([hidden])');
  assert.equal(receivedAuth.at(-1).authorization,'Bearer '+tokenOne);assert(proxyRequests>0);
  assert((await assistant.$$eval('#ai-models option',es=>es.map(e=>e.value))).includes('fixture-tools'));
  await assistant.select('#ai-settings-provider',secondID);assert.equal(await assistant.$eval('#ai-api-key',e=>e.value),tokenTwo);
  const proxyBefore=proxyRequests;await assistant.click('#ai-fetch-models');await assistant.waitForSelector('#ai-models:not([hidden])');
  assert.equal(receivedAuth.at(-1).authorization,'Bearer '+tokenTwo);assert.equal(proxyRequests,proxyBefore);
  await assistant.click('#ai-save-settings');await assistant.waitForSelector('#ai-settings[hidden]');
  const savedSettings=await api('/api/ai/settings');assert.equal(savedSettings.providers.length,2);assert(savedSettings.providers.every(item=>item.hasKey&&!item.apiKey&&!item.proxy.password));
  assert.equal(savedSettings.providers.find(item=>item.id===firstID).proxy.hasPassword,true);
  const snapshot=await page.evaluate(()=>DengShellAIController.snapshot());for(const secret of [tokenOne,tokenTwo,proxyPassword])assert(!JSON.stringify(snapshot).includes(secret));
  await assistant.click('#ai-settings-toggle');assert.equal(await assistant.$eval('#ai-api-key',e=>e.value),'');
  await assistant.select('#ai-settings-provider',firstID);assert.equal(await assistant.$eval('#ai-api-key',e=>e.value),'');assert.equal(await assistant.$eval('#ai-proxy-password',e=>e.value),'');
  await assistant.click('#ai-fetch-models');await assistant.waitForSelector('#ai-models:not([hidden])');assert.equal(receivedAuth.at(-1).authorization,'Bearer '+tokenOne);assert(proxyRequests>proxyBefore);
  await assistant.click('#ai-save-settings');await assistant.waitForSelector('#ai-settings[hidden]');
  // SSH records and proxy selection are not changed by AI settings saves.
  const sshAfter=sshProxyState(await api('/api/config'));assert.equal(sshAfter,sshBefore);
  checks.push('No presets or initial account; two same-endpoint tokens and independent authenticated HTTP/direct proxies survive draft switches and encrypted saves without credential readback');
  await assistant.click('#ai-enabled');await send('验证真实终端、全屏交互及远程文件修改');await idle();
  assert(receivedAuth.filter(item=>item.path.endsWith('/chat/completions')).every(item=>item.authorization==='Bearer '+tokenOne));
  const log=await assistant.$eval('#ai-conversation',e=>e.textContent); assert(log.includes('真实终端、交互界面及文件修改完成'),log);
  assert.equal(await assistant.evaluate(()=>window.aiInjected),undefined);
  const finalReplySelector='#ai-conversation .ai-message-assistant:last-of-type';
  await assistant.waitForSelector(finalReplySelector+' .ai-markdown table');
  const renderedReply=await assistant.$eval(finalReplySelector,element=>({
    caption:element.querySelector('.ai-message-caption').textContent,
    table:element.querySelector('table')?.textContent,
    strong:[...element.querySelectorAll('strong')].map(item=>item.textContent),
    inline:element.querySelector('td code')?.textContent,
    code:element.querySelector('pre code')?.textContent,
    images:element.querySelectorAll('img').length,
  }));
  assert.equal(renderedReply.caption,'fixture-tools');assert(renderedReply.table.includes('检查项目'));assert(renderedReply.strong.includes('终端'));
  assert.equal(renderedReply.inline,'AI_REAL_TERMINAL_OK');assert.equal(renderedReply.code,"printf 'AI_REAL_TERMINAL_OK\\n'\n");assert.equal(renderedReply.images,0);
  await assistant.click(finalReplySelector+' .ai-message-source-toggle');
  await assistant.waitForFunction((selector,text)=>document.querySelector(selector+' .ai-message-source')?.textContent===text,{},finalReplySelector,functionalReply);
  assert.equal(await assistant.$eval(finalReplySelector+' .ai-message-content',element=>element.textContent),functionalReply);
  await assistant.click(finalReplySelector+' .ai-message-source-toggle');await assistant.waitForSelector(finalReplySelector+' .ai-markdown table');
  checks.push('Real owner/child relay renders Chinese Markdown tables, bold and code with the saved model caption; source view preserves exact text and trailing newline while HTML stays inert');
  assert(results.some(item=>item.output?.includes('AI_REAL_TERMINAL_OK')));
  assert(results.some(item=>item.buffer==='alternate'&&item.output?.includes('AI_TUI_READY')));
  assert(results.some(item=>item.output?.includes('AI_TUI_KEY_q')));
  assert(!results.some(item=>item.error),JSON.stringify(results.filter(item=>item.error)));
  const content=await api(`/api/sessions/${session1}/file-content?path=${encodeURIComponent('/home/bitsflow/ai-fixture.txt')}`);
  assert.equal(content.text,'after AI 修改\n');
  checks.push('UI settings/model discovery, real SSH prompt/command, alternate-screen TUI input, SFTP guarded write, inert model HTML');
  for(const theme of ['light','dark']) {await page.evaluate(t=>CloudShellTheme.set(t),theme);await new Promise(r=>setTimeout(r,500));await page.screenshot({path:stage+'/main-'+theme+'.png'});await assistant.screenshot({path:stage+'/ai-'+theme+'.png'});}
  const geometry=await page.evaluate(()=>{const r=id=>{const b=document.getElementById(id).getBoundingClientRect();return {left:b.left,right:b.right,top:b.top,bottom:b.bottom}};return {sync:r('sync-button'),ai:r('ai-button'),connection:r('connection-button'),panel:r('ai-assistant')}});
  assert(geometry.ai.left>=geometry.sync.right&&geometry.connection.left>=geometry.ai.right,JSON.stringify(geometry));
  for(const [width,height] of [[390,640],[600,800]]) {await assistant.setViewport({width,height});await new Promise(r=>setTimeout(r,150)); assert(await assistant.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1)); await assistant.screenshot({path:stage+'/ai-'+width+'.png'});}
  await page.setViewport({width:1440,height:1000});
  await assistant.click('#ai-clear');mode='select';step=0;await send('切换到第二个会话并检查终端');await idle();
  assert.equal(await page.evaluate(()=>current()?.id),session2);assert((await assistant.$eval('#ai-conversation',e=>e.textContent)).includes('会话切换完成'));
  assert.equal(await assistant.$eval(finalReplySelector+' .ai-message-caption',element=>element.textContent),'fixture-tools');
  assert((await assistant.$eval(finalReplySelector+' .ai-message-content',element=>element.textContent.length))<longSelectReply.length,'relay initially sends a bounded preview');
  await assistant.evaluate(()=>{window.aiClipboardWrites=[];window.runtime={...window.runtime,ClipboardSetText:async text=>{window.aiClipboardWrites.push(text);return true;}};});
  const originalBefore=originalRequests.length;
  await assistant.click(finalReplySelector+' .ai-message-source-toggle');
  await assistant.waitForFunction((selector,text)=>document.querySelector(selector+' .ai-message-source')?.textContent===text,{timeout:15000},finalReplySelector,longSelectReply);
  assert.equal(await assistant.$eval(finalReplySelector+' .ai-message-content',element=>element.textContent),longSelectReply);
  const chunks=originalRequests.slice(originalBefore);assert(chunks.length>=3,'long reply must cross the real relay in multiple original-text requests');
  assert.equal(chunks[0].offset,0);assert(chunks.every((item,index)=>index===0||item.offset>chunks[index-1].offset));assert.equal(new Set(chunks.map(item=>item.sourceId)).size,1);
  assert.equal(await assistant.evaluate(()=>window.aiClipboardWrites.length),0);
  await assistant.click(finalReplySelector+' .ai-message-copy');await assistant.waitForFunction(()=>window.aiClipboardWrites.length===1);
  assert.equal(await assistant.evaluate(()=>window.aiClipboardWrites[0]),longSelectReply);
  checks.push('A >128000-character Chinese/emoji reply traverses real get_original relay chunks, preserves emoji at the chunk boundary and trailing newline, and copies the exact full source only through a clipboard spy');
  checks.push('AI-authorized session switch continues its own tool loop');
  for(const action of ['stop','switch','close','lock']) {
    await page.evaluate(id=>activate(id),session1);await assistant.click('#ai-clear');mode='late';
    const before=requestCount;await send('等待并验证取消');
    const deadline=Date.now()+5000;while(requestCount===before&&Date.now()<deadline)await new Promise(r=>setTimeout(r,20));assert(requestCount>before);
    if(action==='stop'){await assistant.waitForSelector('#ai-stop:not([hidden])',{visible:true});await assistant.click('#ai-stop');}
    if(action==='close')await assistant.close();
    if(action==='switch')await page.evaluate(id=>activate(id),session2);
    if(action==='lock') {const {grant}=await api('/api/security-lock/authorize',{});await api('/api/security-lock/settings',{grant,enabled:true,passwordEnabled:true,password:'fixture-ai-lock',idleSeconds:0});await api('/api/security-lock/lock',{});await page.waitForSelector('#security-lock-screen[open]');}
    await idle();await new Promise(r=>setTimeout(r,1500));
    if(action==='lock') {await api('/api/security-lock/unlock',{method:'password',value:'fixture-ai-lock'});await page.waitForFunction(()=>!DengSecurityLock.isLocked());}
    const listed=await api(`/api/sessions/${session1}/files?path=/home/bitsflow`);
    assert(!JSON.stringify(listed).includes('ai-must-not-run'));
    checks.push(action+' cancels delayed model response before any terminal input');
    if(action==='close'){assert.equal(await page.evaluate(()=>DengShellAI.enabled),false);await openAI();assert.equal(await assistant.$eval('#ai-enabled',e=>e.checked),false);await assistant.click('#ai-enabled');}
  }
  // Human activity in the detached assistant counts as workspace activity,
  // whereas its relay polling and passive model updates must not reset idle.
  await openAI();await assistant.bringToFront();
  const {grant}=await api('/api/security-lock/authorize',{method:'password',value:'fixture-ai-lock'});
  await api('/api/security-lock/settings',{grant,enabled:true,passwordEnabled:true,idleSeconds:2});
  for(let index=0;index<7;index++) {
    await assistant.type('#ai-prompt','x');await new Promise(resolve=>setTimeout(resolve,650));
    assert.equal((await api('/api/security-lock/status')).locked,false,'typing in AI must count as real user activity');
  }
  await new Promise(resolve=>setTimeout(resolve,3200));
  assert.equal((await api('/api/security-lock/status')).locked,true,'relay polling must not keep workspace unlocked');
  checks.push('Trusted AI-window input resets idle; passive relay traffic does not');
  assert.deepEqual(errors,[]);
  fs.writeFileSync(stage+'/ai-browser-results.json',JSON.stringify({passed:true,checks,requestCount,completedResponses,originalRequests,geometry,errors},null,2));
  console.log('PASS',checks);
} catch(error) {if(page)await page.screenshot({path:stage+'/failure.png'}).catch(()=>{});if(assistant&&!assistant.isClosed())await assistant.screenshot({path:stage+'/failure-ai.png'}).catch(()=>{});fs.writeFileSync(stage+'/failure.json',JSON.stringify({error:error.stack,errors,results},null,2));throw error;}
finally {await browser.close();proxy.closeAllConnections();provider.closeAllConnections();await Promise.all([new Promise(resolve=>proxy.close(resolve)),new Promise(resolve=>provider.close(resolve))]);}
