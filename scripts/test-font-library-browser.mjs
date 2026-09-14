import assert from 'node:assert/strict';
import {readFile,writeFile,mkdir,rm,mkdtemp} from 'node:fs/promises';
import {join} from 'node:path';
import {tmpdir} from 'node:os';
import {createServer} from 'node:http';
const [stage,chrome,modulePath]=process.argv.slice(2);
if(!stage||!chrome||!modulePath)throw new Error('Usage: test-font-library-browser.mjs QA_DIR CHROME PUPPETEER_MODULE');
const {default:puppeteer}=await import(modulePath);
const fixture=JSON.parse(await readFile(join(stage,'browser-fixture.json'),'utf8'));
const profile=await mkdtemp(join(tmpdir(),'fontqa-'));
const browser=await puppeteer.launch({executablePath:chrome,headless:true,userDataDir:profile,args:['--no-sandbox','--disable-dev-shm-usage','--disable-background-networking']});
const checks=[],errors=[],requests=[];
const api=async(path,options={})=>{const response=await fetch(new URL(path,fixture.url),{...options,headers:{'X-CloudShell-Token':fixture.token,'Content-Type':'application/json'}});const result=await response.json();assert(response.ok,JSON.stringify(result));return result;};
const site=createServer(async(req,res)=>{
  try{const pathname=new URL(req.url,'http://localhost').pathname;const path=join(stage,'website',pathname.endsWith('/')?pathname+'index.html':pathname);const data=await readFile(path);const ext=path.split('.').at(-1);res.setHeader('Content-Type',({html:'text/html; charset=utf-8',js:'application/javascript',css:'text/css',json:'application/json',png:'image/png',svg:'image/svg+xml',zip:'application/zip'})[ext]||'application/octet-stream');res.setHeader('Content-Security-Policy',"default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; form-action 'none'");res.end(data);}catch{res.writeHead(404);res.end();}
});
await new Promise(resolve=>site.listen(0,'127.0.0.1',resolve));
try{
  // This endpoint belongs to the opt-in isolated fixture, never a user Store.
  assert(new URL(fixture.url).hostname==='127.0.0.1');
  for(const asset of (await api('/api/config')).assets) await api(`/api/assets/${asset.id}`,{method:'DELETE'});
  await api('/api/appearance/patch',{method:'POST',body:JSON.stringify({uiFontId:'builtin:ui-ibm-plex-sans-sc',fontId:'builtin:jetbrains-mono'})});
  await mkdir(join(stage,'screenshots'),{recursive:true});
  const page=await browser.newPage();await page.setViewport({width:1440,height:1000});
  page.on('pageerror',e=>errors.push(e.message));
  await page.setRequestInterception(true);page.on('request',r=>{requests.push(r.url());if(r.url().includes('/api/updates/check'))r.respond({status:200,contentType:'application/json',body:'{"status":"current"}'});else if(/^(http|https):/.test(r.url())&&!r.url().startsWith(new URL(fixture.url).origin))r.abort();else r.continue();});
  await page.goto(fixture.url,{waitUntil:'networkidle0'});
  await page.waitForFunction(()=>document.documentElement.dataset.uiFont==='builtin:ui-ibm-plex-sans-sc');
  assert.equal(await page.evaluate(()=>allFonts().length),5);
  assert.equal(requests.filter(u=>u.includes('/api/font-library')).length,0);
  checks.push('启动仅加载内置资源，1 UI / 5 Shell，无在线字体请求');
  await page.evaluate(()=>DengUIAppearance.open());
  assert.equal(await page.$$eval('#ui-font-list .ui-font-card',a=>a.length),1);
  await page.click('#switch-shell-fonts');assert.equal(await page.$$eval('#asset-list .asset-card',a=>a.length),5);
  await page.click('#switch-ui-fonts');await page.click('#online-ui-fonts');
  await page.waitForFunction(()=>document.querySelectorAll('.library-card').length===20);
  await page.waitForFunction(()=>[...document.querySelectorAll('.library-preview img')].some(x=>x.complete&&x.naturalWidth===800));
  await page.screenshot({path:join(stage,'screenshots/app-ui-library.png')});
  checks.push('界面 / Shell 设置切换、20 款界面目录及真实 PNG 预览');
  async function select(kind,id){
    await page.evaluate(k=>DengFontLibrary.open(k),kind);
    await page.$eval('.library-search',(e,id)=>{e.value=id;e.dispatchEvent(new Event('input',{bubbles:true}));},id.name);
    await page.waitForSelector(`[data-library-font="${id.id}"] .library-use`);
    await page.click(`[data-library-font="${id.id}"] .library-use`);
    await page.waitForSelector('#action-dialog[open]');await page.click('#action-confirm');
  }
  await select('ui-font',{id:'ui-marker',name:'漫黑'});
  await page.waitForFunction(()=>!appearance.uiFontId.startsWith('builtin:') && document.documentElement.dataset.uiFont===appearance.uiFontId,{timeout:30000});
  let config=await api('/api/config');const downloadedUI=config.appearance.uiFontId;
  assert(config.assets.some(a=>a.id===downloadedUI && a.libraryId==='ui-marker' && a.licenseText.includes('OPEN FONT LICENSE')));
  checks.push('在线界面字体：确认、校验、保存完整许可证、立即启用');
  await select('font',{id:'shell-iosevka',name:'Iosevka'});
  await page.waitForFunction(()=>managedAssets.some(a=>a.id===appearance.fontId&&a.libraryId==='shell-iosevka') && activeFontID===appearance.fontId,{timeout:30000});
  const metrics=await page.evaluate(()=>{const c=document.createElement('canvas').getContext('2d');c.font=`100px ${terminalFontFamily}`;return {latin:c.measureText('M').width,han:c.measureText('中').width,family:terminalFontFamily};});
  assert(Math.abs(metrics.han-metrics.latin*2)<.1,JSON.stringify(metrics));
  checks.push('Iosevka 窄字宽：中文回退精确适配两格');
  // The actual DOM renderer must retain two-cell Chinese and mixed ANSI columns.
  const grids=await page.evaluate(async()=>{
    document.querySelector('#font-library-dialog').close();const host=document.createElement('div');host.style.cssText='position:fixed;inset:30px;background:#142b3a;z-index:10000;padding:20px';document.body.append(host);
    const rows=[];
    for(const font of allFonts().filter(f=>f.kind==='builtin')){
      await loadFace(font);await loadFace(fontCatalog.fallback);const family=await alignedTerminalFontFamily(font);
      const box=document.createElement('div');box.style.cssText='width:900px;height:100px';host.append(box);
      const term=new Terminal({cols:60,rows:3,fontFamily:family,fontSize:16,allowProposedApi:true});term.open(box);const state={term,host:box};installTerminalFontMetrics(state);
      await new Promise(resolve=>term.write('NAME    STATE\r\n中文    \x1b[1;32m正常\x1b[0m OK\r\nABCD    running',resolve));
      const line=term.buffer.active.getLine(1);rows.push({id:font.id,han:line.getCell(0).getWidth(),state:line.getCell(8).getChars(),column:term.buffer.active.getLine(2).getCell(8).getChars()});term.dispose();box.remove();
    }
    host.remove();return rows;
  });
  for(const row of grids){assert.equal(row.han,2);assert.equal(row.state,'正');assert.equal(row.column,'r');}
  checks.push('五款内置字体在实际 xterm DOM 终端中的中文宽度、ANSI 粗体及列位置');
  await writeFile(join(stage,'slow'),'1');
  await select('font',{id:'shell-roboto-mono',name:'Roboto Mono'});
  await page.waitForSelector('[data-library-font="shell-roboto-mono"] .library-cancel:not([hidden])');
  await page.click('[data-library-font="shell-roboto-mono"] .library-cancel');
  await page.waitForFunction(()=>document.querySelector('[data-library-font="shell-roboto-mono"] .library-use')?.textContent==='下载并使用');
  assert(!(await api('/api/config')).assets.some(a=>a.libraryId==='shell-roboto-mono'));
  checks.push('取消未完成下载，无残留字体、原选择保留');
  await select('font',{id:'shell-inconsolata',name:'Inconsolata'});
  await page.evaluate(()=>chooseAppearance({fontId:'builtin:fira-code'}));
  await page.waitForFunction(()=>managedAssets.some(a=>a.libraryId==='shell-inconsolata'),{timeout:15000});
  assert.equal((await api('/api/config')).appearance.fontId,'builtin:fira-code');
  await select('font',{id:'shell-roboto-mono',name:'Roboto Mono'});
  await api('/api/appearance/patch',{method:'POST',body:JSON.stringify({fontId:'builtin:jetbrains-mono'})});
  await page.waitForFunction(()=>managedAssets.some(a=>a.libraryId==='shell-roboto-mono'),{timeout:15000});
  assert.equal((await api('/api/config')).appearance.fontId,'builtin:jetbrains-mono');
  await rm(join(stage,'slow'));
  checks.push('较早下载完成不会覆盖当前窗口或另一窗口后来选定的字体');
  await writeFile(join(stage,'offline'),'1');
  await page.reload({waitUntil:'networkidle0'});
  await page.waitForFunction(id=>document.documentElement.dataset.uiFont===id,{},downloadedUI);
  assert.equal(await page.evaluate(()=>appearance.fontId),'builtin:jetbrains-mono');
  checks.push('模拟官网不可达时，刷新后仍使用已下载字体');
  await api(`/api/assets/${downloadedUI}`,{method:'DELETE'});await page.evaluate(()=>loadProfiles());
  assert.equal(await page.evaluate(()=>document.documentElement.dataset.uiFont),'builtin:ui-ibm-plex-sans-sc');
  await rm(join(stage,'offline'));
  checks.push('删除当前在线界面字体后恢复 IBM 默认款式');
  await page.evaluate(()=>DengFontLibrary.open('font'));
  await page.$eval('.library-search',e=>{e.value='';e.dispatchEvent(new Event('input',{bubbles:true}));});
  await page.waitForFunction(()=>document.querySelectorAll('.library-card').length===30);
  await page.setViewport({width:800,height:720});await page.screenshot({path:join(stage,'screenshots/app-shell-library.png')});
  assert(await page.$eval('.font-library-dialog',e=>e.getBoundingClientRect().right<=innerWidth+1));
  assert.deepEqual(errors,[]);
  const website=await browser.newPage();const webRequests=[];website.on('request',r=>webRequests.push(r.url()));website.on('pageerror',e=>errors.push(e.message));
  await website.setViewport({width:1440,height:1000});await website.goto(`http://127.0.0.1:${site.address().port}/fonts/`,{waitUntil:'networkidle0'});
  await website.waitForFunction(()=>document.querySelectorAll('.card').length===20);
  assert(!webRequests.some(u=>/\.woff2|\.zip/.test(u)));
  await website.click('.card .primary');await website.waitForSelector('#download[open]');
  await website.evaluate(()=>{const original=URL.createObjectURL.bind(URL);URL.createObjectURL=blob=>{window.testFontZip=blob;return original(blob);};});
  await website.click('.file');await website.waitForFunction(()=>window.testFontZip instanceof Blob,{timeout:30000});
  const zipBytes=Buffer.from(await website.evaluate(async()=>{const data=new Uint8Array(await window.testFontZip.arrayBuffer());let text='';for(let i=0;i<data.length;i+=8192)text+=String.fromCharCode(...data.subarray(i,i+8192));return btoa(text);}),'base64');
  assert.equal(zipBytes.subarray(0,2).toString(),'PK');await writeFile(join(stage,'website-downloaded-font.zip'),zipBytes);
  await website.click('.close');await website.screenshot({path:join(stage,'screenshots/website-desktop.png'),fullPage:false});
  await website.click('[data-kind="font"]');assert.equal(await website.$$eval('.card',a=>a.length),30);
  await website.setViewport({width:390,height:844});await website.screenshot({path:join(stage,'screenshots/website-mobile.png'),fullPage:false});
  assert(await website.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
  checks.push('官网 20 / 30 字体、确认下载含许可证 ZIP、无字体预加载、严格 CSP、手机无横向溢出');
  assert.deepEqual(errors,[]);
  await writeFile(join(stage,'browser-results.json'),JSON.stringify({passed:true,checks,metrics,grids,errors},null,2)+'\n');
  console.log(checks.join('\n'));
}finally{await browser.close();await new Promise(resolve=>site.close(resolve));await rm(profile,{recursive:true,force:true});await rm(join(stage,'offline'),{force:true});await rm(join(stage,'slow'),{force:true});}
