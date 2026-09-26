// Exercise the installed debug APK through its real Android WebView CDP target.
const fs = require('fs');
const port = Number(process.env.CDP_PORT || 9223);
const stage = process.argv[2] || 'status';
const profileName = 'Android 回归临时服务器';
const keyProfileName = 'Android 私钥回归临时服务器';
const keyName = 'Android 实机回归测试密钥 2026-09-25';
const pickerKeyName = 'Android 选择器回归临时密钥 2026-09-25';

(async () => {
  const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
  const target = targets.find(item => item.type === 'page' && item.url.startsWith('http://127.0.0.1:'));
  if (!target) throw new Error('DengShell WebView page not found');
  const socket = new WebSocket(target.webSocketDebuggerUrl);
  const pending = new Map();
  let nextId = 1;
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('CDP connection timed out')), 15000);
    socket.addEventListener('open', () => { clearTimeout(timer); resolve(); }, {once:true});
    socket.addEventListener('error', error => { clearTimeout(timer); reject(error); }, {once:true});
  });
  socket.addEventListener('message', event => {
    const message = JSON.parse(event.data);
    if (!message.id) return;
    const callback = pending.get(message.id);
    if (callback) { pending.delete(message.id); callback(message); }
  });
  function send(method, params = {}) {
    return new Promise((resolve, reject) => {
      const id = nextId++;
      const timer = setTimeout(() => { pending.delete(id); reject(new Error(`${method} timed out`)); }, 20000);
      pending.set(id, reply => {
        clearTimeout(timer);
        reply.error ? reject(new Error(reply.error.message)) : resolve(reply.result);
      });
      socket.send(JSON.stringify({id, method, params}));
    });
  }
  async function evaluate(expression) {
    const reply = await send('Runtime.evaluate', {expression, returnByValue:true, awaitPromise:true});
    if (reply.exceptionDetails) throw new Error(reply.exceptionDetails.text);
    return reply.result.value;
  }
  const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
  await send('Runtime.enable');

  if (stage === 'openKeyPicker') {
    const result=await evaluate(`(() => {
      editKey('import');
      const button=document.querySelector('.mobile-key-file-picker button');
      if(!button)return {error:'Android key picker button missing'};
      button.click();
      return {started:true};
    })()`);
    console.log('openKeyPicker',JSON.stringify(result));
    await pause(250);
  } else if (stage === 'savePickerKey') {
    const result=await evaluate(`(() => {
      const form=document.getElementById('key-editor-form');
      const status=document.querySelector('.mobile-key-file-picker [role=status]')?.textContent||'';
      if(!form.parentElement.open||!status.includes('dengshell-test-key-20260925')||!form.elements.privateKey.value.includes('PRIVATE KEY'))return {error:'key picker did not load fixture key',status,dialogOpen:form.parentElement.open};
      form.elements.name.value=${JSON.stringify(pickerKeyName)};
      form.requestSubmit();
      return {submitted:true,status};
    })()`);
    console.log('savePickerKey',JSON.stringify(result));
    await pause(800);
    console.log('pickerKeySaved',JSON.stringify(await evaluate(`(() => ({present:managedKeys.some(item=>item.name===${JSON.stringify(pickerKeyName)})}))()`)));
  } else if (stage === 'deletePickerKey') {
    const result=await evaluate(`(async () => {
      const matches=managedKeys.filter(item=>item.name===${JSON.stringify(pickerKeyName)});
      if(matches.length!==1)return {error:'expected exactly one synthetic picker key',matches:matches.length};
      await remove('/api/keys/'+encodeURIComponent(matches[0].id));
      await loadProfiles();
      return {removed:true,remaining:managedKeys.filter(item=>item.name===${JSON.stringify(pickerKeyName)}).length};
    })()`);
    console.log('deletePickerKey',JSON.stringify(result));
    await pause(150);
  } else if (stage === 'importKey') {
    const keyPath=process.env.FIXTURE_PRIVATE_KEY_PATH;
    if(!keyPath)throw new Error('FIXTURE_PRIVATE_KEY_PATH is required');
    const privateKey=fs.readFileSync(keyPath,'utf8');
    if(!privateKey.includes('PRIVATE KEY'))throw new Error('fixture key is not a private key');
    const result=await evaluate(`(() => {
      if(managedKeys.some(item=>item.name===${JSON.stringify(keyName)}))return {error:'synthetic key already present'};
      editKey('import');
      const form=document.getElementById('key-editor-form');
      form.elements.name.value=${JSON.stringify(keyName)};
      form.elements.privateKey.value=${JSON.stringify(privateKey)};
      const valid=form.checkValidity();
      if(valid)form.requestSubmit();
      return {valid};
    })()`);
    console.log('importKey',JSON.stringify(result));
    await pause(850);
    console.log('importedKey',JSON.stringify(await evaluate(`(() => {
      const key=managedKeys.find(item=>item.name===${JSON.stringify(keyName)});
      return {present:!!key,id:key?.id,dialogOpen:document.getElementById('key-editor-dialog').open};
    })()`)));
  } else if (stage === 'saveKeyProfile') {
    const fixturePort=Number(process.env.FIXTURE_PORT);
    const fixtureUser=process.env.FIXTURE_USER;
    if(!fixturePort||!fixtureUser)throw new Error('FIXTURE_PORT and FIXTURE_USER are required');
    const result=await evaluate(`(() => {
      const key=managedKeys.find(item=>item.name===${JSON.stringify(keyName)});
      if(!key)return {error:'synthetic key missing'};
      if(profiles.some(item=>item.name===${JSON.stringify(keyProfileName)}))return {error:'synthetic key profile already present'};
      showConnectionForm();
      const form=document.getElementById('connection-form');
      for(const [name,value] of Object.entries({name:${JSON.stringify(keyProfileName)},host:'127.0.0.1',port:${JSON.stringify(String(fixturePort))},user:${JSON.stringify(fixtureUser)},auth:'key',keyId:key.id})){
        form.elements[name].value=value;
        form.elements[name].dispatchEvent(new Event('change',{bubbles:true}));
      }
      updateAuthFields();
      const valid=form.checkValidity();
      if(valid)form.requestSubmit();
      return {valid,keyId:key.id,invalid:[...form.elements].filter(field=>field.willValidate&&!field.checkValidity()).map(field=>field.name)};
    })()`);
    console.log('saveKeyProfile',JSON.stringify(result));
    await pause(1000);
  } else if (stage === 'cleanupKeyProfile') {
    const fixturePort=Number(process.env.FIXTURE_PORT);
    if(!fixturePort)throw new Error('FIXTURE_PORT is required');
    const result=await evaluate(`(async () => {
      const key=managedKeys.find(item=>item.name===${JSON.stringify(keyName)});
      const matches=profiles.filter(item=>item.name===${JSON.stringify(keyProfileName)}&&item.host==='127.0.0.1'&&Number(item.port)===${JSON.stringify(fixturePort)});
      if(matches.length!==1||!key||matches[0].keyId!==key.id)return {error:'synthetic key/profile pair mismatch',profiles:matches.length,keyPresent:!!key};
      const profile=matches[0];
      for(const session of [...sessions.values()].filter(item=>item.profileId===profile.id))await closeSession(session.id);
      await remove('/api/profiles/'+encodeURIComponent(profile.id));
      await remove('/api/trash/'+encodeURIComponent(profile.id));
      await remove('/api/keys/'+encodeURIComponent(key.id));
      credentials.delete(profile.id);
      save('dengshell.history.'+profile.id,undefined);
      save('dengshell.nic.'+profile.id,undefined);
      await loadProfiles();
      return {removed:true,profilePresent:profiles.some(item=>item.id===profile.id),keyPresent:managedKeys.some(item=>item.id===key.id),sessions:[...sessions.values()].filter(item=>item.profileId===profile.id).length};
    })()`);
    console.log('cleanupKeyProfile',JSON.stringify(result));
    await pause(200);
  } else if (stage === 'save') {
    const secret = process.env.FIXTURE_PASSWORD;
    if (!secret) throw new Error('FIXTURE_PASSWORD is required');
    const result = await evaluate(`(() => {
      const form=document.getElementById('connection-form');
      if (!document.getElementById('connection-dialog').open) return {error:'connection dialog is closed'};
      const values={name:${JSON.stringify(profileName)},host:'127.0.0.1',port:'22999',user:'fixture',auth:'password',secret:${JSON.stringify(secret)}};
      for (const [name,value] of Object.entries(values)) {
        const field=form.elements[name]; field.value=value;
        field.dispatchEvent(new Event('input',{bubbles:true}));
        field.dispatchEvent(new Event('change',{bubbles:true}));
      }
      const valid=form.checkValidity();
      if (valid) form.requestSubmit();
      return {valid,invalid:[...form.elements].filter(field=>field.willValidate&&!field.checkValidity()).map(field=>field.name)};
    })()`);
    console.log('save', JSON.stringify(result));
    await pause(1600);
  } else if (stage === 'approve') {
    const expected = process.env.FIXTURE_FINGERPRINT;
    if (!expected) throw new Error('FIXTURE_FINGERPRINT is required');
    const result = await evaluate(`(() => {
      const dialog=document.getElementById('action-dialog');
      const description=document.getElementById('action-description').textContent;
      if (!dialog.open || !description.includes(${JSON.stringify(expected)})) return {approved:false,reason:'fingerprint mismatch or dialog closed',description};
      document.getElementById('action-confirm').click();
      return {approved:true};
    })()`);
    console.log('approve', JSON.stringify(result));
    await pause(1300);
  } else if (stage === 'resetKey') {
    const result = await evaluate(`(() => {
      const profile=profiles.find(item=>item.name===${JSON.stringify(profileName)}&&item.host==='127.0.0.1'&&Number(item.port)===22999);
      if (!profile) return {error:'test profile missing'};
      showConnectionForm(profile);
      document.getElementById('reset-host-key').click();
      return {profileFound:true,formOpen:document.getElementById('connection-dialog').open};
    })()`);
    console.log('resetKey', JSON.stringify(result));
    await pause(300);
  } else if (stage === 'confirmReset') {
    const result = await evaluate(`(() => {
      const dialog=document.getElementById('action-dialog');
      const title=dialog.querySelector('h2')?.textContent||'';
      if (!dialog.open||!title.includes('重置主机指纹')) return {confirmed:false,title};
      document.getElementById('action-confirm').click();
      return {confirmed:true};
    })()`);
    console.log('confirmReset', JSON.stringify(result));
    await pause(300);
  } else if (stage === 'reconnect') {
    const result = await evaluate(`(() => {
      document.getElementById('connection-dialog').close();
      const profile=profiles.find(item=>item.name===${JSON.stringify(profileName)}&&item.host==='127.0.0.1'&&Number(item.port)===22999);
      if (!profile) return {error:'test profile missing'};
      connect(profile.id);
      return {connecting:true};
    })()`);
    console.log('reconnect', JSON.stringify(result));
    await pause(1300);
  } else if (stage === 'provideSecret') {
    const secret=process.env.FIXTURE_PASSWORD;
    if(!secret)throw new Error('FIXTURE_PASSWORD is required');
    const result=await evaluate(`(() => {
      const dialog=document.getElementById('action-dialog'),title=document.getElementById('action-title').textContent,description=document.getElementById('action-description').textContent;
      if(!dialog.open||!title.includes('连接 Android 回归临时服务器')||!description.includes('fixture@127.0.0.1:22999'))return {submitted:false,title,description};
      document.getElementById('action-input').value=${JSON.stringify(secret)};
      document.getElementById('action-form').requestSubmit();
      return {submitted:true};
    })()`);
    console.log('provideSecret',JSON.stringify(result));
    await pause(1100);
  } else if (stage === 'reconnectWithSecret') {
    const secret=process.env.FIXTURE_PASSWORD;
    if(!secret)throw new Error('FIXTURE_PASSWORD is required');
    const result=await evaluate(`(() => {
      const profile=profiles.find(item=>item.name===${JSON.stringify(profileName)}&&item.host==='127.0.0.1'&&Number(item.port)===22999);
      if(!profile)return {error:'test profile missing'};
      credentials.set(profile.id,${JSON.stringify(secret)});
      connect(profile.id);
      return {connecting:true};
    })()`);
    console.log('reconnectWithSecret',JSON.stringify(result));
    await pause(1000);
  } else if (stage === 'terminal') {
    const result = await evaluate(`(() => {
      document.querySelector('#mobile-navigation [data-mobile-target="terminal"]').click();
      const input=document.getElementById('command-input');
      input.value='echo ANDROID_REGRESSION_OK';
      input.dispatchEvent(new Event('input',{bubbles:true}));
      document.getElementById('command-form').requestSubmit();
      return {sent:true};
    })()`);
    console.log('terminal', JSON.stringify(result));
    await pause(900);
  } else if (stage === 'files') {
    await evaluate(`document.querySelector('#mobile-navigation [data-mobile-target="files"]').click()`);
    await pause(900);
  } else if (stage === 'pickUpload') {
    const result=await evaluate(`(() => { document.querySelector('#mobile-navigation [data-mobile-target="files"]').click(); const button=document.getElementById('choose-files'); const r=button.getBoundingClientRect(); button.click(); return {disabled:button.disabled,rect:{x:r.x,y:r.y,width:r.width,height:r.height}}; })()`);
    console.log('pickUpload',JSON.stringify(result));
    await pause(300);
  } else if (stage === 'selectDownload') {
    const name=process.env.FIXTURE_DOWNLOAD_NAME||'welcome.txt';
    const result=await evaluate(`(async () => { document.querySelector('#mobile-navigation [data-mobile-target="files"]').click(); document.getElementById('refresh-files').click(); await new Promise(resolve=>setTimeout(resolve,450)); const row=[...document.querySelectorAll('#file-list tr')].find(item=>item.dataset.fileName===${JSON.stringify(name)}); if(!row)return {error:${JSON.stringify(name)}+' missing'}; row.click(); const button=document.getElementById('download-file'); const r=button.getBoundingClientRect(); return {selected:row.classList.contains('selected'),disabled:button.disabled,rect:{x:r.x,y:r.y,width:r.width,height:r.height}}; })()`);
    console.log('selectDownload',JSON.stringify(result));
    await pause(300);
  } else if (stage === 'beginDownload') {
    const name=process.env.FIXTURE_DOWNLOAD_NAME;
    if(!name)throw new Error('FIXTURE_DOWNLOAD_NAME is required');
    const result=await evaluate(`(() => {
      const row=[...document.querySelectorAll('#file-list tr')].find(item=>item.dataset.fileName===${JSON.stringify(name)});
      if(!row)return {error:'requested fixture file missing'};
      row.click();
      const button=document.getElementById('download-file');
      if(button.disabled)return {error:'download action disabled'};
      button.click();
      return {started:true,name:${JSON.stringify(name)}};
    })()`);
    console.log('beginDownload',JSON.stringify(result));
    await pause(300);
  } else if (stage === 'renameTest') {
    const result=await evaluate(`(async () => {
      const source='dengshell-android-regression.txt',target='dengshell-android-renamed.txt';
      const row=[...document.querySelectorAll('#file-list tr')].find(item=>item.dataset.fileName===source);
      if(!row)return {error:'temporary source file missing'};
      row.click(); document.getElementById('rename-file').click();
      await new Promise(resolve=>setTimeout(resolve,100));
      const dialog=document.getElementById('action-dialog');
      if(!dialog.open||!document.getElementById('action-title').textContent.includes('重命名'))return {error:'rename dialog missing'};
      document.getElementById('action-input').value=target;
      document.getElementById('action-form').requestSubmit();
      return {submitted:true,target};
    })()`);
    console.log('renameTest',JSON.stringify(result));
    await pause(1000);
  } else if (stage === 'openRename') {
    const result=await evaluate(`(async () => {
      document.querySelector('#mobile-navigation [data-mobile-target="files"]').click();
      const row=[...document.querySelectorAll('#file-list tr')].find(item=>item.dataset.fileName==='regression.unknownext');
      if(!row)return {error:'temporary file missing'};
      row.click(); document.getElementById('rename-file').click();
      await new Promise(resolve=>setTimeout(resolve,100));
      return {dialogOpen:document.getElementById('action-dialog').open,title:document.getElementById('action-title').textContent};
    })()`);
    console.log('openRename',JSON.stringify(result));
    await pause(200);
  } else if (stage === 'openDelete') {
    const result=await evaluate(`(async () => {
      const row=[...document.querySelectorAll('#file-list tr')].find(item=>item.dataset.fileName==='regression.unknownext');
      if(!row)return {error:'temporary file missing'};
      row.click(); document.getElementById('delete-file').click();
      await new Promise(resolve=>setTimeout(resolve,100));
      return {dialogOpen:document.getElementById('action-dialog').open,title:document.getElementById('action-title').textContent,description:document.getElementById('action-description').textContent};
    })()`);
    console.log('openDelete',JSON.stringify(result));
    await pause(200);
  } else if (stage === 'deleteTest') {
    const result=await evaluate(`(async () => {
      const name='dengshell-android-renamed.txt';
      const row=[...document.querySelectorAll('#file-list tr')].find(item=>item.dataset.fileName===name);
      if(!row)return {error:'temporary renamed file missing'};
      row.click(); document.getElementById('delete-file').click();
      await new Promise(resolve=>setTimeout(resolve,100));
      const dialog=document.getElementById('action-dialog'),description=document.getElementById('action-description').textContent;
      if(!dialog.open||!description.includes(name))return {error:'delete confirmation missing or target mismatch',description};
      document.getElementById('action-confirm').click();
      return {confirmed:true,name};
    })()`);
    console.log('deleteTest',JSON.stringify(result));
    await pause(1000);
  } else if (stage === 'deleteUnknown') {
    const result=await evaluate(`(async () => {
      const name='regression.unknownext';
      document.querySelector('#mobile-navigation [data-mobile-target="files"]').click();
      document.getElementById('refresh-files').click();
      await new Promise(resolve=>setTimeout(resolve,500));
      const row=[...document.querySelectorAll('#file-list tr')].find(item=>item.dataset.fileName===name);
      if(!row)return {error:'temporary file missing'};
      row.click(); document.getElementById('delete-file').click();
      await new Promise(resolve=>setTimeout(resolve,100));
      const dialog=document.getElementById('action-dialog'),description=document.getElementById('action-description').textContent;
      if(!dialog.open||!description.includes(name))return {error:'delete confirmation missing or target mismatch',description};
      document.getElementById('action-confirm').click();
      return {confirmed:true,name};
    })()`);
    console.log('deleteUnknown',JSON.stringify(result));
    await pause(800);
  } else if (stage === 'cleanupProfile') {
    const result=await evaluate(`(async () => {
      const matches=profiles.filter(item=>item.name===${JSON.stringify(profileName)}&&item.host==='127.0.0.1'&&Number(item.port)===22999);
      if(matches.length!==1)return {error:'expected exactly one synthetic test profile',matches:matches.length};
      const profile=matches[0];
      for(const session of [...sessions.values()].filter(item=>item.profileId===profile.id))await closeSession(session.id);
      await remove('/api/profiles/'+encodeURIComponent(profile.id));
      await remove('/api/trash/'+encodeURIComponent(profile.id));
      credentials.delete(profile.id);
      save('dengshell.history.'+profile.id,undefined);
      save('dengshell.nic.'+profile.id,undefined);
      await loadProfiles();
      return {removed:true,remaining:profiles.filter(item=>item.id===profile.id).length,sessions:[...sessions.values()].filter(item=>item.profileId===profile.id).length};
    })()`);
    console.log('cleanupProfile',JSON.stringify(result));
    await pause(300);
  } else if (stage === 'monitor') {
    await evaluate(`document.querySelector('#mobile-navigation [data-mobile-target="monitor"]').click()`);
    await pause(900);
  } else if (stage === 'keyboard') {
    await evaluate(`(() => { document.querySelector('#mobile-navigation [data-mobile-target="terminal"]').click(); document.getElementById('command-input').focus(); })()`);
    await pause(900);
  } else if (stage === 'fixscroll') {
    await evaluate(`(() => { const style=document.createElement('style'); style.id='regression-scroll-fix'; style.textContent='@media (max-width: 760px) { #terminal-output .xterm-helper-textarea { left: 0 !important; } }'; document.head.append(style); const output=document.getElementById('terminal-output'); output.scrollLeft=0; document.getElementById('command-input').focus(); })()`);
    await pause(900);
  }

  const status = await evaluate(`(() => {
    const rect=selector=>{const element=document.querySelector(selector);if(!element)return null;const r=element.getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,height:r.height}};
    const active=current(),buffer=active?.term?.buffer.active;
    return {
      title:document.title,width:innerWidth,height:innerHeight,scrollWidth:document.documentElement.scrollWidth,
      viewport:{offsetLeft:visualViewport?.offsetLeft,pageLeft:visualViewport?.pageLeft,scale:visualViewport?.scale,scrollX,rootScrollLeft:document.scrollingElement?.scrollLeft,activeElement:document.activeElement?.id},
      view:document.body.dataset.mobileView,drawerOpen:!document.getElementById('connections-drawer').hidden,
      dialogs:[...document.querySelectorAll('dialog[open]')].map(dialog=>({id:dialog.id,title:dialog.querySelector('h2')?.textContent,description:dialog.querySelector('p')?.textContent?.slice(0,250)})),
      sessions:[...document.querySelectorAll('#session-tabs .session-tab')].map(tab=>tab.textContent.trim()),
      terminalState:document.getElementById('terminal-state').textContent,
      terminalText:document.getElementById('terminal-output').textContent.slice(-500),
      terminalRows:[...document.querySelectorAll('#terminal-output .xterm-rows > div')].map(row=>row.textContent).filter(Boolean).slice(-18),
      activeTerminal:{ready:active?.ready,connected:active?.connected,closed:active?.closed,wsState:active?.ws?.readyState,cols:active?.term?.cols,rows:active?.term?.rows,lines:buffer?Array.from({length:Math.min(24,buffer.length)},(_,index)=>buffer.getLine(buffer.length-Math.min(24,buffer.length)+index)?.translateToString(true)).filter(Boolean):[]},
      terminalGeometry:{host:rect('.terminal-session:not([hidden])'),screen:rect('.terminal-session:not([hidden]) .xterm-screen'),rows:rect('.terminal-session:not([hidden]) .xterm-rows')},
      files:[...document.querySelectorAll('#file-list tr')].map(row=>row.textContent.trim()).slice(0,12),
      monitor:document.getElementById('monitor-state').textContent,
      monitorTitle:document.getElementById('monitor-state').title,
      toast:document.querySelector('.toast')?.textContent?.slice(0,200),
      header:rect('.titlebar'),nav:rect('#mobile-navigation'),command:rect('.terminal-command-bar'),
      boxes:Object.fromEntries(['#terminal-output','.terminal-panel','.workspace','.main-panel','.app-shell','body'].map(selector=>[selector,{rect:rect(selector),scrollLeft:document.querySelector(selector)?.scrollLeft,clientWidth:document.querySelector(selector)?.clientWidth,scrollWidth:document.querySelector(selector)?.scrollWidth}]))
    };
  })()`);
  console.log(JSON.stringify(status,null,2));
  socket.close();
})().catch(error => { console.error(error); process.exit(1); });
