// Isolated browser fixture: real selection, strength, large import, persistence and cleanup.
import fs from 'node:fs';
import assert from 'node:assert/strict';
import {fileURLToPath} from 'node:url';
const [stage,chrome,modulePath]=process.argv.slice(2);
const {default:puppeteer}=await import(modulePath);
const fixture=JSON.parse(fs.readFileSync(stage+'/browser-fixture.json'));
const origin=new URL(fixture.url).origin;
const setup=await fetch(origin+'/api/appearance/patch',{method:'POST',headers:{'Content-Type':'application/json','X-CloudShell-Token':fixture.token},body:JSON.stringify({onboardingCompleted:true,startupAnimation:false,uiScale:1,theme:'dark'})});
assert(setup.ok);
const browser=await puppeteer.launch({executablePath:chrome,headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const errors=[],checks=[];
try{
 const page=await browser.newPage();page.on('pageerror',e=>errors.push(e.message));
 await page.setViewport({width:1440,height:1000});
 await page.emulateMediaFeatures([{name:'prefers-reduced-motion',value:'reduce'}]);
 await page.goto(fixture.url,{waitUntil:'networkidle0'});
 await page.evaluate(async()=>{
  document.querySelectorAll('dialog[open]').forEach(d=>d.close());setDrawer(false);pollStats=pollNetwork=pollLatency=()=>{};
  const id='background-check';profiles.push({id,name:'背景显示检查',host:'fixture.invalid',user:'qa',port:22});
  const s={...makeSessionState({id,profileId:id,home:'/'}),localOnly:true};sessions.set(id,s);createTerminal(s);activate(id);s.term.options.cursorBlink=false;
  await writeTerminalAndWait(s,'🔗  连接主机...\r\n✅  连接主机成功！\r\n\r\n背景强度 39% · 保留已确认的深色主题\r\n');
 });
 const inspect=()=>page.evaluate(async()=>{
  await appearanceSave;
  const panel=document.querySelector('.terminal-panel'),s=getComputedStyle(panel,'::before'),shade=getComputedStyle(panel,'::after');
  const asset=allBackgrounds().find(a=>a.id===appearance.backgroundId);
  if(asset.id!=='builtin:none'){const image=new Image();image.src=await assetURL(asset);await image.decode();}
  return{id:appearance.backgroundId,cssVar:document.documentElement.style.getPropertyValue('--terminal-background'),hasImage:panel.classList.contains('has-background'),opacity:Number(s.opacity),display:s.display,image:s.backgroundImage,shadeDisplay:shade.display,base:getComputedStyle(panel).backgroundColor,body:getComputedStyle(document.body).backgroundColor};
 });
 const strength=async value=>{
  await page.$eval('#background-opacity',(el,value)=>{el.value=String(value);el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}));},value);
  await page.evaluate(()=>appearanceSave);
 };
 await page.click('#settings-button');await page.click('#manage-backgrounds');
 await page.waitForSelector('#appearance-dialog[open]');
 await page.click('[data-asset-id="builtin:mist"] .asset-choice');
 await page.waitForFunction(()=>appearance.backgroundId==='builtin:mist'&&activeBackgroundID==='builtin:mist');
 await strength(39);
 const clip=await page.$eval('.terminal-panel',el=>{const r=el.getBoundingClientRect();return{x:r.left+120,y:r.top+230,width:180,height:160};});
 for(const theme of ['dark','light']){
  await page.evaluate(theme=>CloudShellTheme.set(theme),theme);
  const rendered=[];
  for(const value of [0,39,100]){
   await strength(value);const state=await inspect();assert.equal(state.opacity,value/100);assert.notEqual(state.display,'none');assert.notEqual(state.image,'none');assert.notEqual(state.shadeDisplay,'none');
   rendered.push(await page.screenshot({clip}));
   
  }
  assert(!Buffer.from(rendered[0]).equals(Buffer.from(rendered[1])),theme+': image at 39% must change actual rendered pixels');
  assert(!Buffer.from(rendered[1]).equals(Buffer.from(rendered[2])),theme+': intensity slider must change actual rendered pixels');
  await strength(39);const state=await inspect();
  if(theme==='dark'){assert.equal(state.base,'rgb(15, 22, 29)');assert.equal(state.body,'rgb(15, 20, 25)');}
  await page.screenshot({path:stage+'/background-'+theme+'.png'});
  checks.push({theme,values:[0,39,100],pixelsChanged:true,decoded:true});
 }
 await page.evaluate(()=>CloudShellTheme.set('dark'));
 await page.click('[data-asset-id="builtin:none"] .asset-choice');
 await page.waitForFunction(()=>!document.querySelector('.terminal-panel').classList.contains('has-background'));
 assert.equal((await inspect()).display,'none');
 const solidPixels=await page.screenshot({clip});
 await page.screenshot({path:stage+'/background-none.png'});
 // Import through the actual file input, using a repository image in isolated storage.
 await (await page.$('#asset-picker')).uploadFile(fileURLToPath(new URL('../web/assets/backgrounds/moonlake.png',import.meta.url)));
 await page.waitForFunction(()=>!appearance.backgroundId.startsWith('builtin:')&&activeBackgroundID===appearance.backgroundId);
 await strength(39);const custom=await inspect();assert.notEqual(custom.display,'none');assert.equal(custom.opacity,.39);assert.notEqual(custom.image,'none');assert(custom.cssVar.includes('blob:'));
 assert(!Buffer.from(solidPixels).equals(Buffer.from(await page.screenshot({clip}))), 'imported image must change actual pixels');
 await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
  await page.screenshot({path:stage+'/background-import.png'});
  
 await page.reload({waitUntil:'networkidle0'});
 await page.waitForFunction(id=>appearance.backgroundId===id&&activeBackgroundID===id,{},custom.id);
 const saved=await inspect();await page.screenshot({path:stage+'/background-reload.png'});assert.equal(saved.id,custom.id);assert.equal(saved.opacity,.39);assert.notEqual(saved.display,'none');assert.notEqual(saved.image,'none');
 await page.evaluate(()=>CloudShellTheme.set('light'));assert.notEqual((await inspect()).display,'none');
 await page.evaluate(()=>CloudShellTheme.set('dark'));assert.notEqual((await inspect()).display,'none');
 // Deleting an imported image must release its local Blob URL.
 await page.evaluate(()=>openAppearance('background'));
 await page.click('[data-asset-id="'+custom.id+'"] .asset-actions .danger-button');
 await page.waitForSelector('#action-dialog[open]');await page.click('#action-confirm');
 await page.waitForFunction(id=>!managedAssets.some(a=>a.id===id)&&!assetData.has(id),{},custom.id);
 const revoked=await page.evaluate(async url=>{try{await fetch(url);return false}catch{return true}},saved.cssVar.slice(5,-2));assert(revoked);
 assert.deepEqual(errors,[]);
 fs.writeFileSync(stage+'/background-result.json',JSON.stringify({passed:true,checks,solidMode:true,customImport:true,reloadPreserved:true,errors},null,2));
 console.log('PASS: visible built-in/imported backgrounds; actual pixels change at 0/39/100%; dark/light switches; solid mode; saved selection and intensity survive reload; accepted palette unchanged');
}finally{await browser.close();}
