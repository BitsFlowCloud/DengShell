/* Built-in utilities are planned first and submitted only to the chosen live terminal. */
(() => {
  'use strict';
  const scripts = Object.freeze({
    nq: { name:'NQ测试脚本', command:'bash <(curl -sL https://run.NodeQuality.com)' },
    yabs: { name:'YABS测试脚本', command:'curl -sL https://yabs.sh | bash' },
    speed: { name:'国际测速', command:'curl -sL nws.sh | bash' },
  });
  const tools = [
    ['bbr','BBR优化','blue','network'], ['clean','清理垃圾','green','erase'],
    ['nq','NQ测试脚本','purple','code'], ['yabs','YABS测试脚本','teal','cpu'],
    ['speed','国际测速','amber','network'], ['swap','开启/关闭SWAP','rose','disk'],
  ];
  const dialog = node('dialog','common-app-dialog'); dialog.id='common-app-dialog';
  dialog.setAttribute('aria-labelledby','common-app-title');
  dialog.innerHTML='<div class="dialog-heading"><div><p class="common-app-eyebrow">常用应用</p><h2 id="common-app-title"></h2></div><button type="button" class="icon-button" id="common-app-close" aria-label="关闭常用应用窗口"></button></div><p id="common-app-server" class="common-app-server"></p><p id="common-app-shell-note" class="common-app-shell-note" hidden>未检测到 Shell 提示符集成，确认前请先确保终端处于空闲命令提示符。</p><div id="common-app-body" class="common-app-body"></div><p id="common-app-error" class="common-app-error" role="alert" hidden></p><div class="dialog-buttons"><button type="button" id="common-app-cancel">取消</button><button type="button" id="common-app-confirm" class="primary-button" hidden>确认</button></div>';
  document.body.append(dialog); $('#common-app-close').append(icon('close'));
  const body=$('#common-app-body'), confirm=$('#common-app-confirm');
  let view=null, serial=0;
  const button=(text,handler,className='')=>{const el=node('button',className,text);el.type='button';el.onclick=handler;return el;};
  function close() { const old=view;view=null;serial++;if(dialog.open)dialog.close();old?.returnFocus?.focus({preventScroll:true}); }
  $('#common-app-close').onclick=close;$('#common-app-cancel').onclick=close;
  dialog.addEventListener('cancel',event=>{event.preventDefault();close();});
  dialog.addEventListener('close',()=>{if(!dialog.open)view=null;});
  function error(message) { const el=$('#common-app-error');el.textContent=message;el.hidden=!message; }
  function valid(state) { return !!state?.connected && !!state.ready && state.ws?.readyState===WebSocket.OPEN; }
  function currentView(owner) { return view===owner && dialog.open; }
  function guard(owner) {
    if(!currentView(owner))return null;
    const state=sessions.get(owner.sessionID);
    if(activeID!==owner.sessionID){error('当前会话已切换，请关闭此窗口，在目标服务器中重新选择应用。');return null;}
    if(!valid(state)){error('此 SSH 连接已断开，请重新连接后再执行。');return null;}
    if(state.shellIntegration?.ready && !state.shellIntegration.atPrompt){error('终端正在运行命令或还有未提交的输入。请先返回空闲提示符，再执行此应用。');return null;}
    return state;
  }
  function busy(owner,on) {
    if(!currentView(owner))return;
    owner.busy=on;dialog.setAttribute('aria-busy',String(on));
    body.querySelectorAll('button,input').forEach(el=>el.disabled=on);confirm.disabled=on;
  }
  function page(owner,title) {
    if(!currentView(owner))return false;
    $('#common-app-title').textContent=title;body.replaceChildren();error('');confirm.hidden=true;confirm.onclick=null;busy(owner,false);return true;
  }
  function paragraph(text,className='') { const p=node('p',className,text);body.append(p);return p; }
  function details(items) { if(!items?.length)return;const list=node('ul','common-app-details');list.append(...items.map(text=>node('li','',text)));body.append(list); }
  function action(text,handler) { confirm.hidden=false;confirm.textContent=text;confirm.onclick=handler; }
  function submit(owner,command) {
    const state=guard(owner);if(!state)return false;
    if(typeof command!=='string'||!command.trim()||command.includes('\0')){error('没有可执行的命令，请重新打开此应用。');return false;}
    owner.sent=true;
    close();
    pasteTerminalText(state,command,{execute:true});
    toast('命令已发送到 '+(profileFor(state)?.name||'当前 SSH 终端'));
    return true;
  }
  async function plan(owner,kind,extra={}) {
    if(!currentView(owner)||owner.busy)return null;
    // Reading a plan never runs the utility, even if the user closes the dialog.
    if(activeID!==owner.sessionID||!valid(sessions.get(owner.sessionID))){error('当前连接已变化，请在目标服务器中重新打开此应用。');return null;}
    error('');busy(owner,true);
    try {
      const result=await post(`/api/sessions/${owner.sessionID}/utilities/plan`,{kind,...extra});
      if(!currentView(owner))return null;
      if(result.sessionId!==owner.sessionID || result.kind!==kind)throw new Error('执行方案与当前会话不匹配，请重新打开。');
      return result;
    } catch(err) { if(currentView(owner))error(err.message||'无法读取服务器状态，请重试。');return null; }
    finally { busy(owner,false); }
  }
  async function runPlan(owner,kind,extra={}) {
    const result=await plan(owner,kind,extra);if(!result)return;
    if(result.blockedReason){error(result.blockedReason);return;}
    submit(owner,result.command);
  }
  function thirdParty(owner,key,traffic=false) {
    const script=scripts[key];if(!page(owner,script.name+(traffic?' · 流量确认':'')))return;
    if(traffic){
      paragraph('该脚本会消耗大量流量（大约 20～50 GB）。','common-app-emphasis');
      paragraph('实际用量取决于带宽和测试节点，可能超过上述范围。');
      paragraph('请确认是否继续执行？');
    }else{
      paragraph('该脚本为第三方测试脚本。');paragraph('将执行命令：');
      const pre=node('pre','common-app-command');pre.tabIndex=0;pre.setAttribute('aria-label','将执行的命令');pre.append(node('code','',script.command));body.append(pre);
      paragraph('请确定是否执行？');
    }
    action('确认',()=>{if(key==='speed'&&!traffic){if(guard(owner))thirdParty(owner,key,true);}else submit(owner,script.command);});
    if(traffic){confirm.disabled=true;setTimeout(()=>{if(currentView(owner))confirm.disabled=false;},400);}
    $('#common-app-cancel').focus();
  }
  function bbr(owner) {
    if(!page(owner,'BBR优化'))return;
    paragraph('选择调优取向，点击对应方案后在当前 SSH 终端执行。');
    const grid=node('div','bbr-options');
    for(const [mode,title,tag,text] of [
      ['conservative','保守 BBR','延迟优先','较小的发送排队与缓冲预算，侧重控制排队延迟，保留内核重传恢复机制。'],
      ['balanced','平衡 BBR','均衡取向','兼顾吞吐量与排队延迟，采用适中的自动缓冲上限。'],
      ['aggressive','激进 BBR','吞吐优先','提供更大的自动缓冲预算，优先长距离链路吞吐量，可能增加内存占用与排队延迟。'],
    ]){
      const option=button('',()=>runPlan(owner,'bbr',{mode}),'bbr-option');option.dataset.bbrMode=mode;
      const heading=node('span','bbr-option-heading');heading.append(node('strong','',title),node('small','',tag));
      option.append(heading,node('span','bbr-option-text',text),icon('chevron'));grid.append(option);
    }
    body.append(grid);
    paragraph('三档使用当前内核提供的 BBR，具体收益受链路与内核影响，无法保证某档始终最低延迟或最低重传。不支持时会说明原因。','common-app-note');
  }
  async function clean(owner) {
    if(!page(owner,'清理垃圾'))return;
    paragraph('正在检查服务器可清理的缓存…','common-app-loading');
    const result=await plan(owner,'clean');if(!result){if(currentView(owner)){body.replaceChildren(button('重新检查',()=>clean(owner),'upload-button'));}return;}
    page(owner,'清理垃圾');
    paragraph(result.summary||'将清理以下系统缓存：');details(result.details);
    if(result.blockedReason){error(result.blockedReason);return;}
    paragraph('确认后将在当前 SSH 终端执行，并显示实际清理结果。','common-app-note');
    action('确认清理',()=>submit(owner,result.command));
  }
  function swapSize(owner,status) {
    if(!page(owner,'设置 SWAP'))return;
    if(status.hasSwap||status.rebootRequired){
      paragraph(status.hasSwap?'需要先删除当前 SWAP 并重启之后才能正确设置。':'SWAP 已关闭或删除。请按本工具的设置流程，重启服务器后再设置新的 SWAP。','common-app-emphasis');
      paragraph('DengShell 不会自动重启服务器。');
      const back=button('重新检查 SWAP',()=>swap(owner),'upload-button');body.append(back);return;
    }
    paragraph('当前没有启用或配置的 SWAP，请选择需要设置的大小。');details(status.details);
    const choices=node('div','swap-presets');
    const label=node('label','swap-input-label','自定义大小');label.htmlFor='swap-custom-size';const row=node('div','swap-size-row'),input=node('input');input.id='swap-custom-size';input.type='number';input.min='0.001';input.step='any';input.value='1';input.inputMode='decimal';input.required=true;
    let unit='GB';const units=node('div','swap-size-units');units.setAttribute('role','group');units.setAttribute('aria-label','SWAP 大小单位');
    const update=()=>{units.querySelectorAll('button').forEach(b=>b.setAttribute('aria-pressed',String(b.textContent===unit)));const mib=Number(input.value)*(unit==='GB'?1024:1);choices.querySelectorAll('button').forEach(b=>b.setAttribute('aria-pressed',String(Number(b.dataset.swapMib)===mib)));};
    for(const value of ['MB','GB'])units.append(button(value,()=>{unit=value;update();}));
    for(const [mib,text] of [[512,'512 MB'],[1024,'1 GB'],[2048,'2 GB'],[4096,'4 GB']]){const b=button(text,()=>{unit=mib<1024?'MB':'GB';input.value=String(mib/(unit==='GB'?1024:1));update();});b.dataset.swapMib=mib;choices.append(b);}
    input.oninput=update;row.append(input,units);body.append(choices,label,row);update();
    paragraph('按 1 GB = 1024 MB 换算。执行时再次检查现有 SWAP、磁盘空间和文件系统。','common-app-note');
    action('设置并执行',async()=>{
      const sizeMiB=Number(input.value)*(unit==='GB'?1024:1);
      if(!input.reportValidity()||!Number.isSafeInteger(sizeMiB)||sizeMiB<64||sizeMiB>1048576){error('请输入 64 MB～1024 GB 之间、可换算为整数 MB 的大小。');return;}
      await runPlan(owner,'swap-create',{sizeMiB});
    });
  }
  async function swap(owner) {
    if(!page(owner,'开启/关闭SWAP'))return;
    paragraph('正在检查当前 SWAP 与开机配置…','common-app-loading');
    const result=await plan(owner,'swap-status');if(!result){if(currentView(owner)){body.replaceChildren(button('重新检查',()=>swap(owner),'upload-button'));}return;}
    if(result.blockedReason){page(owner,'开启/关闭SWAP');error(result.blockedReason);return;}
    if(!result.hasSwap){swapSize(owner,result);return;}
    page(owner,'开启/关闭SWAP');paragraph('检测到以下 SWAP，请选择接下来的操作。');
    const list=node('div','swap-current-list');
    for(const entry of result.swapEntries||[]){
      const row=node('article','swap-current-row'),name=node('strong','',entry.path);name.title=entry.path;
      const kind={file:'交换文件',partition:'块设备 / 交换分区',zram:'ZRAM',configured:'待解析配置'}[entry.type]||entry.type;
      row.append(name,node('span','',`${kind} · ${entry.sizeMiB?`${Number(entry.sizeMiB).toLocaleString('zh-CN',{maximumFractionDigits:1})} MB · `:''}${entry.active?'已启用':'未启用'}${entry.configured?' · 开机配置':''}`));
      if(entry.sources?.length){const sources=node('small','swap-source-list','配置来源：'+entry.sources.join(' · '));sources.title=entry.sources.join('\n');row.append(sources);}
      if(entry.note)row.append(node('small','swap-entry-note',entry.note));list.append(row);
    }
    body.append(list);
    paragraph('关闭/删除会停用对应交换区并移除其开机配置。确认身份的交换文件会删除；分区和 zram 只关闭。无法核实身份的文件会保留并在终端说明。','common-app-note');
    const actions=node('div','swap-actions');actions.setAttribute('role','group');actions.setAttribute('aria-label','SWAP 操作');for(const [label,glyph,kind,handler] of [['删除 SWAP','erase','remove',()=>runPlan(owner,'swap-remove')],['设置 SWAP','settings','configure',()=>swapSize(owner,result)]]){const option=button('',handler,'swap-action');option.dataset.swapAction=kind;option.append(icon(glyph),node('span','',label));actions.append(option);}body.append(actions);
  }
  function open(kind) {
    const state=current();if(!valid(state)){toast('请先连接 SSH 服务器。');return;}
    if(view)close();
    const profile=profileFor(state);view={id:++serial,sessionID:state.id,busy:false,returnFocus:document.activeElement};
    $('#common-app-shell-note').hidden=!!state.shellIntegration?.ready;
    $('#common-app-server').textContent=`执行到 ${profile?.name||'当前会话'}${profile?.host?' · '+profile.host:''}`;
    dialog.showModal();const owner=view;
    if(scripts[kind])thirdParty(owner,kind);else if(kind==='bbr')bbr(owner);else if(kind==='clean')clean(owner);else if(kind==='swap')swap(owner);
  }
  function render() {
    const state=current(),ready=valid(state);$('#common-app-target').textContent=ready?`执行到 ${profileFor(state)?.name||'当前 SSH 终端'}`:'连接后选择应用';
    $('#common-app-list').querySelectorAll('button').forEach(b=>b.disabled=!ready);
  }
  $('#common-app-list').replaceChildren(...tools.map(([kind,name,color,glyph])=>{const b=button('',()=>open(kind),'common-app-pill');b.dataset.utility=kind;b.dataset.color=color;b.append(icon(glyph),node('span','',name));return b;}));
  window.DengCommonApps=Object.freeze({render,open});render();
})();
