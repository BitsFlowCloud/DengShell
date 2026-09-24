/* Move a live PTY between frontends; a drag never authenticates a replacement SSH. */
'use strict';
(() => {
 const pending = new Map();
 let activeDrag=null, menuCleanup=null, restoreRequest=null, restoringWindow=false, dropGeneration=0, pendingDrop=false;
 function cancelDrag(){dropGeneration++;pendingDrop=false;activeDrag?.();}
 function reflect(){
  const state=current(),button=document.getElementById('detach-terminal');if(!button)return;
  button.hidden=sessions.size<2;
  button.disabled=!state?.ready||!!state.detaching||!!state.ownershipUncertain;
  button.setAttribute('aria-busy',String(!!state?.detaching));
  button.querySelector('span').textContent=state?.detaching?'正在拆分…':'独立窗口';
  button.title=state?.detaching?'正在交接原 SSH 会话，请稍候':'将当前 SSH 拆分为独立窗口';
  window.DengWindowTransfers?.changed();
 }
 function nonce() { return crypto.randomUUID ? crypto.randomUUID() : [...crypto.getRandomValues(new Uint8Array(24))].map(x=>x.toString(16).padStart(2,'0')).join(''); }
 function waitForBarrier(state,id) {
  return new Promise((resolve,reject)=>{
   const timer=setTimeout(()=>{pending.delete(id);reject(new Error('终端交接准备超时，原连接已保留'));},8000);
   pending.set(id,{state,resolve:()=>{clearTimeout(timer);pending.delete(id);resolve()},reject:error=>{clearTimeout(timer);pending.delete(id);reject(error)}});
   sendMessage(state,{type:'handoff-begin',nonce:id});
  });
 }
 function handleMessage(state,message) {
  if(message.type==='handoff-cancelled'){if(state.ownershipUncertain){state.ownershipUncertain=false;state.term.options.disableStdin=!state.connected;renderSessionInfo();}return true}
  if(!['handoff-ready','handoff-error'].includes(message.type))return false;
  const job=pending.get(message.nonce);if(job?.state===state){if(message.type==='handoff-error')job.reject(new Error(message.message||'终端交接暂不可用，请重试'));else job.resolve()}return true;
 }
 async function detach(state,target=null) {
  if(!state?.ready||!state.connected||state.detaching||state.ownershipUncertain)return;
  if ([...localTasks.values()].some(task=>task.sessionId===state.id && task.file && ['queued','uploading'].includes(task.status))) throw new Error('请等待本窗口的浏览器文件上传完成后再移动');
  if(!state.serialize)throw new Error('终端快照组件尚未准备好');
  const popup=target||native()?null:window.open('about:blank','dengshell-'+nonce(),'popup=yes,width=1100,height=760');
  if(!target&&!native()&&!popup)throw new Error('浏览器阻止了独立窗口，请允许本地页面弹出窗口后重试');
  const preview=window.DengWindowPreview.begin(state,target),id=nonce();let completed=false,reserved=false,reservationAttempted=false;
  state.detaching=true;state.term.options.disableStdin=true;renderTabs();
  try {
   await Promise.all([window.DengPortablePreferences.flush(),preview.ready]);
   if(state.closed||!state.connected)throw new Error('连接已关闭，无法移动');
   await waitForBarrier(state,id);
   await new Promise(resolve=>state.term.write('',resolve));
   await window.DengCommandHistory.flush();
   const clientState={stats:state.stats,chart:state.chart,latency:state.latency,networkInterface:state.networkInterface,networkStats:state.networkStats,interfaceCharts:[...(state.interfaceCharts||new Map())],shellIntegration:state.shellIntegration,promptUsername:state.promptUsername,promptHostname:state.promptHostname,terminalDirectory:state.terminalDirectory,terminalRuntime:DengTerminalSnapshot.capture(state.term)};
   const snapshot={sessionId:state.id,nonce:id,terminal:state.serialize.serialize(),cols:state.term.cols,rows:state.term.rows,cwd:state.cwd,follow:state.follow,clientState};
   if(!target&&native()?.DetachWindow)await native().DetachWindow(snapshot);
   else {
    reservationAttempted=true;
    if(target)await window.DengWindowTransfers.prepare(target,snapshot);
    else await post('/api/windows/handoff',snapshot);
    reserved=true;
    if(popup)popup.location.replace(boot.base+'/?window='+encodeURIComponent(id)+'#token='+encodeURIComponent(boot.token));
    const deadline=Date.now()+35000;
    while(Date.now()<deadline){
     if(popup?.closed)throw new Error('独立窗口已关闭，原终端已恢复');
     const status=await api('/api/windows/handoff/'+id+'/status');if(status.attached){completed=true;break}
     await new Promise(resolve=>setTimeout(resolve,100));
    }
    if(!completed)throw new Error('目标窗口接手超时，原终端已恢复');
   }
   completed=true;dropSessionView(state.id,state);toast(target?'已合并到 '+target.title:'已移至独立窗口，SSH 会话保持连接');
  } catch(error) {
   const message=error?.message||String(error);
   if(message.includes('DENGSHELL_HANDOFF_UNCERTAIN:')){state.ownershipUncertain=true;throw new Error(message.split('DENGSHELL_HANDOFF_UNCERTAIN:').pop())}
   if(reserved||reservationAttempted) {
    let result;
    try {result=await post('/api/windows/handoff/'+id+'/cancel',{})}
    catch {state.ownershipUncertain=true;sendMessage(state,{type:'handoff-cancel',nonce:id});throw new Error('窗口交接状态暂时无法确认，正在请求恢复原终端，请保留目标窗口')}
    if(result.attached){completed=true;dropSessionView(state.id,state);toast('SSH 已由目标窗口接手');return}
   }
   sendMessage(state,{type:'handoff-cancel',nonce:id});popup?.close();throw error;
  } finally {
   preview.finish(completed);state.detaching=false;
   if(!completed&&state.ownershipUncertain&&state.ws?.readyState===WebSocket.CLOSED){completed=true;dropSessionView(state.id,state)}
   if(!completed&&!state.closed){state.term.options.disableStdin=!state.connected||!!state.ownershipUncertain;if(state.ws?.readyState===WebSocket.CLOSED)markSessionDisconnected(state);renderTabs();fitActive();}
   if(completed)window.DengWindowTransfers.afterTransfer().catch(error=>toast(error.message));
  }
 }
 function bindTab(tab,state) {
  tab.style.setProperty('--wails-draggable','no-drag');tab.title=state.detaching?'正在移至独立窗口…':'拖出标签栏以拆分 · Esc 取消';
  let drag=null,suppressUntil=0;
  const cleanup=()=>{
   const old=drag;drag=null;cancelAnimationFrame(old?.frame);clearInterval(old?.probeTimer);old?.preview?.remove();
   document.removeEventListener('pointermove',move);document.removeEventListener('pointerup',up);document.removeEventListener('pointercancel',cancel);
   if(old&&tab.hasPointerCapture(old.id))tab.releasePointerCapture(old.id);
   document.documentElement.classList.remove('session-tab-dragging');if(activeDrag===cancel)activeDrag=null;
  };
  const cancel=()=>{if(drag?.moved)suppressUntil=Date.now()+600;cleanup()};
  tab.addEventListener('click',event=>{if(Date.now()<suppressUntil){event.preventDefault();event.stopImmediatePropagation()}},true);
  tab.addEventListener('pointerdown',event=>{
   if(event.button!==0||event.target.closest('.tab-close')||!state.ready||state.detaching||state.ownershipUncertain)return;
   cancelDrag();drag={id:event.pointerId,x:event.clientX,y:event.clientY,moved:false,bounds:tab.parentElement.getBoundingClientRect()};activeDrag=cancel;
   // Preserve the button as the click target until this is an actual drag.
   document.addEventListener('pointermove',move);document.addEventListener('pointerup',up);document.addEventListener('pointercancel',cancel);
  });
  const move=event=>{
   if(!drag||event.pointerId!==drag.id)return;
   if(!drag.moved&&Math.hypot(event.clientX-drag.x,event.clientY-drag.y)<10)return;
   if(!drag.moved)tab.setPointerCapture(event.pointerId);
   drag.moved=true;document.documentElement.classList.add('session-tab-dragging');
   if(!drag.preview){
    const {preview,label}=window.DengWindowPreview.drag(state);drag.label=label;
    document.documentElement.append(preview);drag.preview=preview;
    const active=drag;const probe=async()=>{if(drag!==active||active.probing)return;active.probing=true;try{const target=await window.DengWindowTransfers.atPointer();if(drag===active){active.target=target;updateDragLabel();}}catch{}finally{active.probing=false}};
    active.probeTimer=setInterval(probe,120);probe();
   }
   const b=drag.bounds;drag.detach=event.clientY<b.top-22||event.clientY>b.bottom+40||event.clientX<-8||event.clientX>innerWidth+8;
   updateDragLabel();
   drag.left=Math.max(8,Math.min(innerWidth-drag.preview.offsetWidth-8,event.clientX+16));drag.top=Math.max(8,Math.min(innerHeight-drag.preview.offsetHeight-8,event.clientY+16));
   cancelAnimationFrame(drag.frame);drag.frame=requestAnimationFrame(()=>{if(drag)drag.preview.style.transform=`translate3d(${drag.left}px,${drag.top}px,0)`});
  };
  const updateDragLabel=()=>{if(!drag?.preview)return;const label=drag.target?'松开，合并到 '+drag.target.title:drag.detach?'松开，移至独立窗口':'拖向另一个窗口合并，或移出以拆分';drag.label.textContent=label;drag.preview.classList.toggle('will-detach',drag.detach&&!drag.target);drag.preview.classList.toggle('will-merge',!!drag.target);};
  const up=event=>{
   if(!drag||event.pointerId!==drag.id)return;
   const moved=drag.moved,action=drag.detach,knownTarget=drag.target;if(moved)suppressUntil=Date.now()+600;
   cleanup();if(moved){event.preventDefault();const token=++dropGeneration;pendingDrop=true;safe(async()=>{try{const target=await window.DengWindowTransfers.atPointer();if(token!==dropGeneration||state.closed||state.detaching)return;if(target)await detach(state,target);else if(action&&!knownTarget)await detach(state);else if(knownTarget)toast('目标窗口已离开，请重新拖动');}finally{if(token===dropGeneration)pendingDrop=false}})()}
  };
  tab.addEventListener('pointercancel',cancel);tab.addEventListener('lostpointercapture',cleanup);
  tab.addEventListener('keydown',event=>{if(event.shiftKey&&event.key==='F10'){event.preventDefault();openTabMenu(event,tab,state)}});
  tab.addEventListener('contextmenu',event=>{event.preventDefault();openTabMenu(event,tab,state)});
 }
 function openTabMenu(event,tab,state){
  menuCleanup?.();if(!state.ready||state.detaching||state.ownershipUncertain)return;
  const menu=node('div','session-tab-menu');menu.id='session-tab-menu';menu.setAttribute('role','menu');
  const button=node('button','','拆分为独立窗口');button.type='button';button.setAttribute('role','menuitem');button.append(icon('popout'));
  const dismiss=e=>{if(!menu.contains(e.target))close()};
  const close=()=>{menu.remove();document.removeEventListener('pointerdown',dismiss);if(menuCleanup===close)menuCleanup=null};menuCleanup=close;
  const another=node('button','','新建同服务器会话');another.type='button';another.setAttribute('role','menuitem');another.onclick=safe(()=>{close();return connect(state.profileId)});menu.append(another);
  button.onclick=safe(()=>{close();return detach(state)});menu.append(button);
  const caption=node('div','merge-window-caption','合并到窗口'),loading=node('div','merge-window-empty','正在查找其他窗口…');menu.append(caption,loading);document.documentElement.append(menu);
  window.DengWindowTransfers.targets().then(targets=>{
   if(!menu.isConnected)return;loading.remove();
   if(!targets.length)menu.append(node('div','merge-window-empty','暂无其他可见的 DengShell 窗口'));
   for(const target of targets){const item=node('button','merge-window-option');item.type='button';item.setAttribute('role','menuitem');item.append(node('span','',target.title),icon('layout'));item.title=target.title;item.onclick=safe(()=>{close();return detach(state,target)});menu.append(item);}
   menu.style.top=Math.max(8,Math.min(innerHeight-menu.offsetHeight-8,tab.getBoundingClientRect().bottom+5))+'px';
  }).catch(()=>{if(menu.isConnected)loading.textContent='无法读取窗口列表，请稍后重试'});
  const rect=tab.getBoundingClientRect();menu.style.left=Math.max(8,Math.min(innerWidth-menu.offsetWidth-8,event.clientX||rect.left))+'px';menu.style.top=Math.min(innerHeight-menu.offsetHeight-8,rect.bottom+5)+'px';button.focus();
  document.addEventListener('pointerdown',dismiss);menu.onkeydown=e=>{if(e.key==='Escape'){close();tab.querySelector('button')?.focus()}};
 }
 function prepareRestore(){
  if(boot.detachedNonce&&!window.dengshellDetachedRestored&&!restoringWindow&&!restoreRequest){restoreRequest=api('/api/windows/handoff/'+encodeURIComponent(boot.detachedNonce));restoreRequest.catch(()=>{});}
 }
 function installSnapshot(saved,id,initial=false){
  const existing=sessions.get(saved.session.id);if(existing?.connected)throw new Error('当前窗口已经包含此 SSH 标签');if(existing)dropSessionView(existing.id);
  const state=makeSessionState(saved.session);Object.assign(state,saved.clientState||{});state.interfaceCharts=new Map(saved.clientState?.interfaceCharts||[]);state.tabOrder=nextSessionOrder++;
  state.cwd=saved.cwd;state.follow=saved.follow;state.restoration=saved;state.restoring=true;state.handoffNonce=id;state.initialWindowRestore=initial;state.handoffProvisional=true;
  sessions.set(state.id,state);try{createTerminal(state);activate(state.id);setDrawer(false);}catch(error){dropSessionView(state.id,state);throw error;}
  navigate(state.cwd,state).catch(error=>{if(!state.closed)toast('窗口已接手，目录读取失败：'+error.message)});pollStats();pollLatency();window.DengWindowTransfers.changed();return state;
 }
 async function awaitReady(state){
  const until=Date.now()+15000;
  while(Date.now()<until){if(state.windowRestoreError)throw state.windowRestoreError;if(state.closed||!state.connected)throw new Error('目标终端未能接手，已恢复原窗口');if(state.ready){window.DengWindowPreview.arrive(state);return;}await new Promise(resolve=>setTimeout(resolve,30));}
  throw new Error('目标终端接手超时，请检查原窗口');
 }
 async function receive(id,viewId){
  const previous=activeID;let state;
  try{
   const saved=await api('/api/windows/handoff/'+encodeURIComponent(id)+'?viewId='+encodeURIComponent(viewId));
   await window.DengCommandHistory.refresh(saved.session.profileId);
   state=installSnapshot(saved,id);await awaitReady(state);if(native()?.RestoreWindow)await native().RestoreWindow();else window.focus();return state;
  }catch(error){
   let result;
   for(const delay of [0,150,450]){if(delay)await new Promise(resolve=>setTimeout(resolve,delay));try{result=await post('/api/windows/handoff/'+id+'/cancel',{});break}catch{}}
   if(state&&sessions.get(state.id)===state){
    if(result&&!result.attached){const focused=activeID===state.id;dropSessionView(state.id,state);if(focused&&sessions.has(previous))activate(previous);}
    else{state.handoffProvisional=!result;state.ownershipUncertain=!result;state.term.options.disableStdin=!state.ready||!result;renderTabs();if(activeID===state.id)renderSessionInfo();}
   }
   throw error;
  }
 }
 async function restore() {
  const id=boot.detachedNonce;if(!id||window.dengshellDetachedRestored||restoringWindow)return;
  prepareRestore();restoringWindow=true;
  try {
   let saved;try{saved=await restoreRequest}finally{restoreRequest=null}
   await window.DengCommandHistory.refresh(saved.session.profileId);
   const state=installSnapshot(saved,id,true);window.dengshellDetachedRestored=true;
   awaitReady(state).catch(error=>{if(!state.closed)toast(error.message)});
  } finally {restoringWindow=false}
 }
 document.addEventListener('keydown',event=>{if(event.key==='Escape'&&(activeDrag||pendingDrop)){event.preventDefault();event.stopPropagation();cancelDrag()}},true);
 document.addEventListener('DOMContentLoaded',()=>{document.getElementById('detach-terminal').onclick=safe(()=>detach(current()));reflect();},{once:true});
 window.DengSessionWindows={bindTab,handleMessage,detach,restore,receive,reflect,cancelDrag,prepareRestore};
})();
