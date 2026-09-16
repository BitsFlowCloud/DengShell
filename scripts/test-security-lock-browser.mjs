import fs from 'node:fs';
import crypto from 'node:crypto';
import assert from 'node:assert/strict';
import {execFileSync} from 'node:child_process';
const [stage,chrome,modulePath,qrPython]=process.argv.slice(2);
const {default:puppeteer}=await import(modulePath),fixture=JSON.parse(fs.readFileSync(stage+'/browser-fixture.json'));
const browser=await puppeteer.launch({executablePath:chrome,headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const errors=[],checks=[];
const endpoint=new URL(fixture.url).origin;
async function api(path,body){const r=await fetch(endpoint+path,{method:body===undefined?'GET':'POST',headers:{'Content-Type':'application/json','X-CloudShell-Token':fixture.token},body:body===undefined?undefined:JSON.stringify(body)});return {status:r.status,data:await r.json()};}
function base32(text){const alphabet='ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';let bits='';for(const c of text)bits+=alphabet.indexOf(c).toString(2).padStart(5,'0');return Buffer.from((bits.match(/.{8}/g)||[]).map(b=>parseInt(b,2)));}
function otp(key,offset=0){const count=Buffer.alloc(8);count.writeBigUInt64BE(BigInt(Math.floor(Date.now()/30000)+offset));const h=crypto.createHmac('sha1',base32(key)).update(count).digest();return String((h.readUInt32BE(h.at(-1)&15)&0x7fffffff)%1000000).padStart(6,'0');}
const password='4826';let first,second;
async function load(){const p=await browser.newPage();p.on('pageerror',e=>{errors.push(e.message);console.error(e.stack)});await p.setViewport({width:1440,height:1000});await p.goto(fixture.url,{waitUntil:'networkidle0'});await p.waitForFunction(()=>window.DengSecurityLock&&document.querySelector('#security-lock-screen'));return p;}
async function settings(p){await p.bringToFront();await p.click('#settings-button');await p.click('#manage-security-lock');await p.waitForSelector('#security-lock-settings[open]');}
async function authSettings(p,value=password,method='password'){await p.select('#security-auth-method',method);await p.type('#security-auth-value',value);await p.click('#security-auth-button');await p.waitForFunction(()=>!document.querySelector('#security-enabled').disabled);}
async function lock(p){await p.bringToFront();await p.click('#security-lock-button');await p.waitForFunction(()=>document.querySelector('#security-lock-screen').open&&document.documentElement.dataset.securityLocked==='true');}
async function unlock(p,value=password,method='password'){await p.bringToFront();if(await p.$eval('#security-unlock-form',e=>e.hidden))await p.click('#security-unlock-open');if(await p.$eval('#security-method-label',e=>!e.hidden))await p.select('#security-unlock-method',method);await p.type('#security-unlock-value',value);await p.click('#security-unlock-submit');await p.waitForFunction(()=>!document.querySelector('#security-lock-screen').open);}
try{
 const initial=(await api('/api/security-lock/status')).data;if(initial.enabled){assert(initial.passwordConfigured,'Use a fresh isolated fixture after TOTP-only tests');await api('/api/security-lock/unlock',{method:'password',value:password});const grant=(await api('/api/security-lock/authorize',{method:'password',value:password})).data.grant;assert(grant);assert.equal((await api('/api/security-lock/settings',{grant,enabled:false})).status,200);}
 await api('/api/appearance/patch',{startupAnimation:false,onboardingCompleted:true,uiScale:1});
 first=await load();await first.evaluate(()=>setDrawer(false));
 assert(await first.$eval('#security-lock-button',e=>e.disabled));
 const gap=await first.evaluate(()=>document.querySelector('#security-lock-button').getBoundingClientRect().left-document.querySelector('#theme-toggle').getBoundingClientRect().right);assert(Math.abs(gap-14)<.5,JSON.stringify({gap}));
 await settings(first);await first.waitForFunction(()=>!document.querySelector('#security-enabled').disabled);
 await first.click('#security-enabled');await first.type('#security-new-password','123');await first.type('#security-repeat-password','123');await first.click('#security-save');await first.waitForFunction(()=>document.querySelector('#security-settings-error').textContent.includes('至少 4'));
 for(const id of ['security-new-password','security-repeat-password'])await first.$eval('#'+id,(e,pw)=>e.value=pw,password);
 await first.type('#security-password-hint','隔离测试提醒');await first.click('#security-save');await first.waitForFunction(()=>!document.querySelector('#security-lock-settings').open);
 assert.equal((await api('/api/security-lock/status')).data.enabled,true);
 second=await load();await second.evaluate(()=>setDrawer(false));
 // Unsaved editor-like dialog is left open, then covered and restored intact.
 await first.evaluate(()=>{const d=document.createElement('dialog');d.id='qa-unsaved';d.innerHTML='<textarea>未保存的草稿</textarea>';document.body.append(d);d.showModal();window.qaShortcuts=0;document.addEventListener('keydown',e=>{if(e.key==='F8')window.qaShortcuts++});});
 await api('/api/security-lock/lock',{});await first.waitForSelector('#security-lock-screen[open]');await second.waitForSelector('#security-lock-screen[open]');
 assert.equal((await api('/api/config')).status,423);
 assert.equal((await api('/api/security-lock/activity',{})).data.locked,true);
 await first.bringToFront();await first.keyboard.press('Escape');assert(await first.$eval('#security-lock-screen',e=>e.open));
 await first.keyboard.press('F8');assert.equal(await first.evaluate(()=>qaShortcuts),0);
 assert(await first.$eval('#qa-unsaved',e=>e.inert&&getComputedStyle(e).filter.includes('blur(24px)')));
 await first.evaluate(()=>{const d=document.createElement('dialog');d.id='qa-late';d.textContent='late dialog';document.body.append(d);d.showModal();});assert.equal(await first.$eval('#qa-late',e=>e.open),false);
 await first.screenshot({path:stage+'/locked-light.png'});
 await first.click('#security-unlock-open');assert((await first.$eval('#security-unlock-hint',e=>e.textContent)).includes('隔离测试提醒'));
 await first.type('#security-unlock-value','wrong');await first.click('#security-unlock-submit');await first.waitForFunction(()=>document.querySelector('#security-unlock-error').textContent.includes('不正确'));
 assert(await first.$eval('#security-lock-screen',e=>e.open));await unlock(first);
 await second.waitForFunction(()=>!document.querySelector('#security-lock-screen').open,{polling:100});
 assert.equal(await first.$eval('#qa-unsaved textarea',e=>e.value),'未保存的草稿');assert(await first.$eval('#qa-late',e=>e.open));
 await first.evaluate(()=>{document.querySelector('#qa-unsaved').close();document.querySelector('#qa-late').close();document.querySelector('#qa-unsaved').remove();document.querySelector('#qa-late').remove()});
 checks.push('Password minimum/hint, wrong password, backend gate, multi-window lock/unlock, modal/shortcut isolation and draft preservation');
 await settings(first);assert(await first.$eval('#security-enabled',e=>e.disabled));await authSettings(first);await first.select('#security-lock-mode','idle');await first.$eval('#security-idle-seconds',e=>e.value='3');await first.click('#security-save');await first.waitForFunction(()=>!document.querySelector('#security-lock-settings').open);
 for(let i=0;i<4;i++){await first.mouse.move(600+i*5,400);await new Promise(r=>setTimeout(r,900));assert.equal((await api('/api/security-lock/status')).data.locked,false);}
 await new Promise(r=>setTimeout(r,3700));assert.equal((await api('/api/security-lock/status')).data.locked,true);await first.waitForSelector('#security-lock-screen[open]');await unlock(first);
 await settings(first);await authSettings(first);await first.select('#security-lock-mode','manual');await first.click('#security-use-totp');await first.click('#security-enroll-start');await first.waitForFunction(()=>document.querySelector('#security-enroll-qr').src.startsWith('data:image/png'));
 const key=await first.$eval('#security-enroll-secret',e=>e.value),qr=await first.$eval('#security-enroll-qr',e=>e.src);
 await second.bringToFront();await first.bringToFront();await first.waitForFunction(()=>!DengSecurityLock.isLocked());assert(await first.$eval('#security-lock-settings',e=>e.open));assert.equal(await first.$eval('#security-enroll-secret',e=>e.value),key);

 fs.writeFileSync(stage+'/binding-qr.png',Buffer.from(qr.split(',')[1],'base64'));
 const decoded=execFileSync(qrPython,['-c','import sys,zxingcpp;from PIL import Image;print(zxingcpp.read_barcode(Image.open(sys.argv[1])).text)',stage+'/binding-qr.png'],{encoding:'utf8'}).trim();
 const uri=new URL(decoded);assert.equal(uri.protocol,'otpauth:');assert.equal(uri.hostname,'totp');assert.equal(uri.searchParams.get('secret'),key);assert.equal(uri.searchParams.get('issuer'),'DengShell');
 await first.type('#security-enroll-code',otp(key,-1));await first.click('#security-save');await first.waitForFunction(()=>!document.querySelector('#security-lock-settings').open);
 await lock(first);await unlock(first,otp(key),'totp');
 await settings(first);await authSettings(first);await first.click('#security-use-password');await first.click('#security-save');await first.waitForFunction(()=>!document.querySelector('#security-lock-settings').open);
 await lock(first);await first.click('#security-unlock-open');assert.equal(await first.$$eval('#security-unlock-method option',es=>es.map(e=>e.value).join(',')),'totp');await unlock(first,otp(key,1),'totp');
 checks.push('Human activity resets idle, background polling does not; independent QR decoding and real TOTP-only unlock');
 await api('/api/security-lock/lock',{});await first.reload({waitUntil:'networkidle0'});await first.waitForSelector('#security-lock-screen[open]');assert.equal((await api('/api/config')).status,423);
 await first.evaluate(()=>CloudShellTheme.set('dark'));await first.waitForFunction(()=>!document.documentElement.dataset.lightSwitch);await first.screenshot({path:stage+'/locked-dark.png'});
 await first.setViewport({width:480,height:640});await first.click('#security-unlock-open');assert(await first.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));await first.waitForFunction(()=>{const r=document.querySelector('#security-unlock-form').getBoundingClientRect();return Math.abs((r.left+r.right)/2-innerWidth/2)<2&&Math.abs((r.top+r.bottom)/2-innerHeight/2)<2;});await first.screenshot({path:stage+'/locked-narrow.png'});
 const geometry=[];
 for(const [width,height,scale]of [[480,640,1],[800,600,1],[1440,1000,1],[1440,1000,1.5],[1920,1200,2]]){
  await first.setViewport({width,height});await first.evaluate(s=>{appearance.uiScale=s;applyUIScale()},scale);
  await first.waitForFunction(()=>{const r=document.querySelector('#security-unlock-form').getBoundingClientRect();return Math.abs((r.left+r.right)/2-innerWidth/2)<2&&Math.abs((r.top+r.bottom)/2-innerHeight/2)<2});
  const measured=await first.evaluate(()=>{const r=document.querySelector('#security-unlock-form').getBoundingClientRect(),theme=document.querySelector('#theme-toggle').getBoundingClientRect(),lock=document.querySelector('#security-lock-button').getBoundingClientRect();return {scale:effectiveScale,gap:(lock.left-theme.right)/effectiveScale,centerX:(r.left+r.right)/2,centerY:(r.top+r.bottom)/2,width:innerWidth,height:innerHeight}});
  assert(Math.abs(measured.gap-14)<.5,JSON.stringify(measured));geometry.push(measured);
 }
 fs.writeFileSync(stage+'/lock-layout.json',JSON.stringify({passed:true,geometry},null,2));
 await first.setViewport({width:480,height:640});await first.evaluate(()=>{appearance.uiScale=1;applyUIScale()});await new Promise(r=>setTimeout(r,100));await first.screenshot({path:stage+'/locked-narrow.png'});
 assert.deepEqual(errors,[]);checks.push('Reload remains locked; deep blur and centered unlock at five viewport/scale combinations; 14px button gap');
 fs.writeFileSync(stage+'/security-lock-browser.json',JSON.stringify({passed:true,checks,qrIndependentlyDecoded:true,errors},null,2));console.log('PASS',checks);
}finally{await browser.close()}
