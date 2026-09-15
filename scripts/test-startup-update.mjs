import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import vm from 'node:vm';

class Element extends EventTarget {
 constructor(){super();this.children=new Map();this.hidden=false;this.disabled=false;this.textContent='';this.value=0;}
 querySelector(selector){if(!this.children.has(selector))this.children.set(selector,new Element());return this.children.get(selector)}
 setAttribute(){}
 removeAttribute(name){if(name==='value')this.value=undefined}
 showModal(){}
 focus(){}
 remove(){}
 close(){this.dispatchEvent(new Event('close'))}
}
const tick=()=>new Promise(resolve=>setImmediate(resolve));
async function fixture(){
 const document=new EventTarget(),window=new EventTarget();let dialog,listener,rejectInstall,installs=0,unsubscribed=0;
 document.createElement=()=>dialog=new Element();document.body={append(){}};
 window.runtime={EventsOn(name,callback){assert.equal(name,'dengshell:update-progress');listener=callback;return()=>{listener=undefined;unsubscribed++}}};
 const offer={status:'available',platform:'windows-amd64',currentVersion:'v0.01',latestVersion:'v0.01',currentBuild:20260915042,latestBuild:20260915043,package:{sha256:'fixture'}};
 const api=async path=>path==='/api/updates/check'?offer:{id:'job',status:'ready'};
 vm.runInNewContext(readFileSync(new URL('../web/startup-update.js',import.meta.url),'utf8'),{
  document,window,Event,setTimeout,clearTimeout,console,api,desktopPage:true,appearanceSave:Promise.resolve(),
  post:async()=>({id:'job',status:'ready'}),remove:async()=>{},
  native:()=>({InstallUpdate(){installs++;return new Promise((resolve,reject)=>{rejectInstall=reject})}})
 });
 document.dispatchEvent(new Event('DOMContentLoaded'));await tick();
 return {window,dialog,get installs(){return installs},get unsubscribed(){return unsubscribed},progress:message=>listener(message),fail:()=>rejectInstall(Error('更新助手准备超时'))};
}

const f=await fixture();
assert.match(f.dialog.querySelector('#startup-update-versions').textContent,/20260915042.*20260915043/);
const install=f.dialog.querySelector('#startup-update-install'),later=f.dialog.querySelector('#startup-update-later');
const progress=f.dialog.querySelector('#startup-update-progress'),bar=progress.querySelector('progress');
const pending=install.onclick();await tick();
assert.equal(f.installs,1);assert.equal(install.disabled,true);assert.equal(later.disabled,true);
assert.equal(bar.value,undefined,'preparation must not display a completed installation');
f.progress('正在校验更新文件…');assert.equal(progress.querySelector('p').textContent,'正在校验更新文件…');
f.fail();await pending;
assert.equal(install.disabled,false);assert.equal(later.disabled,false);assert.equal(bar.hidden,true);
assert.match(progress.querySelector('p').textContent,/安装未开始/);
assert.equal(f.unsubscribed,1);
const retry=install.onclick();await tick();assert.equal(f.installs,2);assert.equal(bar.hidden,false);
f.fail();await retry;assert.equal(f.unsubscribed,2);
later.onclick();await f.window.DengShellStartupReady;
console.log('Startup update: build labels, preparation progress, failure recovery, retry and event cleanup passed.');
