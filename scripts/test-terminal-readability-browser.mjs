// Real xterm rendering against the app's dark/light and image backgrounds.
// All configuration changes use an isolated TestNotesHistoryBrowserFixture.
import fs from 'node:fs';
import assert from 'node:assert/strict';
const [stage,chrome,modulePath,ansiDirectory]=process.argv.slice(2);
const {default:puppeteer}=await import(modulePath);
const fixture=JSON.parse(fs.readFileSync(stage+'/browser-fixture.json'));
const browser=await puppeteer.launch({executablePath:chrome,headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const page=await browser.newPage(),errors=[],checks=[];
page.on('pageerror',error=>errors.push(error.message));
const wait=ms=>new Promise(r=>setTimeout(r,ms));
try {
 await page.setViewport({width:1740,height:1100});
 await page.emulateMediaFeatures([{name:'prefers-reduced-motion',value:'reduce'}]);
 await page.goto(fixture.url,{waitUntil:'networkidle0'});
 await page.evaluate(async()=>{
  await chooseAppearance({onboardingCompleted:true,startupAnimation:false,uiScale:1});
  document.querySelectorAll('dialog[open]').forEach(d=>d.close());setDrawer(false);pollStats=()=>{};pollNetwork=()=>{};
  const id='readability-qa';profiles.push({id,name:'终端清晰度验证',host:'fixture.invalid',user:'qa',port:22});
  const state={...makeSessionState({id,profileId:id,home:'/'}),localOnly:true};sessions.set(id,state);createTerminal(state);state.connectionView.remove();activate(id);
  window.qaWrite=text=>new Promise(r=>state.term.write(text,r));
  window.qaText=[
   '\x1b[38;2;0;0;0mBLACK 黑色文字','\x1b[38;2;0;0;255mBLUE 深蓝文字',
   '\x1b[38;2;255;0;0mRED 红色文字','\x1b[38;2;0;255;0mGREEN 绿色文字',
   '\x1b[38;2;255;255;255mWHITE 白色文字','\x1b[34mANSI 基础蓝色',
   '\x1b[38;5;21mPALETTE 256色蓝色','\x1b[7mINVERSE 反色文字',
   '\x1b[48;2;255;255;255m\x1b[38;2;0;0;0mBLACK_ON_WHITE',
   '\x1b[48;2;0;0;0m\x1b[38;2;255;255;255mWHITE_ON_BLACK',
   '\x1b[38;2;0;0;255m████ ▄▄▄▄ ────',
   '\x1b[2;38;2;0;0;255mDIM 弱化文字'
  ].map(line=>'\x1b[0m'+line+'\x1b[0m').join('\r\n');
  window.qaRender=async()=>{state.term.reset();await qaWrite('\x1b[?25l'+qaText);await new Promise(r=>setTimeout(r,80))};
  window.qaInspect=()=>{
   const rgb=value=>value.match(/[\d.]+/g).slice(0,3).map(Number);
   const luminance=channels=>channels.map(x=>{x/=255;return x<=.04045?x/12.92:((x+.055)/1.055)**2.4}).reduce((n,x,i)=>n+x*[.2126,.7152,.0722][i],0);
   const ratio=(a,b)=>{const [hi,lo]=[luminance(a),luminance(b)].sort((x,y)=>y-x);return(hi+.05)/(lo+.05)};
   const reference=terminalContrastBackground().slice(1,7).match(/../g).map(x=>parseInt(x,16));
   const rows=[...state.host.querySelectorAll('.xterm-rows > div')].slice(0,12).map((row,i)=>{
    const span=[...row.querySelectorAll('span')].find(el=>el.textContent.trim()),style=getComputedStyle(span);
    const foreground=rgb(style.color),background=style.backgroundColor;
    const opaque=background.startsWith('rgb(')||background.endsWith(', 1)');
    return {i,text:row.textContent,foreground,background,contrast:ratio(foreground,opaque?rgb(background):reference)};
   });
   return{rows,reference,option:state.term.options.minimumContrastRatio,rawBlue:state.term.buffer.normal.getLine(1).getCell(0).getFgColor(),background:state.term.options.theme.background};
  };
 });
 for(const theme of ['dark','light']){
  await page.evaluate(theme=>CloudShellTheme.set(theme),theme);
  for(const backgroundId of ['builtin:none','builtin:paperfolds']){
   for(const opacity of backgroundId==='builtin:none'?[0]:[0,.42,1]){
    await page.evaluate(async({backgroundId,opacity})=>{await chooseAppearance({backgroundId,backgroundOpacity:opacity});await qaRender()}, {backgroundId,opacity});
    const result=await page.evaluate(()=>qaInspect());
    assert.equal(result.option,4.5);assert.equal(result.rawBlue,255);assert(result.background.endsWith('00'));
    for(const row of result.rows.slice(0,10))assert(row.contrast>=4.45,JSON.stringify({theme,backgroundId,opacity,row}));
    assert.deepEqual(result.rows[10].foreground,[0,0,255],'block graphics keep original colors');
    assert(result.rows[11].contrast>=2.2,'dim text remains distinguishable');
    checks.push({theme,backgroundId,opacity,minContrast:Math.min(...result.rows.slice(0,10).map(row=>row.contrast))});
   }
  }
  // White is the brightest possible image: verify the CSS blending bound.
  await page.evaluate(async()=>{document.documentElement.style.setProperty('--terminal-background','linear-gradient(white,white)');await qaRender()});
  await page.screenshot({path:stage+`/evidence/readability-white-${theme}.png`});
  const stable=await page.evaluate(()=>{const t=current().term;t.select(0,1,20);return{copy:t.getSelection(),raw:t.buffer.normal.getLine(1).getCell(0).getFgColor()}});
  assert(stable.copy.startsWith('BLUE 深蓝文字'));assert.equal(stable.raw,255);
  await page.evaluate(()=>current().term.clearSelection());
  // Changing the opacity slider updates existing terminals immediately.
  const changed=await page.evaluate(()=>{const before=current().term.options.theme.background;const slider=document.querySelector('#background-opacity');slider.value='15';slider.dispatchEvent(new Event('input',{bubbles:true}));return{before,after:current().term.options.theme.background,expected:terminalContrastBackground()}});
  assert.notEqual(changed.before,changed.after);assert.equal(changed.after,changed.expected);
 }
 const themeChange=await page.evaluate(()=>{CloudShellTheme.set('dark');const before=current().term.options.theme.background;CloudShellTheme.set('light');return{before,after:current().term.options.theme.background,expected:terminalContrastBackground()}});
 assert.notEqual(themeChange.before,themeChange.after);assert.equal(themeChange.after,themeChange.expected);
 if(ansiDirectory){
  const files=['sponsor','ad2','ad3','ad4'].map(name=>fs.readFileSync(ansiDirectory+'/'+name+'.ans','utf8').split('\n'));
  let art='正在运行 IP 质量测试...\r\n';
  for(let pair=0;pair<2;pair++)for(let row=0;row<12;row++)art+=(files[pair*2][row]||'')+'\x1b[0m     '+(files[pair*2+1][row]||'')+'\x1b[0m\r\n';
  for(const theme of ['dark','light']){
   await page.evaluate(async({theme,art})=>{
    CloudShellTheme.set(theme);await chooseAppearance({backgroundId:'builtin:none'});await chooseAppearance({backgroundId:'builtin:paperfolds',backgroundOpacity:.42,terminalFontSize:13});
    const t=current().term;t.resize(160,34);t.reset();await qaWrite('\x1b[?25l'+art);
   },{theme,art});
   await wait(180);await page.screenshot({path:stage+`/evidence/readability-nq-${theme}.png`});
  }
 }
 assert.deepEqual(errors,[]);
 fs.writeFileSync(stage+'/readability-browser.json',JSON.stringify({passed:true,checks,copyAndRawColors:'preserved',liveThemeAndSlider:'passed',errors},null,2));
 console.log('PASS: '+checks.length+' theme/background combinations, original ANSI data, copy, explicit backgrounds, inverse/dim/block graphics, live theme and opacity updates');
}finally{await browser.close()}
