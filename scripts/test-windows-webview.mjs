// Runs against the actual release WebView2, with a disposable configuration.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
const [port, output]=process.argv.slice(2);if(!port||!output)throw Error('Usage: node test-windows-webview.mjs PORT OUTPUT');
const pause=ms=>new Promise(r=>setTimeout(r,ms));
let target;
for(let n=0;n<60;n++){try{const targets=await(await fetch(`http://127.0.0.1:${port}/json/list`)).json();target=targets.find(t=>t.type==='page'&&t.url.includes('wails.localhost'));if(target)break;}catch{}await pause(500);}
assert.ok(target,'native WebView2 debug target unavailable');
const ws=new WebSocket(target.webSocketDebuggerUrl),jobs=new Map(),errors=[];let sequence=0,sshRequests=0;
await new Promise((resolve,reject)=>{ws.onopen=resolve;ws.onerror=reject;});
ws.onmessage=event=>{const m=JSON.parse(event.data);if(m.id){const job=jobs.get(m.id);if(job){jobs.delete(m.id);m.error?job.reject(Error(JSON.stringify(m.error))):job.resolve(m.result);}}if(m.method==='Runtime.exceptionThrown')errors.push(m.params.exceptionDetails.text);if(m.method==='Network.requestWillBeSent'&&m.params.request.url.endsWith('/api/sessions')&&m.params.request.method==='POST')sshRequests++;};
const cdp=(method,params={})=>new Promise((resolve,reject)=>{const id=++sequence;jobs.set(id,{resolve,reject});ws.send(JSON.stringify({id,method,params}));});
const evaluate=async expression=>{const result=await cdp('Runtime.evaluate',{expression,awaitPromise:true,returnByValue:true});if(result.exceptionDetails)throw Error(JSON.stringify(result.exceptionDetails));return result.result.value;};
const check=async(expression)=>{for(let n=0;n<60;n++){if(await evaluate(expression))return;await pause(100);}throw Error(`Condition failed: ${expression}`);};
try{
 await cdp('Runtime.enable');await cdp('Network.enable');await check('typeof serverManager!=="undefined" && serverManager.initialized');
 await evaluate(`(async()=>{for(const d of document.querySelectorAll('dialog[open]'))d.close();const root=await post('/api/group-nodes',{name:'QA 一级目录'}),child=await post('/api/group-nodes',{name:'二级目录',parentId:root.id}),deep=await post('/api/group-nodes',{name:'三级目录',parentId:child.id});for(const [name,groupId,host] of [['主连接',root.id,'192.0.2.10'],['深层连接',deep.id,'192.0.2.11']])await post('/api/profiles',{name,groupId,host,user:'root',auth:'password',secret:'local-qa-only'});await loadProfiles();window.__qaFolders={root:root.id,child:child.id,deep:deep.id};setDrawer(true);chooseServerFolder(root.id)})()`);
 await check('document.querySelectorAll(".server-profile-row").length===2');
 await evaluate('document.querySelector("#server-include-children").click()');await check('document.querySelectorAll(".server-profile-row").length===1');
 await evaluate('chooseServerFolder(__qaFolders.deep)');await check('document.querySelector("#server-explorer-level").textContent==="3级"');
 const clickPoint=await evaluate('(()=>{const r=document.querySelector(".connection-card").getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}})()');
 await cdp('Input.dispatchMouseEvent',{type:'mousePressed',button:'left',clickCount:1,...clickPoint});await cdp('Input.dispatchMouseEvent',{type:'mouseReleased',button:'left',clickCount:1,...clickPoint});
 await check('document.querySelectorAll(".server-multi-selected").length===1');assert.equal(sshRequests,0,'single click initiated SSH');
 await evaluate('document.querySelector(".server-profile-more").click()');assert.deepEqual(await evaluate('[...document.querySelectorAll("#server-group-menu button")].map(b=>b.textContent)'),['连接','编辑','删除','定位所属分组','复制地址']);
 await evaluate('closeServerGroupMenu();serverManager.treeWidth=360;saveServerExplorer();document.documentElement.dataset.theme="light";fitServerExplorer()');
 await evaluate('window.DengPortablePreferences.flush()');
 for(const theme of ['light','dark']){await evaluate(`document.documentElement.dataset.theme=${JSON.stringify(theme)}`);await pause(250);const shot=await cdp('Page.captureScreenshot',{format:'png'});fs.writeFileSync(path.join(output,`windows-native-manager-${theme}.png`),Buffer.from(shot.data,'base64'));}
 await cdp('Page.reload');await check('typeof serverManager!=="undefined" && serverManager.initialized');assert.equal(await evaluate('serverManager.treeWidth'),360);assert.equal(await evaluate('serverManager.includeChildren'),false);
 assert.deepEqual(errors,[]);fs.writeFileSync(path.join(output,'windows-webview-validation.json'),JSON.stringify({passed:true,nativeWebView2:true,directorySelection:true,descendantFilter:true,clickWithoutSSH:true,contextMenu:true,persistedLayout:true,lightAndDarkScreenshots:true,uncaughtErrors:errors,actualSSHRequests:sshRequests},null,2));
 console.log('PASS: actual Windows WebView2 directory navigation, selection, context menu, saved layout and both themes.');
}finally{ws.close();}
