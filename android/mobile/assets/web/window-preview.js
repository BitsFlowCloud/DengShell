/* A small live-text preview bridges the handoff; it never creates another PTY. */
'use strict';
(() => {
 const reduced=()=>matchMedia('(prefers-reduced-motion: reduce)').matches;
 function card(state){
  const box=node('div','window-miniature'),bar=node('div','window-miniature-bar'),dots=node('span','window-miniature-dots');
  dots.append(node('i'),node('i'),node('i'));bar.append(icon('terminal'),node('strong','',profileFor(state)?.name||'SSH 会话'),dots);
  const text=node('pre','window-miniature-terminal'),buffer=state?.term?.buffer.active;
  if(buffer){const start=Math.max(0,buffer.viewportY);text.textContent=Array.from({length:Math.min(9,state.term.rows)},(_,i)=>buffer.getLine(start+i)?.translateToString(true)||'').join('\n');}
  box.append(bar,text);return box;
 }
 function drag(state){const preview=node('div','session-drag-preview'),label=node('span','session-drag-label');preview.append(card(state),label,node('small','','Esc 取消'));return {preview,label};}
 function begin(state,target){
  const overlay=node('div','window-opening-preview'),panel=node('div','window-opening-card'),label=node('span','window-opening-label',target?'正在合并到 '+target.title:'正在打开独立窗口…');
  panel.append(card(state),label);overlay.append(panel);overlay.setAttribute('role','status');overlay.setAttribute('aria-live','polite');document.documentElement.append(overlay);
  const ready=new Promise(resolve=>{requestAnimationFrame(()=>{overlay.classList.add('visible');setTimeout(resolve,reduced()?0:240)});});
  let ended=false;
  return{ready,finish(success){if(ended)return;ended=true;overlay.classList.add(success?'completed':'failed');overlay.classList.remove('visible');setTimeout(()=>overlay.remove(),reduced()?0:200)}};
 }
 function arrive(state){
  if(reduced())return;
  const host=state?.host;if(!host)return;
  host.animate?.([{opacity:.25,filter:'blur(2px)',transform:'scale(.985)'},{opacity:1,filter:'blur(0)',transform:'scale(1)'}],{duration:340,easing:'cubic-bezier(.2,.7,.25,1)'});
  const tab=[...document.querySelectorAll('.session-tab')].find(el=>el.dataset.sessionId===state.id);
  tab?.animate?.([{transform:'translateY(5px)',opacity:.4},{transform:'translateY(0)',opacity:1}],{duration:280,easing:'ease-out'});
 }
 window.DengWindowPreview={drag,begin,arrive};
})();
