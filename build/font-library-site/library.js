'use strict';
(() => {
  const $ = selector => document.querySelector(selector);
  const el = (tag, cls, text) => { const n=document.createElement(tag);if(cls)n.className=cls;if(text)n.textContent=text;return n; };
  let fonts=[],kind='ui-font',query='',downloadController;
  const size = bytes => bytes>=1048576?`${(bytes/1048576).toFixed(2)} MiB`:`${Math.ceil(bytes/1024)} KiB`;
  try {document.body.classList.toggle('dark',(localStorage.getItem('dengshell-site-theme') || (matchMedia('(prefers-color-scheme:dark)').matches?'dark':'light'))==='dark');}catch{}
  $('#theme').onclick=()=>{document.body.classList.toggle('dark');try{localStorage.setItem('dengshell-site-theme',document.body.classList.contains('dark')?'dark':'light');}catch{}};
  const localPath = (path, type) => typeof path==='string' && new RegExp(`^/fonts/${type}/[a-z0-9-]+\\.${type==='previews'?'png':'woff2'}$`).test(path);
  // Store each WOFF2 only once on the server. Create a licensed, uncompressed
  // ZIP after confirmation; WOFF2 is already compressed. No third-party script.
  const crcTable=Uint32Array.from({length:256},(_,n)=>{for(let i=0;i<8;i++)n=(n>>>1)^((n&1)?0xedb88320:0);return n>>>0;});
  function zip(files){
    const encoder=new TextEncoder(),parts=[],directory=[];let offset=0;
    for(const [name,body] of files){
      const filename=encoder.encode(name),data=typeof body==='string'?encoder.encode(body):body;
      let crc=0xffffffff;for(const byte of data)crc=crcTable[(crc^byte)&255]^(crc>>>8);crc=(crc^0xffffffff)>>>0;
      const local=new Uint8Array(30+filename.length),l=new DataView(local.buffer);
      l.setUint32(0,0x04034b50,true);l.setUint16(4,20,true);l.setUint16(6,0x800,true);l.setUint16(12,0x5d2e,true);l.setUint32(14,crc,true);l.setUint32(18,data.length,true);l.setUint32(22,data.length,true);l.setUint16(26,filename.length,true);local.set(filename,30);
      const central=new Uint8Array(46+filename.length),c=new DataView(central.buffer);
      c.setUint32(0,0x02014b50,true);c.setUint16(4,20,true);c.setUint16(6,20,true);c.setUint16(8,0x800,true);c.setUint16(14,0x5d2e,true);c.setUint32(16,crc,true);c.setUint32(20,data.length,true);c.setUint32(24,data.length,true);c.setUint16(28,filename.length,true);c.setUint32(42,offset,true);central.set(filename,46);
      parts.push(local,data);directory.push(central);offset+=local.length+data.length;
    }
    const end=new Uint8Array(22),e=new DataView(end.buffer);e.setUint32(0,0x06054b50,true);e.setUint16(8,files.length,true);e.setUint16(10,files.length,true);e.setUint32(12,directory.reduce((n,p)=>n+p.length,0),true);e.setUint32(16,offset,true);
    return new Blob([...parts,...directory,end],{type:'application/zip'});
  }
  async function download(font){
    const button=$('.file'),status=$('.download-status'),controller=new AbortController();downloadController=controller;
    button.disabled=true;button.textContent='正在下载…';status.textContent='正在下载并校验字体，可关闭此窗口取消。';
    try{
      const response=await fetch(font.file.path,{signal:controller.signal});if(!response.ok)throw new Error(`HTTP ${response.status}`);
      const data=new Uint8Array(await response.arrayBuffer());
      const checksum=[...new Uint8Array(await crypto.subtle.digest('SHA-256',data))].map(x=>x.toString(16).padStart(2,'0')).join('');
      if(data.length!==font.file.size || checksum!==font.file.sha256 || String.fromCharCode(...data.subarray(0,4))!=='wOF2')throw new Error('字体校验失败，请稍后重试');
      if(controller.signal.aborted)return;
      const note=`${font.name}\n来源：${font.project}\n版本：${font.version}\n许可：${font.licenseName}，完整版权与许可见 OFL.txt。\n${font.fallbackNote}\n\n解压后，在 DengShell 设置的${font.kind==='font'?'Shell 字体':'界面字体'}中导入 .woff2 文件。手动导入的界面字体需重启软件。\n\n格式适配版采用独立 Deng Library 内部家族名，保留上游版权；上游名称用于说明来源。\nSHA-256：${font.file.sha256}\n`;
      const url=URL.createObjectURL(zip([[font.id+'.woff2',data],['OFL.txt',font.licenseText],['README.txt',note]]));
      const link=el('a');link.href=url;link.download=`DengShell-${font.id}.zip`;document.body.append(link);link.click();link.remove();setTimeout(()=>URL.revokeObjectURL(url),60000);
      status.textContent='字体已准备好，请查看浏览器下载列表。ZIP 中附带完整许可证和导入说明。';
    }catch(error){if(!controller.signal.aborted)status.textContent=`下载未完成：${error.message}。请重试。`;}
    finally{if(downloadController===controller){downloadController=null;button.disabled=false;button.textContent='确认下载字体 ZIP';}}
  }
  $('#download').addEventListener('close',()=>downloadController?.abort());
  function open(font) {
    const dialog=$('#download');dialog.querySelector('h2').textContent=font.name;
    dialog.querySelector('.description').textContent=`${font.description} · ${size(font.file.size)} · ${font.licenseName}`;
    dialog.querySelector('.note').textContent=font.fallbackNote;
    const file=dialog.querySelector('.file');file.disabled=false;file.textContent='确认下载字体 ZIP';file.onclick=()=>download(font);
    dialog.querySelector('.download-status').textContent='ZIP 内含完整字体、版权许可证和导入说明。';
    const project=dialog.querySelector('.project');project.href=font.project;
    dialog.querySelector('pre').textContent=font.licenseText;dialog.querySelector('details').open=false;dialog.showModal();
  }
  function render() {
    const matches=fonts.filter(f=>f.kind===kind && `${f.name} ${f.description} ${f.style}`.toLowerCase().includes(query.toLowerCase()));
    document.querySelectorAll('[data-kind]').forEach(b=>b.setAttribute('aria-pressed',String(b.dataset.kind===kind)));
    $('#count').textContent=`${matches.length} 款${kind==='font'?' Shell ':'界面'}字体 · 样张来自完整字体实际渲染`;
    $('#cards').replaceChildren();
    for(const font of matches){
      const card=el('article','card');card.dataset.font=font.id;
      const img=el('img');img.src=font.preview.path;img.alt=`${font.name}：简体、繁体与英文实际样张`;img.loading='lazy';img.decoding='async';img.width=800;img.height=200;
      const bottom=el('div','card-bottom'),meta=el('span','meta',`${size(font.file.size)}${font.builtinId?' · 客户端已内置':''}`),button=el('button','primary','选择此字体');button.type='button';button.onclick=()=>open(font);
      bottom.append(meta,button);card.append(el('h2','',font.name),el('p','style',font.description),img,el('p','note',font.fallbackNote),bottom);$('#cards').append(card);
    }
    if(!matches.length)$('#cards').append(el('p','empty','没有找到匹配的字体，请换个关键词。'));
  }
  document.querySelectorAll('[data-kind]').forEach(b=>b.onclick=()=>{kind=b.dataset.kind;render();});
  $('#search').oninput=e=>{query=e.target.value.trim();render();};
  async function load(){
    try{const response=await fetch('catalog.json',{cache:'no-cache'});if(!response.ok)throw new Error();const data=await response.json();
      if(data.version!==1 || !Array.isArray(data.fonts) || data.fonts.length!==50)throw new Error();
      for(const f of data.fonts)if(!/^[a-z0-9-]+$/.test(f.id) || !['font','ui-font'].includes(f.kind) || !localPath(f.file?.path,'files') || !localPath(f.preview?.path,'previews') || !/^https:\/\//.test(f.project))throw new Error();
      fonts=data.fonts;render();
    }catch{$('#count').replaceChildren(document.createTextNode('字体目录暂时无法读取。'));const retry=el('button','','重试');retry.onclick=load;$('#count').append(retry);}
  }
  load();
})();
