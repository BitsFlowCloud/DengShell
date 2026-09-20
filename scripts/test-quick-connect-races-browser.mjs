import fs from 'node:fs';
import assert from 'node:assert/strict';
const [stage,chrome,modulePath]=process.argv.slice(2);
const {default:puppeteer}=await import(modulePath),fixture=JSON.parse(fs.readFileSync(stage+'/browser-fixture.json'));
await fetch(new URL(fixture.url).origin+'/api/appearance/patch',{method:'POST',headers:{'Content-Type':'application/json','X-CloudShell-Token':fixture.token},body:JSON.stringify({startupAnimation:false,onboardingCompleted:true,uiScale:1})});
const browser=await puppeteer.launch({executablePath:chrome,headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
try {
 const page=await browser.newPage();await page.setViewport({width:1440,height:1000});await page.goto(fixture.url,{waitUntil:'networkidle0'});
 await page.evaluate(()=>{
  document.querySelectorAll('dialog[open]').forEach(d=>d.close());pollStats=()=>{};pollNetwork=()=>{};loadProfiles=async()=>{};
  for(const id of ['save-a','save-b']){
   const p={id,name:id,host:'example.invalid',user:'qa',port:22,temporary:true,auth:'password'};temporaryProfiles.set(id,p);
   sessions.set(id,{...makeSessionState({id,profileId:id,home:'/'}),localOnly:true,term:{options:{},focus(){},refresh(){},dispose(){}},host:document.createElement('div')});
  }
  const original=api;api=(url,body)=>url.endsWith('/save')?new Promise((resolve,reject)=>{window.qaSave={resolve,reject,url,body}}):original(url,body);
 });
 for(const outcome of ['success','failure']){
  await page.evaluate(()=>{activate('save-a');document.querySelector('#save-quick-connection').click();document.querySelector('#quick-save-dialog form').requestSubmit()});
  await page.waitForFunction(()=>!!window.qaSave);
  await page.evaluate(()=>{document.querySelector('#quick-save-dialog').close();activate('save-b');document.querySelector('#save-quick-connection').click()});
  await page.evaluate(outcome=>{if(outcome==='success')qaSave.resolve({...temporaryProfiles.get('save-a'),temporary:false});else qaSave.reject(Error('old request failed'));},outcome);
  await page.waitForFunction(()=>!document.querySelector('#quick-save-dialog [type=submit]').disabled);
  assert(await page.$eval('#quick-save-dialog',e=>e.open),'late result closed another connection dialog');
  assert.equal(await page.$eval('#quick-save-dialog [name=name]',e=>e.value),'save-b');
  assert.equal(await page.$eval('#quick-save-error',e=>e.textContent),'');
  await page.evaluate(()=>{document.querySelector('#quick-save-dialog').close();temporaryProfiles.set('save-a',{id:'save-a',name:'save-a',host:'example.invalid',user:'qa',port:22,temporary:true,auth:'password'});window.qaSave=null});
 }
 fs.writeFileSync(stage+'/quick-connect-races.json',JSON.stringify({passed:true,lateSaveSuccessIsolated:true,lateSaveFailureIsolated:true}));console.log('PASS quick connection save success/failure cannot close or overwrite another dialog');
}finally{await browser.close()}
