// Real frontend rendering: preserve intentional ANSI contrast and backgrounds.
// Uses an isolated TestNotesHistoryBrowserFixture, never an actual SSH server.
// Optional streams JSON contains independently parsed complete ANSI resources.
import fs from 'node:fs';
import assert from 'node:assert/strict';
const [stage,chrome,modulePath,streamsPath]=process.argv.slice(2);
const {default:puppeteer}=await import(modulePath);
const fixture=JSON.parse(fs.readFileSync(stage+'/browser-fixture.json'));
const setup=await fetch(new URL('/api/appearance/patch',fixture.url),{method:'POST',headers:{'Content-Type':'application/json','X-CloudShell-Token':fixture.token},body:JSON.stringify({onboardingCompleted:true,startupAnimation:false,uiScale:1})});assert(setup.ok);
const browser=await puppeteer.launch({executablePath:chrome,headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const page=await browser.newPage(),errors=[],checks=[],resources=[];
page.on('pageerror',e=>errors.push(e.message));
const wait=ms=>new Promise(r=>setTimeout(r,ms));
try {
 await page.setViewport({width:1720,height:1250});await page.emulateMediaFeatures([{name:'prefers-reduced-motion',value:'reduce'}]);
 await page.goto(fixture.url,{waitUntil:'networkidle0'});
 await page.evaluate(async()=>{
  await chooseAppearance({onboardingCompleted:true,startupAnimation:false,uiScale:1});
  document.querySelectorAll('dialog[open]').forEach(d=>d.close());setDrawer(false);pollStats=()=>{};pollNetwork=()=>{};
  const id='ansi-colors-qa';profiles.push({id,name:'ANSI 配色验证',host:'fixture.invalid',user:'qa',port:22});const s={...makeSessionState({id,profileId:id,home:'/'}),localOnly:true};sessions.set(id,s);createTerminal(s);s.connectionView.remove();activate(id);s.fit.fit=()=>{};
  window.qaWrite=text=>new Promise(r=>s.term.write(text,r));
  window.qaCell=(x,y)=>{const c=s.term.buffer.normal.getLine(y)?.getCell(x);return c?{text:c.getChars(),width:c.getWidth(),fg:c.isFgRGB()?[(c.getFgColor()>>16)&255,(c.getFgColor()>>8)&255,c.getFgColor()&255]:null,bg:c.isBgRGB()?[(c.getBgColor()>>16)&255,(c.getBgColor()>>8)&255,c.getBgColor()&255]:null,underline:!!c.isUnderline()}:null};
  window.qaDOM=()=>{
   const width=s.term._core._renderService._renderer.value.dimensions.css.cell.width;
   return {top:s.term.buffer.normal.viewportY,rows:[...s.host.querySelectorAll('.xterm-rows > div')].map(row=>[...row.querySelectorAll(':scope > span')].map(span=>{
    const style=getComputedStyle(span),parse=value=>{const a=value.match(/[\d.]+/g)?.map(Number)||[];return a.length===4&&a[3]===0?null:a.slice(0,3)};
    return{x:Number(span.dataset.terminalColumn),text:span.textContent,fg:parse(style.color),bg:parse(style.backgroundColor),underline:style.textDecorationLine.includes('underline'),left:parseFloat(style.left),width:parseFloat(style.width),cellWidth:width};
   }))};
  };
  window.qaSample=[
   '\x1b[48;2;0;0;0m\x1b[38;2;0;0;0mHIDDEN 黑底黑字',
   'INHERITED 跨行继承黑底黑字',
   '\x1b[38;2;0;0;255mBLUE 黑底深蓝',
   '\x1b[0;38;2;16;16;16mDARK 低亮度',
   '\x1b[0;38;2;255;0;0mRED 红色',
   '\x1b[0;38;2;0;255;0mGREEN 绿色',
   '\x1b[0;38;2;0;0;255mBLUE 蓝色',
   '\x1b[0;38;5;21mPALETTE 256色',
   '\x1b[0;34mANSI 基础色',
   '\x1b[0;7mINVERSE 反色',
   '\x1b[0;48;2;255;255;255;38;2;0;0;0mBLACK_ON_WHITE',
   '\x1b[0;48;2;0;0;0;38;2;255;255;255mWHITE_ON_BLACK',
   '\x1b[0;2;38;2;0;0;255mDIM 弱化文字',
   '\x1b[0;38;2;0;0;255m████ ▄▄▄▄ ────',
   '\x1b[0;4;38;2;42;90;130mhttps://example.test/ 下划线\x1b[0m'
  ].join('\r\n');
  window.qaRender=async()=>{const t=s.term;t.reset();t.resize(120,36);await qaWrite('\x1b[?25l'+qaSample);await new Promise(r=>setTimeout(r,80))};
 });
 const foregrounds=[[0,0,0],[0,0,0],[0,0,255],[16,16,16],[255,0,0],[0,255,0],[0,0,255],[0,0,255],[143,181,209],[0,0,0],[0,0,0],[255,255,255],[0,0,255],[0,0,255],[42,90,130]];
 for(const theme of ['dark','light']){
  await page.evaluate(theme=>CloudShellTheme.set(theme),theme);
  for(const backgroundId of ['builtin:none','builtin:paperfolds'])for(const opacity of backgroundId==='builtin:none'?[0]:[0,.42,1]){
   await page.evaluate(async settings=>{await chooseAppearance(settings);await qaRender()},{backgroundId,backgroundOpacity:opacity});
   const rendered=await page.evaluate(()=>({dom:qaDOM(),option:current().term.options.minimumContrastRatio,raw:qaCell(0,2)}));
   assert.equal(rendered.option,1);assert.deepEqual(rendered.raw.fg,[0,0,255]);
   for(let row=0;row<foregrounds.length;row++)assert.deepEqual(rendered.dom.rows[row][0].fg,foregrounds[row],`${theme}/${backgroundId}/${opacity}/row ${row}`);
   for(const row of [0,1,2])assert.deepEqual(rendered.dom.rows[row][0].bg,[0,0,0],'background must persist across newlines');
   assert.deepEqual(rendered.dom.rows[10][0].bg,[255,255,255]);assert.deepEqual(rendered.dom.rows[11][0].bg,[0,0,0]);assert(rendered.dom.rows[14][0].underline);
   checks.push({theme,backgroundId,opacity,renderedColors:'exact match'});
  }
  const copied=await page.evaluate(()=>{const t=current().term;t.select(0,1,30);const result=t.getSelection();t.clearSelection();return result});assert(copied.startsWith('INHERITED 跨行继承黑底黑字'));
  const change=await page.evaluate(async()=>{const before=qaDOM();const slider=document.querySelector('#background-opacity');slider.value='15';slider.dispatchEvent(new Event('input',{bubbles:true}));await new Promise(r=>setTimeout(r,60));return{before:before.rows[0][0],after:qaDOM().rows[0][0],opacity:getComputedStyle(document.querySelector('.terminal-panel'),'::before').opacity}});
  assert.deepEqual(change.after,change.before,'changing image opacity must not recolor the terminal');assert.equal(Number(change.opacity),.15);
 }
 if(streamsPath){
  const streams=JSON.parse(fs.readFileSync(streamsPath));
  for(const theme of ['dark','light']){
   await page.evaluate(async theme=>{CloudShellTheme.set(theme);await chooseAppearance({backgroundId:'builtin:paperfolds',backgroundOpacity:.42,terminalFontSize:16});},theme);
   for(const stream of streams){
    await page.evaluate(async stream=>{const t=current().term;t.reset();t.resize(stream.cols,36);await qaWrite('\x1b[?25l'+stream.text.replaceAll('\n','\r\n'));},stream);
    const model=await page.evaluate(cells=>cells.map(c=>qaCell(c.x,c.y)),stream.cells);
    for(let i=0;i<stream.cells.length;i++){const{x,y,...expected}=stream.cells[i];assert.deepEqual(model[i],expected,`${theme}/${stream.name}: buffer ${x},${y}`)}
    const rows=new Map();
    for(let top=0;top<stream.rows;top+=30){await page.evaluate(top=>current().term.scrollToLine(top),top);await wait(60);const dom=await page.evaluate(()=>qaDOM());dom.rows.forEach((spans,i)=>rows.set(dom.top+i,spans))}
    for(const c of stream.cells){
     const span=(rows.get(c.y)||[]).find(s=>c.x>=s.x&&c.x<s.x+s.width/s.cellWidth-.01);assert(span,`${stream.name}: missing rendered cell`);
     if(c.fg)assert.deepEqual(span.fg,c.fg,`${theme}/${stream.name}: foreground ${c.x},${c.y}`);
     assert.deepEqual(span.bg,c.bg,`${theme}/${stream.name}: background ${c.x},${c.y}`);assert.equal(span.underline,c.underline);
     assert(Math.abs(span.left-span.x*span.cellWidth)<.1,'misaligned terminal span');
    }
    resources.push({theme,name:stream.name,characters:stream.cells.length,bufferAndRendering:'exact match'});
   }
   const sample=streams.find(s=>s.name==='sponsor').text+streams.find(s=>s.name==='ad1').text;
   await page.evaluate(async sample=>{document.querySelector('#help-guide-close')?.click();document.querySelectorAll('dialog[open]').forEach(d=>d.close());const t=current().term;t.reset();t.resize(90,36);await qaWrite('\x1b[?25l'+sample.replaceAll('\n','\r\n'));t.scrollToTop();},sample);await wait(100);
   const clip=await page.evaluate(()=>{const b=current().host.getBoundingClientRect(),d=current().term._core._renderService._renderer.value.dimensions.css.cell;return{x:b.x,y:b.y,width:Math.ceil(d.width*74),height:Math.ceil(d.height*24)}});
   await page.screenshot({path:stage+`/evidence/ansi-correct-${theme}.png`,clip});
  }
 }
 assert.deepEqual(errors,[]);
 fs.writeFileSync(stage+'/ansi-colors-browser.json',JSON.stringify({passed:true,checks,resources,copy:'preserved',errors},null,2));
 console.log(`PASS: ${checks.length} appearance combinations and ${resources.length} complete streams; exact rendered colors, inherited backgrounds, Chinese cells, grid, underline and copy`);
}finally{await browser.close()}
