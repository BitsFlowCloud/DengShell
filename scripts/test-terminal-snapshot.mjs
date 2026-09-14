// Actual bundled xterm + SerializeAddon in isolated headless Chromium; no SSH.
import {createServer} from 'node:http';
import {readFile,mkdtemp,rm} from 'node:fs/promises';
import {spawn} from 'node:child_process';
import {join,dirname,resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {tmpdir} from 'node:os';
const root=resolve(dirname(fileURLToPath(import.meta.url)),'..');
const assets=new Map([['/xterm.js','web/vendor/xterm.js'],['/serialize.js','web/vendor/addon-serialize.js'],['/snapshot.js','web/terminal-snapshot.js'],['/app.js','web/app.js']]);
const server=createServer(async(req,res)=>{try{if(req.url==='/'){res.setHeader('Content-Type','text/html');res.end('<script src="/xterm.js"></script><script src="/serialize.js"></script><script src="/snapshot.js"></script>');return}const file=assets.get(req.url);if(!file){res.writeHead(404).end();return}res.setHeader('Content-Type','text/javascript');res.end(await readFile(join(root,file)))}catch(error){res.writeHead(500).end(String(error))}});
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
const directory=await mkdtemp(join(process.env.TMPDIR||tmpdir(),'dengshell-vt-snapshot-'));
const child=spawn(process.env.CHROME_BINARY||'google-chrome',['--headless=new','--no-sandbox','--disable-dev-shm-usage','--disable-background-networking','--disable-gpu','--remote-debugging-address=127.0.0.1','--remote-debugging-port=0','--user-data-dir='+directory,'http://127.0.0.1:'+server.address().port],{stdio:'ignore'});
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));let ws;
try{
 let port;for(let i=0;i<100;i++){try{port=(await readFile(join(directory,'DevToolsActivePort'),'utf8')).split('\n')[0];break}catch{}await pause(50)}if(!port)throw Error('Chromium did not start');
 const targets=await(await fetch('http://127.0.0.1:'+port+'/json/list')).json();ws=new WebSocket(targets.find(t=>t.type==='page').webSocketDebuggerUrl);await new Promise((resolve,reject)=>{ws.onopen=resolve;ws.onerror=reject});
 let counter=0;const jobs=new Map();ws.onmessage=e=>{const m=JSON.parse(e.data);if(m.id&&jobs.has(m.id)){const p=jobs.get(m.id);jobs.delete(m.id);m.error?p.reject(m.error):p.resolve(m.result)}};
 const cdp=(method,params={})=>new Promise((resolve,reject)=>{const id=++counter;jobs.set(id,{resolve,reject});ws.send(JSON.stringify({id,method,params}))});
 const evaluate=async expression=>{const r=await cdp('Runtime.evaluate',{expression,awaitPromise:true,returnByValue:true});if(r.exceptionDetails)throw Error(JSON.stringify(r.exceptionDetails));return r.result.value};
 for(let i=0;i<100;i++){if(await evaluate('!!window.DengTerminalSnapshot && !!window.SerializeAddon'))break;await pause(50)}
 const result=await evaluate(`(async()=>{
 const results=[];const check=(name,ok)=>{if(!ok)throw Error(name);results.push(name)};
 const write=(t,s)=>new Promise(resolve=>t.write(s,resolve));
 function make(cols=20,rows=8){const term=new Terminal({cols,rows,scrollback:10000,allowProposedApi:true});const element=document.createElement('div');element.style.cssText='width:800px;height:400px';document.body.append(element);term.open(element);const addon=new SerializeAddon.SerializeAddon();term.loadAddon(addon);return {term,addon}}
 function shape(term){return JSON.stringify({state:DengTerminalSnapshot.capture(term),normal:read(term.buffer.normal),alt:read(term.buffer.alternate)})}
 function read(buffer){return Array.from({length:buffer.length},(_,i)=>({text:buffer.getLine(i).translateToString(false),wrapped:buffer.getLine(i).isWrapped}))}
 async function roundtrip(source){const saved=DengTerminalSnapshot.capture(source.term), restored=make(source.term.cols,source.term.rows);let dataEvents=0;restored.term.onData(()=>dataEvents++);await write(restored.term,source.addon.serialize());DengTerminalSnapshot.restore(restored.term,JSON.parse(JSON.stringify(saved)));check('runtime snapshot equality',JSON.stringify(DengTerminalSnapshot.capture(restored.term))===JSON.stringify(saved));check('restore sends no terminal input',dataEvents===0);return restored}
 const source=make();
 await write(source.term,'NORMAL 中文\\r\\n'+String.fromCharCode(27)+'[2;7r'+String.fromCharCode(27)+'[3;4H'+String.fromCharCode(27)+'[31m'+String.fromCharCode(27)+'[?1049h'+String.fromCharCode(27)+'[2;6r'+String.fromCharCode(27)+'[?25l'+String.fromCharCode(27)+'[?1002h'+String.fromCharCode(27)+'[?1006h'+String.fromCharCode(27)+'[?2004h'+String.fromCharCode(27)+'[4;5H'+String.fromCharCode(27)+'7'+String.fromCharCode(27)+'[2;1H'+'ALT 中文\\r\\n');
 const target=await roundtrip(source);
 check('normal and alternate scroll region',target.term._core._bufferService.buffers.normal.scrollTop===1&&target.term._core._bufferService.buffers.normal.scrollBottom===6&&target.term._core._bufferService.buffers.alt.scrollTop===1&&target.term._core._bufferService.buffers.alt.scrollBottom===5);
 check('hidden cursor and SGR mouse mode',target.term._core.coreService.isCursorHidden&&target.term._core.coreMouseService.activeProtocol==='DRAG'&&target.term._core.coreMouseService.activeEncoding==='SGR');
 const continuation=String.fromCharCode(27)+'[6;1HSCROLL\\r\\nNEXT'+String.fromCharCode(27)+'8Z';await write(source.term,continuation);await write(target.term,continuation);check('subsequent partial-region scroll and saved cursor agree',shape(source.term)===shape(target.term));
 await write(source.term,String.fromCharCode(27)+'[?1049lEND');await write(target.term,String.fromCharCode(27)+'[?1049lEND');check('leaving alternate restores same normal screen and cursor',shape(source.term)===shape(target.term));
 const wrapping=make(8,4);await write(wrapping.term,'12345678');const wrapped=await roundtrip(wrapping);await write(wrapping.term,'中x');await write(wrapped.term,'中x');check('pending autowrap and CJK continuation agree',shape(wrapping.term)===shape(wrapped.term));
 const charset=make();await write(charset.term,String.fromCharCode(27)+'[3g'+String.fromCharCode(27)+'[1;6H'+String.fromCharCode(27)+'H'+String.fromCharCode(27)+'(0lq');const ch=await roundtrip(charset);await write(charset.term,'qk\\tX');await write(ch.term,'qk\\tX');check('line drawing charset and custom tabs agree',shape(charset.term)===shape(ch.term));
 const good=DengTerminalSnapshot.capture(target.term),before=shape(target.term);for(const mutation of [s=>s.normal.top=-1,s=>s.alternate.bottom=999,s=>s.mouseEncoding='unknown',s=>s.normal.savedAttrs.fg=NaN,s=>s.charsets=[{evil:'x'}]]){const bad=structuredClone(good);mutation(bad);let rejected=false;try{DengTerminalSnapshot.restore(target.term,bad)}catch{rejected=true}check('invalid snapshot rejected before mutation',rejected&&shape(target.term)===before)}
 const app=await(await fetch('/app.js')).text();
 const reconnectCode=app.slice(app.indexOf('function writeTerminalAndWait('),app.indexOf('function showConnectionProgress('));
 const reconnectSource=make(40,8),reconnectTarget=make(80,12);
 const oldState={id:'old',term:reconnectSource.term,serialize:reconnectSource.addon},newState={id:'new',term:reconnectTarget.term};
 let disposed=false,replayedInput=0;
 newState.term.onData(()=>replayedInput++);
 const reconnect=new Function('closeSession','current',reconnectCode+';return {preserveReconnectTerminal,restoreReconnectTerminal,writeTerminalAndWait};')(async id=>{if(id!=='old')throw Error('wrong terminal closed');oldState.closed=true;oldState.term.dispose();disposed=true},()=>null);
 // Queued output must be parsed before the old renderer is destroyed.
 oldState.term.write(Array.from({length:180},(_,i)=>'行 '+i+' 中文 '+String.fromCharCode(27)+'[31mRED'+String.fromCharCode(27)+'[0m\\r\\n').join(''));
 oldState.term.write(String.fromCharCode(27)+'[?1049h'+String.fromCharCode(27)+'[?1002h'+String.fromCharCode(27)+'[?2004hTUI');
 await reconnect.preserveReconnectTerminal(oldState,newState);
 check('reconnect disposes only after capturing queued output',disposed&&!!newState.reconnectScreen);
 await reconnect.restoreReconnectTerminal(newState);
 const retained=read(newState.term.buffer.normal).map(l=>l.text).join('\\n');
 check('reconnect retains old scrollback and Chinese text',retained.includes('行 0 中文 RED')&&retained.includes('行 179 中文 RED')&&retained.includes('以上为上一会话记录'));
 check('reconnect keeps colors',Array.from({length:newState.term.cols},(_,i)=>newState.term.buffer.normal.getLine(0).getCell(i)).some(cell=>cell.getChars()==='R'&&cell.getFgColor()===1));
 check('reconnect resets TUI alternate and input modes',newState.term.buffer.active.type==='normal'&&!newState.term.modes.bracketedPasteMode&&newState.term.modes.mouseTrackingMode==='none');
 check('reconnect restoration does not resend old commands',replayedInput===0);
 const closing=make(),closingState={term:closing.term};
 const drained=reconnect.writeTerminalAndWait(closingState,'queued');closingState.closed=true;for(const done of closingState.terminalWriteWaiters)done();closing.term.dispose();
 check('closing a tab releases a pending renderer write',await drained===false);
 return results;
})()`);
 for(const name of result)console.log('PASS:',name);
}catch(error){console.error(error);process.exitCode=1}finally{ws?.close();const exited=new Promise(resolve=>{if(child.exitCode!==null||child.signalCode!==null)resolve();else child.once('exit',resolve)});child.kill('SIGTERM');await exited;server.close();await rm(directory,{recursive:true,force:true,maxRetries:5,retryDelay:150})}
