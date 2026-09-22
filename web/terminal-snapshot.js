/* xterm 6 runtime state that SerializeAddon 0.14 does not include. No PTY input. */
'use strict';
(() => {
  const modeKeys = ['applicationCursorKeys','applicationKeypad','bracketedPasteMode','origin','reverseWraparound','sendFocus','wraparound'];
  const protocols = ['NONE','X10','VT200','DRAG','ANY'];
  const encodings = ['DEFAULT','SGR','SGR_PIXELS'];
  const fail = () => { throw new Error('终端显示状态无法完整恢复，请返回原窗口重试'); };
  const integer = (value, min, max) => Number.isInteger(value) && value >= min && value <= max;
  function internals(term) {
    const core = term?._core, buffers = core?._bufferService?.buffers;
    if (!buffers?.normal || !buffers?.alt || !core.coreService || !core.coreMouseService || !core._charsetService || !core._inputHandler?._curAttrData) fail();
    return {core,buffers};
  }
  function charset(value) {
    if (value == null) return null;
    if (typeof value !== 'object' || Array.isArray(value) || Object.keys(value).length > 128) fail();
    const result = Object.create(null);
    for (const [key,text] of Object.entries(value)) {
      if (key.length !== 1 || key.charCodeAt(0) > 127 || typeof text !== 'string' || text.length > 8) fail();
      result[key] = text;
    }
    return result;
  }
  function attrs(value) { return {fg:value.fg >>> 0,bg:value.bg >>> 0,ext:value.extended.ext >>> 0}; }
  function validAttrs(value) { return value && ['fg','bg','ext'].every(key=>integer(value[key],0,0xffffffff)); }
  function readBuffer(buffer,cols) {
    return {
      x:buffer.x,y:buffer.y,top:buffer.scrollTop,bottom:buffer.scrollBottom,
      savedX:buffer.savedX,savedY:buffer.savedY-buffer.ybase,savedAttrs:attrs(buffer.savedCurAttrData),savedCharset:charset(buffer.savedCharset),
      tabs:Object.keys(buffer.tabs).filter(key=>buffer.tabs[key] && Number(key)<cols).map(Number),viewport:buffer.ybase-buffer.ydisp
    };
  }
  function capture(term) {
    const {core,buffers} = internals(term), service = core.coreService, cs = core._charsetService;
    if (core._inputHandler._parser.currentState !== 0 || core._inputHandler._utf8Decoder.interim.some(byte=>byte)) fail();
    return {
      version:1,cols:term.cols,rows:term.rows,active:term.buffer.active.type,
      normal:readBuffer(buffers.normal,term.cols),alternate:readBuffer(buffers.alt,term.cols),
      cursorHidden:service.isCursorHidden,cursorInitialized:service.isCursorInitialized,
      insertMode:service.modes.insertMode,modes:Object.fromEntries(modeKeys.map(key=>[key,service.decPrivateModes[key]])),
      cursorBlink:service.decPrivateModes.cursorBlink ?? null,cursorStyle:service.decPrivateModes.cursorStyle ?? null,
      mouseProtocol:core.coreMouseService.activeProtocol,mouseEncoding:core.coreMouseService.activeEncoding,
      attrs:attrs(core._inputHandler._curAttrData),glevel:cs.glevel,charset:charset(cs.charset),charsets:Array.from({length:4},(_,i)=>charset(cs._charsets[i]))
    };
  }
  function restore(term,snapshot) {
    const {core,buffers} = internals(term), s = snapshot;
    if (!s || s.version !== 1 || !integer(s.cols,2,1000) || !integer(s.rows,2,500) || s.cols !== term.cols || s.rows !== term.rows || !['normal','alternate'].includes(s.active) || s.active !== term.buffer.active.type) fail();
    if (![s.cursorHidden,s.cursorInitialized,s.insertMode].every(value=>typeof value==='boolean') || !s.modes || !modeKeys.every(key=>typeof s.modes[key]==='boolean') || ![null,true,false].includes(s.cursorBlink) || ![null,'block','underline','bar'].includes(s.cursorStyle) || !protocols.includes(s.mouseProtocol) || !encodings.includes(s.mouseEncoding) || !validAttrs(s.attrs) || !integer(s.glevel,0,3) || !Array.isArray(s.charsets) || s.charsets.length!==4) fail();
    // Validate and copy every value before mutating either buffer. A malformed
    // snapshot never partially changes the newly created terminal.
    const read = value => {
      if (!value || !integer(value.x,0,s.cols) || !integer(value.y,0,s.rows-1) || !integer(value.top,0,s.rows-1) || !integer(value.bottom,value.top,s.rows-1) || !integer(value.savedX,0,s.cols) || !integer(value.savedY,-10000,s.rows-1) || !integer(value.viewport,0,10000) || !validAttrs(value.savedAttrs) || !Array.isArray(value.tabs) || value.tabs.length>s.cols || !value.tabs.every(x=>integer(x,0,s.cols-1))) fail();
      return {...value,savedCharset:charset(value.savedCharset),tabs:[...value.tabs],savedAttrs:{...value.savedAttrs}};
    };
    const normal=read(s.normal), alternate=read(s.alternate), currentCharset=charset(s.charset), charsets=s.charsets.map(charset);
    const applyAttrs = (target,value) => { target.fg=value.fg;target.bg=value.bg;target.extended=target.extended.clone();target.extended.ext=value.ext;target.extended.urlId=0; };
    const applyBuffer = (target,value) => {
      target.x=value.x;target.y=value.y;target.scrollTop=value.top;target.scrollBottom=value.bottom;
      target.savedX=value.savedX;target.savedY=Math.max(0,target.ybase+value.savedY);applyAttrs(target.savedCurAttrData,value.savedAttrs);target.savedCharset=value.savedCharset||undefined;
      target.tabs=Object.create(null);for(const column of value.tabs)target.tabs[column]=true;
      target.ydisp=Math.max(0,target.ybase-value.viewport);
    };
    applyBuffer(buffers.normal,normal);applyBuffer(buffers.alt,alternate);
    const service=core.coreService;
    service.isCursorHidden=s.cursorHidden;service.isCursorInitialized=s.cursorInitialized;service.modes.insertMode=s.insertMode;
    for(const key of modeKeys)service.decPrivateModes[key]=s.modes[key];
    service.decPrivateModes.cursorBlink=s.cursorBlink??undefined;service.decPrivateModes.cursorStyle=s.cursorStyle??undefined;
    // A synchronized-output timer belongs to the old renderer and is temporary.
    // Resume drawing immediately; a subsequent DEC 2026 sequence controls it.
    service.decPrivateModes.synchronizedOutput=false;
    core.coreMouseService.activeProtocol=s.mouseProtocol;core.coreMouseService.activeEncoding=s.mouseEncoding;
    for(let i=0;i<4;i++)core._charsetService.setgCharset(i,charsets[i]||undefined);
    core._charsetService.setgLevel(s.glevel);core._charsetService.charset=currentCharset||undefined;
    applyAttrs(core._inputHandler._curAttrData,s.attrs);
    // Restoring ydisp alone leaves xterm in follow-output mode. Synchronize
    // both its user-scroll flag and the xterm 6 viewport before output resumes.
    const active = buffers.active;
    // The normal buffer owns scrollback, including while a TUI is active.
    core._bufferService.isUserScrolling = buffers.normal.ydisp < buffers.normal.ybase;
    term.scrollToLine(active.ydisp);
    core._viewport?.queueSync?.(active.ydisp);
    term.refresh(0,term.rows-1);
  }
  window.DengTerminalSnapshot={capture,restore};
})();
