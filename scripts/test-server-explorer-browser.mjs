import fs from 'node:fs';
import assert from 'node:assert/strict';
const [stage,chrome,modulePath]=process.argv.slice(2);
const {default:puppeteer}=await import(modulePath),fixture=JSON.parse(fs.readFileSync(stage+'/browser-fixture.json'));
const setup=await fetch(new URL('/api/appearance/patch',fixture.url),{method:'POST',headers:{'Content-Type':'application/json','X-CloudShell-Token':fixture.token},body:JSON.stringify({startupAnimation:false,onboardingCompleted:true,uiScale:1})});assert(setup.ok);
const browser=await puppeteer.launch({executablePath:chrome,headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const page=await browser.newPage(),errors=[],checks=[];
page.on('pageerror',e=>errors.push(e.message));
try {
 await page.emulateMediaFeatures([{name:'prefers-reduced-motion',value:'reduce'}]);
 await page.setViewport({width:1366,height:1000});await page.goto(fixture.url,{waitUntil:'networkidle0'});
 const ids=await page.evaluate(async()=>{
  await chooseAppearance({startupAnimation:false,onboardingCompleted:true,uiScale:1});
  document.querySelectorAll('dialog[open]').forEach(d=>d.close());
  const root=await post('/api/group-nodes',{name:'分组点击验证-'+Date.now()});
  const child=await post('/api/group-nodes',{name:'日常服务器',parentId:root.id});
  const leaf=await post('/api/group-nodes',{name:'备用',parentId:child.id});
  await loadProfiles();serverManager.tab='servers';serverManager.collapsed.add(root.id);persistGroupCollapse();chooseServerFolder('');setDrawer(true);
  return {root:root.id,child:child.id,leaf:leaf.id};
 });
 const row=id=>`#server-folder-list [data-group-id="${id}"]`;
 const button=id=>row(id)+' [data-group-action=select]';
 const state=()=>page.evaluate(({root,child,leaf})=>({selected:serverManager.selectedGroup,collapsed:[...serverManager.collapsed],visible:[...document.querySelectorAll('#server-folder-list [data-group-id]')].map(e=>e.dataset.groupId),focused:document.activeElement.closest('[data-group-id]')?.dataset.groupId}),ids);
 for(const theme of ['light','dark']) {
  await page.evaluate(t=>{CloudShellTheme.set(t);chooseServerFolder('')},theme);
  await new Promise(r=>setTimeout(r,1000));
  await page.click(button(ids.root));let s=await state();assert.equal(s.selected,ids.root);assert(s.visible.includes(ids.child));
  await page.click(button(ids.root));s=await state();assert(s.collapsed.includes(ids.root)&&!s.visible.includes(ids.child));assert.equal(s.focused,ids.root);
  await page.keyboard.press('Enter');s=await state();assert(!s.collapsed.includes(ids.root)&&s.visible.includes(ids.child));
  await page.keyboard.press('Space');s=await state();assert(s.collapsed.includes(ids.root)&&!s.visible.includes(ids.child));
  await page.evaluate(id=>chooseServerFolder(id),ids.leaf);s=await state();assert.equal(s.selected,ids.leaf);assert(s.visible.includes(ids.leaf)&&!s.collapsed.includes(ids.root)&&!s.collapsed.includes(ids.child));
  await page.click(button(ids.child));await page.click(button(ids.child));s=await state();assert(s.collapsed.includes(ids.child));
  await page.click(row(ids.child)+' [data-group-action=toggle]');s=await state();assert(!s.collapsed.includes(ids.child));
  await page.click(button(ids.child));assert((await state()).collapsed.includes(ids.child));
  await page.type('#connection-search','备用');await page.waitForFunction(id=>!!document.querySelector(`#server-folder-list [data-group-id="${id}"]`),{},ids.leaf);
  await page.click(button(ids.child));s=await state();assert(!s.collapsed.includes(ids.child));assert.equal(await page.$eval('#connection-search',e=>e.value),'');
  await page.click(button(ids.leaf));await page.click(button(ids.leaf));assert(!(await state()).collapsed.includes(ids.leaf));
  await page.click(button(ids.root));await page.click(button(ids.root));assert((await state()).collapsed.includes(ids.root));
  await page.evaluate(()=>loadProfiles());assert((await state()).collapsed.includes(ids.root),'backend refresh preserves collapse');
  await page.evaluate(id=>chooseServerFolder(id),ids.child);
  await page.screenshot({path:stage+`/server-explorer-${theme}.png`});
  checks.push(`${theme}: repeat click and Enter/Space toggle; focus retained; caret, leaf, search, ancestor navigation and refresh verified`);
 }
 await page.evaluate(()=>chooseServerFolder(''));
 assert.deepEqual(errors,[]);fs.writeFileSync(stage+'/server-explorer-browser.json',JSON.stringify({passed:true,checks,errors},null,2));console.log('PASS',checks);
} catch(e) {await page.screenshot({path:stage+'/server-explorer-failure.png'});throw e;}
finally {await browser.close()}
