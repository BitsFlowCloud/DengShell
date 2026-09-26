/* Discover visible sibling windows in the same backend; transfer only on request. */
'use strict';
(() => {
 const id=crypto.randomUUID?crypto.randomUUID():Array.from(crypto.getRandomValues(new Uint8Array(24)),v=>v.toString(16).padStart(2,'0')).join('');
 let registered=false,registerPromise=null,pollBusy=false,closing=false;
 const receiving=new Set(),completed=new Set();
 const available=()=>typeof sessions!=='undefined'&&window.DengPortablePreferences?.ready;
 function shortTitle(value){let title='',bytes=0;for(const c of value){bytes+=new TextEncoder().encode(c).length;if(bytes>220)return title+'…';title+=c;}return title;}
 function details(){const states=[...sessions.values()].filter(s=>!s.closed&&!s.localOnly);const names=states.map(s=>profileFor(s)?.name||'SSH');return{id,title:shortTitle((boot.detachedNonce?'独立窗口':'主窗口')+(names.length?' · '+names.slice(0,3).join('、'):' · DengShell')),sessionIds:states.map(s=>s.id),visible:!window.DengShellWindowHidden};}
 async function register(){
  if(!available()||closing)return;
  if(registerPromise){await registerPromise;return;}
  const request=details();registerPromise=(async()=>{if(native()?.RegisterWindowView)await native().RegisterWindowView(request);else await post('/api/windows/views',request);registered=true;})();
  try{await registerPromise}finally{registerPromise=null}
 }
 async function incoming(){
  if(!registered||pollBusy||closing||window.DengShellWindowHidden)return;
  pollBusy=true;
  try{const list=await api('/api/windows/views/'+id+'/incoming');for(const item of list){if(receiving.has(item.nonce)||completed.has(item.nonce))continue;receiving.add(item.nonce);
    window.DengSessionWindows.receive(item.nonce,id).then(()=>{completed.add(item.nonce);if(completed.size>128)completed.delete(completed.values().next().value);return register()}).catch(error=>{toast('合并未完成：'+error.message)}).finally(()=>receiving.delete(item.nonce));
  }}catch{}finally{pollBusy=false}
 }
 async function targets(){await register();const views=await api('/api/windows/views');return views.filter(v=>v.id!==id&&v.visible);}
 async function atPointer(){if(!native()?.WindowDropTarget)return null;const result=await native().WindowDropTarget(id);return result?.target||null;}
 async function prepare(target,snapshot){await register();return post('/api/windows/merge',{sourceId:id,targetId:target.id,handoff:snapshot});}
 async function afterTransfer(){await window.DengCommandHistory.flush();await register();if(boot.detachedNonce&&sessions.size===0){closing=true;await api('/api/windows/views/'+id,{method:'DELETE'}).catch(()=>{});try{if(native()?.ConfirmQuit)await native().ConfirmQuit();else window.close();}catch(error){closing=false;await register().catch(()=>{});throw error;}}}
 function changed(){if(available())register().catch(()=>{});}
 document.addEventListener('DOMContentLoaded',()=>{
  changed();setInterval(changed,2000);setInterval(incoming,600);
  window.addEventListener('focus',()=>{changed();incoming()});
  window.addEventListener('beforeunload',()=>{closing=true;if(registered)api('/api/windows/views/'+id,{method:'DELETE',keepalive:true}).catch(()=>{})});
 },{once:true});
 window.DengWindowTransfers={id,register,targets,atPointer,prepare,afterTransfer,changed};
})();
