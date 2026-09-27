import fs from 'node:fs';
import assert from 'node:assert/strict';
const [stage,modulePath] = process.argv.slice(2);
const {default:puppeteer}=await import(modulePath);
const log=fs.readFileSync(stage+'/browser-backend.log','utf8');
const url=log.match(/http:\/\/127\.0\.0\.1:\d+\/#token=[a-zA-Z0-9]+/)?.[0];assert(url);
const origin=new URL(url).origin,token=new URLSearchParams(new URL(url).hash.slice(1)).get('token');
await fetch(origin+'/api/appearance/patch',{method:'POST',headers:{'Content-Type':'application/json','X-CloudShell-Token':token},body:JSON.stringify({onboardingCompleted:true,startupAnimation:false,uiScale:1})});
const browser=await puppeteer.launch({executablePath:'/usr/bin/google-chrome',headless:true,args:['--no-sandbox']});
const errors=[];
try {
 const page=await browser.newPage();await page.setViewport({width:1440,height:1000});page.on('pageerror',e=>errors.push(e.message));
 await page.setRequestInterception(true);page.on('request',r=>{const u=new URL(r.url());if(['http:','https:'].includes(u.protocol)&&u.origin!==new URL(url).origin)r.abort();else r.continue()});
 await page.goto(url,{waitUntil:'networkidle0'});
 await page.evaluate(async()=>{
  document.querySelectorAll('dialog[open]').forEach(d=>d.close());setDrawer(false);
  qa={calls:[],rdp:[],seq:0};window.runtime={EventsOn:()=>()=>{}};
  const real=api;api=(path,options)=>path==='/api/rdp'?Promise.resolve({available:true,sessions:structuredClone(qa.rdp)}):real(path,options);
  window.go={main:{Desktop:{Request:async(method,path,body)=>{const r=await fetch(CLOUDSHELL.base+path,{method,headers:{'Content-Type':'application/json','X-CloudShell-Token':CLOUDSHELL.token},body:method==='GET'?undefined:body});const data=await r.text();if(!r.ok)throw new Error('DENGSHELL_ERROR:'+data);return data},
   RDPConnect:async(profileId,secret)=>{const p=connectionProfile(profileId);const item={id:'rdp-qa-'+(++qa.seq),profileId,name:p.name,status:'connecting',message:'🔗  正在连接远程桌面…',createdAt:new Date().toISOString()};qa.lastSecret=secret;qa.rdp.push(item);return structuredClone(item)},
   OpenTerminal:async()=>{},SendTerminal:async()=>{},CloseTerminal:async()=>{},RDPViewport:async(...args)=>{qa.calls.push(args);if(qa.calls.length>1000)qa.calls.shift()},RDPResolution:async(...args)=>{qa.resolution=args},RDPSendSecureAttention:async()=>{}
  }}};
  await DengRDP.refresh();
  showConnectionForm();const form=document.querySelector('#connection-form');form.elements.protocol.value='rdp';form.elements.protocol.dispatchEvent(new Event('change'));
 });
 assert.equal(await page.$eval('[name=port]',e=>e.value),'3389');
 assert.equal(await page.$eval('#connection-auth-field',e=>e.hidden),true);
 assert.equal(await page.$eval('#rdp-domain-field',e=>e.hidden),false);
 await page.evaluate(()=>{const f=document.querySelector('#connection-form');for(const [key,val]of Object.entries({name:'RDP 测试机器',host:'192.0.2.20',user:'Administrator',domain:'EXAMPLE',secret:'QA-not-persisted'}))f.elements[key].value=val;f.requestSubmit()});
 await page.waitForFunction(()=>DengRDP.active()==='rdp-qa-1');
 assert.equal(await page.evaluate(()=>qa.lastSecret),'QA-not-persisted');
 assert.equal(await page.evaluate(()=>!!connectionProfile(qa.rdp[0].profileId).hasSecret),false);
 assert.equal(await page.$eval('#workspace',e=>getComputedStyle(e).display),'none');
 assert.equal(await page.$eval('.rdp-tab button',e=>e.getAttribute('aria-selected')),'true');
 await page.evaluate(async()=>{qa.rdp[0].status='connected';qa.rdp[0].message='✅  远程桌面已连接';qa.rdp[0].remoteWidth=2000;qa.rdp[0].remoteHeight=1250;await DengRDP.refresh()});
 await page.waitForFunction(()=>qa.calls.at(-1)?.at(-1)===true);
 assert.equal(await page.$eval('#rdp-resolution',e=>e.value),'0x0');
 assert.match(await page.$eval('#rdp-active-status',e=>e.textContent),/2000 × 1250/);
 await page.select('#rdp-resolution','1024x768');await page.waitForFunction(()=>qa.resolution?.[1]===1024);
 assert.deepEqual(await page.evaluate(()=>qa.resolution),['rdp-qa-1',1024,768]);
 const r=await page.$eval('#rdp-viewport',e=>{const r=e.getBoundingClientRect();return {left:r.left,top:r.top,width:r.width,height:r.height}});assert(r.width>1000&&r.height>500);
 await page.evaluate(()=>showConnectionForm());
 await page.waitForFunction(()=>qa.calls.at(-1)?.at(-1)===false);
 await page.evaluate(()=>document.querySelector('#connection-dialog').close());
 await page.waitForFunction(()=>qa.calls.at(-1)?.at(-1)===true);
 await page.evaluate(()=>setDrawer(true));await page.waitForFunction(()=>qa.calls.at(-1)?.at(-1)===false);
 await page.evaluate(()=>setDrawer(false));await page.waitForFunction(()=>qa.calls.at(-1)?.at(-1)===true);
 await page.evaluate(async()=>{credentials.set(qa.rdp[0].profileId,'second');await connect(qa.rdp[0].profileId)});
 assert.equal(await page.$$eval('.rdp-tab',e=>e.length),2);
 assert.equal(await page.$eval('#rdp-resolution',e=>e.value),'0x0');
 await page.evaluate(async()=>{qa.rdp[1].status='connected';await DengRDP.refresh()});await page.waitForFunction(()=>qa.calls.at(-1)?.[0]==='rdp-qa-2'&&qa.calls.at(-1)?.at(-1)===true);
 await page.evaluate(()=>document.querySelector('.rdp-tab button').click());await page.waitForFunction(()=>qa.calls.at(-1)?.[0]==='rdp-qa-1');
 await page.evaluate(()=>setSettingsMenu(true));await page.waitForFunction(()=>qa.calls.at(-1)?.at(-1)===false);
 await page.evaluate(()=>setSettingsMenu(false));await page.waitForFunction(()=>qa.calls.at(-1)?.at(-1)===true);
 assert.equal(await page.$eval('#rdp-resolution',e=>e.value),'1024x768');
 for(const theme of ['dark','light']){await page.evaluate(t=>CloudShellTheme.set(t),theme);await page.waitForFunction(t=>document.documentElement.dataset.theme===t,{},theme);await new Promise(r=>setTimeout(r,950));await page.screenshot({path:stage+'/rdp-'+theme+'.png'});}
 await page.setViewport({width:1000,height:700});await page.waitForFunction(()=>qa.calls.at(-1)?.[5]===1000);
 await page.evaluate(()=>{const state=makeSessionState({id:'ssh-qa',profileId:qa.rdp[0].profileId,home:'/'});state.connected=false;state.ready=false;state.tabOrder=0;sessions.set(state.id,state);createTerminal(state);activate(state.id)});
 await page.waitForFunction(()=>qa.calls.at(-1)?.at(-1)===false);
 assert.equal(await page.$eval('#rdp-panel',e=>e.hidden),true);
 assert.notEqual(await page.$eval('#workspace',e=>getComputedStyle(e).display),'none');
 assert.deepEqual(errors,[]);
 fs.writeFileSync(stage+'/rdp-browser-results.json',JSON.stringify({passed:true,checks:['RDP profile defaults and secret handling','native connection route','same-host independent tabs','active surface bounds and resizing','modal and server-manager occlusion','SSH/RDP switching','dark/light rendering'],errors},null,2));
 console.log('RDP browser integration passed');
}finally{await browser.close()}
