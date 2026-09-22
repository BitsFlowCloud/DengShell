// Replay NQ's progress/clear sequences in the real frontend without running a benchmark.
import fs from 'node:fs';
import assert from 'node:assert/strict';
const [stage,chrome,modulePath]=process.argv.slice(2);
const {default:puppeteer}=await import(modulePath),fixture=JSON.parse(fs.readFileSync(stage+'/browser-fixture.json'));
const setup=await fetch(new URL('/api/appearance/patch',fixture.url),{method:'POST',headers:{'Content-Type':'application/json','X-CloudShell-Token':fixture.token},body:JSON.stringify({onboardingCompleted:true,startupAnimation:false,uiScale:1})});assert(setup.ok);
const browser=await puppeteer.launch({executablePath:chrome,headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const page=await browser.newPage(),errors=[],checks=[];page.on('pageerror',e=>errors.push(e.message));
const pause=ms=>new Promise(r=>setTimeout(r,ms));
try{
 await page.setViewport({width:1440,height:1000});await page.goto(fixture.url,{waitUntil:'networkidle0'});
 await page.evaluate(()=>{
  document.querySelectorAll('dialog[open]').forEach(d=>d.close());setDrawer(false);pollStats=()=>{};pollNetwork=()=>{};
  const id='scrollback-qa';profiles.push({id,name:'NQ scrollback test',host:'fixture.invalid',user:'qa',port:22});const state={...makeSessionState({id,profileId:id,home:'/'}),localOnly:true};sessions.set(id,state);createTerminal(state);state.connectionView.remove();state.localOnly=false;state.ready=true;state.term.options.disableStdin=false;window.qaFrames=[];state.ws={readyState:WebSocket.OPEN,send(data){qaFrames.push(JSON.parse(data))},close(){}};activate(id);
  window.qaWrite=text=>new Promise(r=>state.term.write(text,r));
  window.qaRead=()=>{const b=state.term.buffer.active;return{base:b.baseY,view:b.viewportY,first:b.getLine(b.viewportY)?.translateToString(true),length:b.length,type:b.type}};
 });await pause(150);
 for(const scale of [1,.9,1.25]){
  await page.evaluate(async scale=>{appearance.uiScale=scale;applyUIScale();await new Promise(r=>setTimeout(r,120));current().term.reset();await qaWrite(Array.from({length:260},(_,i)=>'历史行 '+i+' 中文 \x1b[31mRED\x1b[0m\r\n').join(''));current().term.focus();window.qaProgress=setInterval(()=>current().term.write('\r\x1b[36m[192.0.2.1]# NQ 测试中...... ⠋ 50%\x1b[0m'),25)},scale);
  await page.hover('.terminal-session:not([hidden]) .xterm-screen');for(let i=0;i<6;i++){await page.mouse.wheel({deltaY:-500});await pause(35)}await pause(100);
  const before=await page.evaluate(()=>qaRead());assert(before.view<before.base);
  await page.evaluate(async()=>{
   // ncurses clear at the next NQ stage, fragmented across network frames.
   clearInterval(qaProgress);
   const bytes=new TextEncoder().encode('\x1b[H\x1b[2J\x1b[3J新阶段 中文\r\n');
   for(let i=0;i<bytes.length;i+=2)await qaWrite(bytes.slice(i,i+2));
   for(let i=0;i<12;i++){await qaWrite('\r进度 '+i+'\x1b[2K\r\n结果 '+i+'\r\n\x1b[1A\x1b[2K结果更新\r\n');await new Promise(r=>setTimeout(r,20))}
   clearInterval(qaProgress);
  });await pause(100);
  const after=await page.evaluate(()=>qaRead());assert.equal(after.view,before.view);assert.equal(after.first,before.first);assert(after.base>before.base,JSON.stringify({before,after,scale}));
  checks.push(`UI scale ${scale}: wheel scrolling remains anchored through live progress, fragmented clear, cursor-up and new output`);
 }
 await page.evaluate(()=>{appearance.uiScale=1;applyUIScale();current().term.focus();qaFrames=[]});
 await page.keyboard.down('Control');await page.keyboard.press('KeyC');await page.keyboard.up('Control');await pause(70);
 let state=await page.evaluate(()=>qaRead());assert.equal(state.view,state.base);assert.deepEqual(await page.evaluate(()=>qaFrames.filter(f=>f.type==='input').map(f=>f.data)),['\x03']);
 await page.evaluate(()=>qaWrite('继续输出\r\n'));state=await page.evaluate(()=>qaRead());assert.equal(state.view,state.base);checks.push('Ctrl+C returns to bottom, sends one interrupt, and resumes following output');
 await page.evaluate(()=>{current().term.scrollToLine(30);qaFrames=[]});await pause(50);await page.evaluate(()=>qaWrite('\x1b[6n'));await pause(50);assert.equal((await page.evaluate(()=>qaRead())).view,30,'cursor-position replies must not resume following');
 await page.keyboard.type('a');await pause(50);state=await page.evaluate(()=>qaRead());assert.equal(state.view,state.base);checks.push('Terminal replies preserve reading position; genuine keyboard input resumes following');
 const tui=await page.evaluate(async()=>{
  const t=current().term,normal=()=>Array.from({length:t.buffer.normal.length},(_,i)=>t.buffer.normal.getLine(i).translateToString(false)).join('\n');
  const before=normal();await qaWrite('\x1b[?1049h\x1b[?1000h\x1b[2J\x1b[HVim screen\r\n\x1b[2J\x1b[3J\x1b[HNext screen');
  const result={type:t.buffer.active.type,base:t.buffer.active.baseY,historyUnchanged:normal()===before,mouse:t.modes.mouseTrackingMode};
  await qaWrite('\x1b[?1000l\x1b[?1049l');result.restored=normal()===before;return result;
 });assert.deepEqual(tui,{type:'alternate',base:0,historyUnchanged:true,mouse:'vt200',restored:true});checks.push('Alternate-screen editor redraw and mouse reporting preserve the normal buffer');
 const margins=await page.evaluate(async()=>{const t=current().term,before=t.buffer.normal.baseY;await qaWrite('\x1b[2;6r\x1b[2J');const after=t.buffer.normal.baseY;await qaWrite('\x1b[r');return{before,after}});assert.equal(margins.after,margins.before);checks.push('Partial scrolling regions retain standard erase behavior');
 await page.evaluate(()=>document.querySelector('#clear-terminal').click());state=await page.evaluate(()=>qaRead());assert.equal(state.base,0);
 assert(!await page.evaluate(()=>Array.from({length:current().term.buffer.normal.length},(_,i)=>current().term.buffer.normal.getLine(i).translateToString(true)).some(s=>s.includes('历史行'))));checks.push('Explicit Clear Terminal removes retained history');
 await page.evaluate(async()=>{current().term.options.scrollback=50;await qaWrite(Array.from({length:180},(_,i)=>'Bounded '+i+'\r\n').join(''));for(let i=0;i<4;i++)await qaWrite('\x1b[H\x1b[2J\x1b[3J阶段 '+i+'\r\n')});assert(await page.evaluate(()=>current().term.buffer.normal.length<=current().term.rows+50));checks.push('Repeated clears obey the configured scrollback limit');
 assert.deepEqual(errors,[]);await page.screenshot({path:stage+'/terminal-scrollback.png'});fs.writeFileSync(stage+'/terminal-scrollback-browser.json',JSON.stringify({passed:true,checks,errors},null,2));console.log('PASS',checks);
}catch(e){await page.screenshot({path:stage+'/terminal-scrollback-failure.png'});throw e}
finally{await browser.close()}
