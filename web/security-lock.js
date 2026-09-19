// Loaded before app.js so lock cover/keyboard interception exist before any
// workspace handlers. The backend remains authoritative across all windows.
(() => {
 'use strict';
 const boot = window.CLOUDSHELL || {};
 let state = boot.securityLock || { enabled:false, locked:false, revision:0 };
 let covered = !!state.locked, screen, settings, grant='', enrolling=false, lastSent=0, activityBusy=false, statusBusy=false, forcingLock=false;
 let resolveReady; const ready = new Promise(resolve => { resolveReady=resolve; });
 const waiters=[],inertBefore=new Map(),deferredDialogs=new Set();
 const el=id=>document.getElementById(id);
 const originalModal=HTMLDialogElement.prototype.showModal;
 HTMLDialogElement.prototype.showModal=function(){if(covered&&this.id!=='security-lock-screen'){deferredDialogs.add(this);return;}return originalModal.call(this);};
 const originalPopover=HTMLElement.prototype.showPopover;
 if(originalPopover)HTMLElement.prototype.showPopover=function(...args){if(covered&&!screen?.contains(this))return;return originalPopover.apply(this,args);};
 if(covered)document.documentElement.dataset.securityLocked='true';
 function isolate(){
  if(!document.body)return;
  for(const child of document.body.children){
   if(child===screen||['SCRIPT','STYLE','LINK'].includes(child.tagName))continue;
   if(!inertBefore.has(child))inertBefore.set(child,child.inert);
   child.inert=true;
   if(child.matches?.(':popover-open'))child.hidePopover?.();
  }
 }
 function fitScreen(){if(screen){const scale=Number(document.body.style.zoom)||1;screen.style.width=(innerWidth/scale)+'px';screen.style.height=(innerHeight/scale)+'px';}}
 function cover(value,confirmed=true){
  const changed=covered!==value;covered=value;
  if(value){
   document.documentElement.dataset.securityLocked='true';fitScreen();
   if(confirmed&&settings?.open){settings.close();clearSettingsSecrets();}
   isolate();window.DengShellSplash?.finish('locked');
   if(changed&&el('exit-dialog')?.open){el('exit-dialog').close();queueMicrotask(()=>window.requestQuit?.());}
   if(screen&&!screen.open){originalModal.call(screen);el('security-unlock-open').focus();}
  }else{
   delete document.documentElement.dataset.securityLocked;
   if(screen?.open)screen.close();
   for(const [child,was]of inertBefore){if(child.isConnected)child.inert=was;}inertBefore.clear();
   for(const dialog of deferredDialogs){if(dialog.isConnected&&!dialog.open)originalModal.call(dialog);}deferredDialogs.clear();
   if(changed){for(const resolve of waiters.splice(0))resolve();window.dispatchEvent(new Event('dengshell:unlocked'));}
  }
 }
 function apply(next){
  if(!next||typeof next.enabled!=='boolean')return;
  // Responses from a request begun before a newer lock/unlock cannot clear it.
  if(Number(next.revision)<Number(state.revision))return;
  state=next;cover(forcingLock||state.locked);
  const closeWindow=el('security-window-close');if(closeWindow)closeWindow.hidden=!window.go?.main?.Desktop?.ConfirmQuit;
  const manual=el('security-lock-button');if(manual){manual.disabled=!state.enabled;manual.title=state.enabled?'立即锁定所有窗口':'请先在设置中启用安全锁定';}
  const options=el('security-unlock-method');if(options){
   const previous=options.value,methods=[];
   if(state.passwordConfigured)methods.push(['password','密码解锁']);
   if(state.twoFactorConfigured)methods.push(['totp','2FA 验证码解锁']);
   if(options.dataset.methods!==JSON.stringify(methods)){
    options.replaceChildren(...methods.map(([value,label])=>new Option(label,value)));options.value=methods.some(m=>m[0]===previous)?previous:methods[0]?.[0]||'';options.dataset.methods=JSON.stringify(methods);
   }
   el('security-method-label').hidden=methods.length<2;reflectUnlock();
  }
 }
 async function call(path,body){
  const target='/api/security-lock/'+path;
  const bridge=window.go?.main?.Desktop;
  if(bridge?.Request){try{return JSON.parse(await bridge.Request(body===undefined?'GET':'POST',target,body===undefined?'':JSON.stringify(body)));}catch(error){const text=error?.message||String(error),i=text.indexOf('DENGSHELL_ERROR:');if(i>=0){let data;try{data=JSON.parse(text.slice(i+16));}catch{}if(data)throw Object.assign(new Error(data.error),{code:data.code});}throw error;}}
  const r=await fetch(boot.base+target,{method:body===undefined?'GET':'POST',headers:{'Content-Type':'application/json','X-CloudShell-Token':boot.token},body:body===undefined?undefined:JSON.stringify(body),cache:'no-store'});
  const data=await r.json();if(!r.ok)throw Object.assign(new Error(data.error||'安全锁定服务暂不可用'),{code:data.code});return data;
 }
 async function refresh(){
  if(statusBusy)return;statusBusy=true;
  try{apply(await call('status'));}catch{if(state.enabled){cover(true);if(el('security-lock-error'))el('security-lock-error').textContent='暂时无法连接锁定服务，请稍后重试';}}
  finally{statusBusy=false;}
 }
 function reflectUnlock(){
  if(!screen)return;const method=el('security-unlock-method').value,field=el('security-unlock-value');
  field.type=method==='password'?'password':'text';field.inputMode=method==='totp'?'numeric':'text';field.autocomplete=method==='totp'?'one-time-code':'current-password';field.maxLength=method==='totp'?6:72;field.placeholder=method==='totp'?'000000':'请输入解锁密码';field.dataset.method=method;
  el('security-unlock-value-label').textContent=method==='totp'?'验证器中的 6 位验证码':'解锁密码';
  el('security-unlock-hint').textContent=method==='password'&&state.passwordHint?'密码提醒：'+state.passwordHint:method==='totp'?'验证码每 30 秒变化，已使用的验证码不能重复使用。':'';
  const retry=state.retryAfterSeconds||0;el('security-unlock-submit').disabled=retry>0;el('security-unlock-retry').textContent=retry?`尝试次数过多，${retry} 秒后可重试`:'';
 }
 function proof(prefix){return {method:el(prefix+'-method').value,value:el(prefix+'-value').value};}
 function clearSettingsSecrets(){grant='';enrolling=false;if(!settings)return;for(const id of ['security-new-password','security-repeat-password','security-auth-value','security-enroll-code','security-enroll-secret'])el(id).value='';el('security-enroll-qr').removeAttribute('src');el('security-enrollment').hidden=true;}
 function reflectSettings(){
  const enabled=el('security-enabled').checked,editable=!!grant;
  settings.dataset.verifying=String(state.enabled&&!editable);
  el('security-options').disabled=!enabled||!editable;
  el('security-password-fields').hidden=!el('security-use-password').checked;
  el('security-totp-fields').hidden=!el('security-use-totp').checked;
  const manual=el('security-lock-mode').value==='manual';el('security-idle-seconds').disabled=manual;
  el('security-timing-hint').textContent=manual?'点击明暗按钮右侧的锁，即可立即锁定。':'无鼠标或键盘操作达到设定时长后锁定，也可随时手动上锁。';
  el('security-enabled').disabled=!editable;el('security-save').disabled=!editable;
 }
 async function openSettings(){
  await refresh();if(covered)return;clearSettingsSecrets();
  el('security-settings-error').textContent='';el('security-auth-error').textContent='';
  el('security-enabled').checked=state.enabled;el('security-use-password').checked=state.passwordConfigured||!state.twoFactorConfigured;
  el('security-use-totp').checked=state.twoFactorConfigured;el('security-password-hint').value=state.passwordHint||'';
  el('security-lock-mode').value=state.idleSeconds>0?'idle':'manual';el('security-idle-seconds').value=String(state.idleSeconds||300);
  el('security-new-password').placeholder=state.passwordConfigured?'留空保留现有密码':'至少 4 位，可使用数字或其他字符';
  el('security-totp-state').textContent=state.twoFactorConfigured?'已绑定验证器':'尚未绑定验证器';el('security-enroll-start').textContent=state.twoFactorConfigured?'重新绑定验证器':'扫码绑定验证器';
  el('security-auth').hidden=!state.enabled;
  const methods=el('security-auth-method');methods.replaceChildren(...[['password','密码'],['totp','2FA 验证码']].filter(([m])=>m==='password'?state.passwordConfigured:state.twoFactorConfigured).map(([v,t])=>new Option(t,v)));
  el('security-auth-value').type=methods.value==='totp'?'text':'password';
  settings.showModal();reflectSettings();
  if(!state.enabled){try{grant=(await call('authorize',{})).grant;reflectSettings();}catch(e){el('security-settings-error').textContent=e.message;}}
 }
 async function activity(event){
  if(!event.isTrusted||covered||!state.enabled||!state.idleSeconds||document.hidden||window.DengShellWindowHidden)return;
  const interval=Math.min(1000,Math.max(100,state.idleSeconds*250));if(activityBusy||Date.now()-lastSent<interval)return;
  lastSent=Date.now();activityBusy=true;try{apply(await call('activity',{}));}catch{cover(true);}finally{activityBusy=false;}
 }
 // Stop the application's global terminal/editor shortcuts at the first
 // capture listener. Native input editing/paste defaults still work in the form.
 for(const name of ['keydown','keyup','keypress','copy','cut','paste'])window.addEventListener(name,event=>{
  if(!covered)return;
  if(event.key==='Escape'||((event.ctrlKey||event.metaKey)&&!['a','c','v','x'].includes(event.key?.toLowerCase()))||!screen?.contains(event.target)){event.preventDefault();}
  event.stopImmediatePropagation();
 },true);
 for(const name of ['pointerdown','click','dblclick','contextmenu','wheel','drop','dragover'])window.addEventListener(name,event=>{
  if(covered&&!screen?.contains(event.target)){event.preventDefault();event.stopImmediatePropagation();}
 },{capture:true,passive:false});
 for(const name of ['pointerdown','pointermove','keydown','wheel','touchstart'])window.addEventListener(name,activity,{capture:true,passive:true});
 window.addEventListener('focus',()=>{if(state.enabled){cover(true,false);refresh();}});
 document.addEventListener('visibilitychange',()=>{if(!document.hidden&&state.enabled){cover(true,false);refresh();}});
 function confirmExit(description, action, cancel) {
  if (!covered || !screen) return false;
  const form = el('security-exit-form');
  if (!form.hidden) return true;
  const previous = el('security-unlock-form').hidden ? 'security-lock-message' : 'security-unlock-form';
  el('security-unlock-value').value='';
  el('security-lock-message').hidden=true; el('security-unlock-form').hidden=true;
  el('security-exit-description').textContent=description; el('security-exit-error').textContent=''; form.hidden=false;
  const restore=()=>{form.hidden=true;el(previous).hidden=false;};
  el('security-exit-cancel').onclick=async()=>{await cancel();restore();el(previous==='security-lock-message'?'security-unlock-open':'security-unlock-value').focus();};
  form.onsubmit=async event=>{event.preventDefault();const button=el('security-exit-confirm');button.disabled=true;
   try{await action();restore();}catch(error){el('security-exit-error').textContent=error.message||String(error);}finally{button.disabled=false;}
  };
  el('security-exit-cancel').focus();return true;
 }
 window.DengSecurityLock={confirmExit,ready,isLocked:()=>covered,whenUnlocked:()=>covered?new Promise(r=>waiters.push(r)):Promise.resolve(),blocked(){cover(true);refresh();},openSettings};
 document.addEventListener('DOMContentLoaded',async()=>{
  screen=el('security-lock-screen');settings=el('security-lock-settings');
  screen.addEventListener('cancel',e=>e.preventDefault());screen.addEventListener('close',()=>{if(covered)originalModal.call(screen);});
  new MutationObserver(()=>{if(covered){isolate();fitScreen();}}).observe(document.body,{childList:true,attributes:true,attributeFilter:['style']});
  window.addEventListener('resize',fitScreen);
  el('security-window-close').onclick=()=>window.requestQuit?.();
  el('security-lock-button').onclick=async()=>{if(!state.enabled)return;forcingLock=true;cover(true);try{const next=await call('lock',{});forcingLock=false;apply(next);}catch(e){forcingLock=false;el('security-lock-error').textContent=e.message;await refresh();}};
  el('manage-security-lock').onclick=openSettings;
  el('security-unlock-open').onclick=()=>{el('security-lock-message').hidden=true;el('security-unlock-form').hidden=false;el('security-unlock-error').textContent='';el('security-unlock-value').focus();};
  el('security-unlock-cancel').onclick=()=>{el('security-unlock-value').value='';el('security-unlock-form').hidden=true;el('security-lock-message').hidden=false;el('security-unlock-open').focus();};
  el('security-unlock-method').onchange=()=>{el('security-unlock-value').value='';reflectUnlock();el('security-unlock-value').focus();};
  el('security-unlock-form').onsubmit=async e=>{
   e.preventDefault();const button=el('security-unlock-submit');button.disabled=true;el('security-unlock-error').textContent='';
   try{const next=await call('unlock',proof('security-unlock'));el('security-unlock-value').value='';apply(next);el('security-unlock-form').hidden=true;el('security-lock-message').hidden=false;}
   catch(error){el('security-unlock-error').textContent=error.message;el('security-unlock-value').value='';await refresh();}finally{button.disabled=!!state.retryAfterSeconds;}
  };
  el('security-settings-close').onclick=()=>settings.close();settings.addEventListener('close',clearSettingsSecrets);
  el('security-auth-value').onkeydown=event=>{if(event.key==='Enter'&&!event.isComposing){event.preventDefault();el('security-auth-button').click();}};
  el('security-auth-method').onchange=()=>{el('security-auth-value').value='';el('security-auth-value').type=el('security-auth-method').value==='totp'?'text':'password';};
  el('security-auth-button').onclick=async()=>{
   const button=el('security-auth-button');button.disabled=true;el('security-auth-error').textContent='';
   try{grant=(await call('authorize',proof('security-auth'))).grant;el('security-auth-value').value='';el('security-auth').hidden=true;reflectSettings();}catch(e){el('security-auth-error').textContent=e.message;}finally{button.disabled=false;}
  };
  for(const id of ['security-enabled','security-use-password','security-use-totp','security-lock-mode'])el(id).onchange=reflectSettings;
  el('security-enroll-start').onclick=async()=>{
   const button=el('security-enroll-start');button.disabled=true;el('security-settings-error').textContent='';
   try{const value=await call('enroll',{grant});if(!settings.open||covered)return;enrolling=true;el('security-enroll-qr').src=value.qr;el('security-enroll-secret').value=value.secret;el('security-enroll-code').value='';el('security-enrollment').hidden=false;el('security-totp-state').textContent='等待验证码确认，保存后生效';}catch(e){el('security-settings-error').textContent=e.message;}finally{button.disabled=false;}
  };
  el('security-settings-form').onsubmit=async e=>{
   e.preventDefault();el('security-settings-error').textContent='';
   const password=el('security-new-password').value;
   if(el('security-enabled').checked&&el('security-use-password').checked&&password!==el('security-repeat-password').value){el('security-settings-error').textContent='两次输入的密码不一致';return;}
   const button=el('security-save');button.disabled=true;
   try{
    const next=await call('settings',{grant,enabled:el('security-enabled').checked,idleSeconds:el('security-lock-mode').value==='manual'?0:Number(el('security-idle-seconds').value),passwordEnabled:el('security-use-password').checked,password,passwordHint:el('security-password-hint').value,twoFactorEnabled:el('security-use-totp').checked,code:enrolling?el('security-enroll-code').value:''});
    apply(next);settings.close();window.toast?.('安全锁定设置已保存');
   }catch(error){el('security-settings-error').textContent=error.message;}finally{button.disabled=!grant;}
  };
  apply(state);await refresh();resolveReady();
  setInterval(refresh,1000);
 },{once:true});
})();
