import fs from 'node:fs';
import assert from 'node:assert/strict';
const [stage, chrome, modulePath] = process.argv.slice(2);
const {default: puppeteer} = await import(modulePath);
const fixture = JSON.parse(fs.readFileSync(stage + '/browser-fixture.json'));
assert.equal(new URL(fixture.url).hostname, '127.0.0.1');
const call = async (path, body) => {
  const r = await fetch(new URL(path, fixture.url), {method:'POST', headers:{'Content-Type':'application/json','X-CloudShell-Token':fixture.token}, body:JSON.stringify(body)});
  assert(r.ok, await r.text());
};
await call('/api/appearance/patch', {onboardingCompleted:true,startupAnimation:false,uiScale:1});
const browser = await puppeteer.launch({executablePath:chrome,headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const page = await browser.newPage(), errors = [], checks = [];
page.on('pageerror', e => errors.push(e.message));
try {
  await page.emulateMediaFeatures([{name:'prefers-reduced-motion',value:'reduce'}]);
  await page.setViewport({width:1366,height:1000,deviceScaleFactor:1.25});
  await page.goto(fixture.url, {waitUntil:'networkidle0'});
  await page.evaluate(id => { document.querySelectorAll('dialog[open]').forEach(d=>d.close());setDrawer(false);showConnectionForm(profiles.find(p=>p.id===id)); }, fixture.sessions[0].profileId);
  const field = '#connection-notes';
  const reset = value => page.$eval(field, (e,v) => {window.DengProfileNotes.init(e,v);e.focus();e.setSelectionRange(e.value.length,e.value.length);}, value);
  const value = () => page.$eval(field, e=>e.value);
  await reset('第一行\n第二行\n第三行');
  await page.keyboard.press('Enter');
  assert.equal(await value(), '第一行\n第二行\n第三行');
  assert(await page.$eval('#connection-notes-hint',e=>e.textContent.includes('超出限制')));
  await page.keyboard.type('!'); assert.equal(await value(), '第一行\n第二行\n第三行!');
  await reset('字'.repeat(19)); await page.keyboard.sendCharacter('😀');
  assert.equal(Array.from(await value()).length, 20);
  await page.keyboard.sendCharacter('多'); assert.equal(Array.from(await value()).length, 20);
  await page.keyboard.press('Backspace'); await page.keyboard.sendCharacter('替');
  assert.equal(await value(), '字'.repeat(19)+'替');
  await reset('a'.repeat(40)); await page.keyboard.type('x');
  assert.equal(await value(), 'a'.repeat(40));
  await page.keyboard.press('Enter'); await page.keyboard.sendCharacter('字'.repeat(20));
  await page.keyboard.press('Enter'); await page.keyboard.sendCharacter('字'.repeat(10)+'a'.repeat(20));
  await page.keyboard.sendCharacter('多');
  assert.equal(await value(), 'a'.repeat(40)+'\n'+'字'.repeat(20)+'\n'+'字'.repeat(10)+'a'.repeat(20));
  // A rejected paste keeps the prior value and selection, with an explicit hint.
  await reset('保留原文'); await page.keyboard.sendCharacter('一\n二\n三\n四');
  assert.equal(await value(), '保留原文');
  await page.$eval(field,e=>e.select()); await page.keyboard.sendCharacter('到期：2027-02-03\n用途：备用节点\n续费：https://example.test');
  const note = await value(); assert.equal(note.split('\n').length,3);
  await page.click('#save-connection');
  await page.waitForFunction(()=>!document.querySelector('#connection-dialog').open);
  await page.waitForFunction((id,n)=>profiles.find(p=>p.id===id)?.notes===n,{},fixture.sessions[0].profileId,note);
  checks.push('Real save; 3-line/40-width-unit-per-line limits; rejected paste preserves text; selection replacement and deletion');
  // IME composition must remain untouched until composition ends.
  const ime = await page.evaluate(id=>{
    showConnectionForm(profiles.find(p=>p.id===id)); const e=$('#connection-notes');
    DengProfileNotes.init(e,'字'.repeat(19)); e.setSelectionRange(e.value.length,e.value.length);
    e.dispatchEvent(new CompositionEvent('compositionstart'));
    e.value+='中文'; e.dispatchEvent(new InputEvent('input',{isComposing:true}));
    const during=Array.from(e.value).length;
    e.dispatchEvent(new CompositionEvent('compositionend'));
    const rejected=Array.from(e.value).length;
    e.dispatchEvent(new CompositionEvent('compositionstart'));e.value+='中';e.dispatchEvent(new InputEvent('input',{isComposing:true}));e.dispatchEvent(new CompositionEvent('compositionend'));
    return {during,rejected,accepted:Array.from(e.value).length};
  }, fixture.sessions[0].profileId);
  assert.deepEqual(ime,{during:21,rejected:19,accepted:20});
  // Existing long notes remain visible and can be reduced before saving.
  const legacy = await page.$eval(field,e=>{
    DengProfileNotes.init(e,'历史备注\n'.repeat(40));const original=e.value;
    const unchanged=e.checkValidity();e.value=e.value.slice(0,-2);e.dispatchEvent(new Event('input'));
    const invalidEdit=!e.checkValidity();const kept=e.value.length===original.length-2;
    e.value='已缩短';e.dispatchEvent(new Event('input'));
    return {unchanged,invalidEdit,kept,shortened:e.checkValidity()};
  });
  assert.deepEqual(legacy,{unchanged:true,invalidEdit:true,kept:true,shortened:true});
  await page.click('#cancel-connection');
  checks.push('Composition waits until commit; old long notes preserved and editable without silent truncation');
  // Exercise the actual quick-save opener, including reopening for another server.
  await page.evaluate(()=>{
    temporaryProfiles.set('temporary-notes-qa',{id:'temporary-notes-qa',temporary:true,name:'快速连接测试',host:'192.0.2.30',user:'root',port:22,auth:'password'});
    sessions.set('temporary-notes-qa',{id:'temporary-notes-qa',profileId:'temporary-notes-qa',connected:true});activeID='temporary-notes-qa';
    document.querySelector('#save-quick-connection').onclick();
  });
  const quick = '#quick-save-dialog textarea[name=notes]';
  await page.focus(quick); await page.keyboard.sendCharacter('一\n二\n三');await page.keyboard.press('Enter');
  assert.equal(await page.$eval(quick,e=>e.value),'一\n二\n三');
  await page.evaluate(()=>{$('#quick-save-dialog').close();$('#save-quick-connection').onclick();});
  assert.equal(await page.$eval(quick,e=>e.value),'');
  await page.evaluate(()=>{ $('#quick-save-dialog').close(); sessions.delete('temporary-notes-qa');temporaryProfiles.delete('temporary-notes-qa');activeID='';setDrawer(true); });
  await page.waitForNetworkIdle({idleTime:200});
  checks.push('Quick-save uses identical limits; reopening clears prior server draft');
  const visual = await page.evaluate(()=>{
    profiles[0].notes='测试'.repeat(80);profiles[1].notes='';renderConnections();return profiles[0].id;
  });
  for (const theme of ['light','dark']) {
    await page.evaluate(t=>CloudShellTheme.set(t),theme);
    await page.waitForFunction(t=>document.documentElement.dataset.theme===t,{},theme);
    const metrics=await page.$$eval('.server-profile-row',es=>es.map(e=>{
      const b=e.querySelector('.server-card-notes'),t=e.querySelector('.server-card-notes-text'),label=e.querySelector('.server-card-notes-label');
      return {hasNote:!!b,label:label?.textContent,titleAbove:label&&t&&label.getBoundingClientRect().bottom<=t.getBoundingClientRect().top,lines:t?.textContent.split('\n'),overflow:e.scrollWidth>e.clientWidth+1,height:t?.getBoundingClientRect().height,border:b?getComputedStyle(b).borderTopWidth:null,clamp:t?getComputedStyle(t).webkitLineClamp:null};
    }));
    assert.equal(metrics.filter(m=>m.hasNote).length,1);
    assert(metrics.every(m=>!m.overflow));assert(metrics.filter(m=>m.hasNote).every(m=>m.height<=51.5&&m.border==='1px'&&m.clamp==='3'&&m.label==='备注'&&m.titleAbove&&m.lines.length===3&&m.lines.every(l=>Array.from(l).reduce((n,c)=>n+(c.codePointAt(0)<=127?1:2),0)<=40)));
    await page.screenshot({path:stage+`/notes-box-${theme}.png`});
  }
  await page.setViewport({width:750,height:660,deviceScaleFactor:1});
  await page.evaluate(id=>{profiles.find(p=>p.id===id).notes='长备注'.repeat(300)+'\n<img src=x onerror="window.notesInjected=1">';renderConnections();},visual);
  const narrow=await page.$eval('.server-card-notes-text',e=>({height:e.getBoundingClientRect().height,overflow:e.scrollWidth>e.clientWidth+1,images:e.querySelectorAll('img').length}));
  assert(narrow.height<=51.5&&!narrow.overflow&&narrow.images===0,JSON.stringify(narrow));
  assert.equal(await page.evaluate(()=>window.notesInjected),undefined);
  checks.push('Light/dark framed notes; separate title; long legacy preview wraps at 40 width units; three visual lines even in narrow windows; empty notes add no box; safe text');
  assert.deepEqual(errors,[]);
  fs.writeFileSync(stage+'/browser-results.json',JSON.stringify({passed:true,checks,errors},null,2));
  console.log('PASS',checks);
} catch(e) { await page.screenshot({path:stage+'/browser-failure.png'});throw e; }
finally { await browser.close(); }
