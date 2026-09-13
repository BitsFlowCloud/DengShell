// Native minimize policy; settings stay portable with the rest of Appearance.
(() => {
 'use strict';
 let state={available:false,action:'ask'},dialog=null;
 function setHidden(hidden){const previous=window.DengShellWindowHidden;window.DengShellWindowHidden=hidden;if(previous&&!hidden){if(typeof pollStats==='function')pollStats();if(typeof pollNetwork==='function')pollNetwork();window.CloudShellTheme?.refreshSystem();}}
 async function refresh(){if(!native()?.MinimizeState)return;state=await native().MinimizeState();setHidden(!!(state.hidden||state.minimized));const control=document.getElementById('minimize-to-tray');if(control){control.checked=state.action==='tray';control.disabled=!state.available;document.getElementById('minimize-to-tray-hint').textContent=state.available?(state.action==='ask'?'首次最小化时询问':'关闭后使用普通最小化'):'当前桌面未提供托盘，使用普通最小化';}if(state.pending)showChoice();}
 function showChoice(){
  if(dialog?.open)return;window.DengShellSplash?.finish('interaction');dialog=document.createElement('dialog');dialog.className='manager-dialog tray-choice-dialog';dialog.setAttribute('aria-labelledby','tray-choice-title');dialog.innerHTML='<div class="dialog-heading"><h2 id="tray-choice-title">最小化到哪里？</h2></div><p>可隐藏到系统托盘继续保持 SSH 连接，也可以保留在任务栏。</p><p class="tray-unavailable-note" hidden>当前桌面没有可用托盘，将使用普通最小化。</p><label class="tray-remember"><input type="checkbox" checked>记住我的选择，可在设置中修改</label><div class="tray-choice-actions"><button type="button" data-choice="cancel">取消</button><button type="button" data-choice="minimize">保留在任务栏</button><button type="button" class="primary-button" data-choice="tray">隐藏到托盘</button></div>';
  dialog.querySelector('[data-choice="tray"]').disabled=!state.available;dialog.querySelector('.tray-unavailable-note').hidden=state.available;
  const choose=async action=>{for(const b of dialog.querySelectorAll('button'))b.disabled=true;try{if(action!=='cancel'&&dialog.querySelector('input').checked)await persistAppearance({minimizeAction:action});await native().ChooseMinimize(action);dialog.close();}catch(e){toast(e.message);for(const b of dialog.querySelectorAll('button'))b.disabled=false;dialog.querySelector('[data-choice="tray"]').disabled=!state.available;}};
  dialog.querySelectorAll('[data-choice]').forEach(button=>button.onclick=()=>choose(button.dataset.choice));dialog.oncancel=event=>{event.preventDefault();choose('cancel')};dialog.addEventListener('close',()=>{dialog.remove();dialog=null;refresh().catch(()=>{});},{once:true});document.body.append(dialog);dialog.showModal();dialog.querySelector('[data-choice="minimize"]').focus();
 }
 document.addEventListener('DOMContentLoaded',async()=>{
  if(!desktopPage)return;await waitForDesktop();if(!native()?.MinimizeState)return;
  const menu=document.getElementById('settings-menu');const row=document.createElement('label');row.className='tray-setting';row.innerHTML='<span>最小化到系统托盘<small id="minimize-to-tray-hint"></small></span><input id="minimize-to-tray" type="checkbox" role="switch">';const first=menu.querySelector('.startup-animation-setting');if(first)first.after(row);else menu.prepend(row);
  row.querySelector('input').addEventListener('change',async event=>{const input=event.target;input.disabled=true;try{await persistAppearance({minimizeAction:input.checked?'tray':'minimize'});}catch(e){toast(e.message)}finally{await refresh()}});
  runtime.EventsOn('dengshell:window-visibility',value=>setHidden(!!value.hidden));runtime.EventsOn('dengshell:choose-minimize',value=>{state=value;showChoice()});runtime.EventsOn('dengshell:tray-unavailable',()=>{toast('系统托盘已不可用，窗口已恢复显示');refresh().catch(()=>{})});document.getElementById('settings-button').addEventListener('click',()=>refresh().catch(()=>{}));await refresh();
 },{once:true});
})();
