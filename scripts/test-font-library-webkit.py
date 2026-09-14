#!/usr/bin/env python3
# Requires an opt-in isolated TestFontLibraryBrowserFixture. Uses an ephemeral WebKit context.
import gi,json,sys
from pathlib import Path
gi.require_version('Gtk','3.0');gi.require_version('WebKit2','4.1')
from gi.repository import Gtk,WebKit2,GLib
stage=Path(sys.argv[1]) if len(sys.argv)>1 else None
if stage is None:raise SystemExit('Usage: python3 scripts/test-font-library-webkit.py QA_DIR')
fixture=json.loads((stage/'browser-fixture.json').read_text())
manager=WebKit2.UserContentManager();manager.register_script_message_handler('qa')
manager.add_script(WebKit2.UserScript.new("window.__qaErrors=[];addEventListener('error',e=>__qaErrors.push(e.message+' '+e.filename+':'+e.lineno));addEventListener('unhandledrejection',e=>__qaErrors.push(String(e.reason)));",WebKit2.UserContentInjectedFrames.TOP_FRAME,WebKit2.UserScriptInjectionTime.START,None,None))
context=WebKit2.WebContext.new_ephemeral()
context.get_website_data_manager().set_network_proxy_settings(WebKit2.NetworkProxyMode.NO_PROXY,None)
view=WebKit2.WebView(web_context=context,user_content_manager=manager)
window=Gtk.Window();window.set_default_size(1200,850);window.set_skip_taskbar_hint(True);window.add(view)
finished=False
script=r'''(async()=>{try{
const wait=ms=>new Promise(r=>setTimeout(r,ms));
for(let i=0;i<100&&!window.DengFontLibrary;i++)await wait(100);
await loadProfiles(); await chooseAppearance({uiFontId:'builtin:ui-ibm-plex-sans-sc'});
const widths=[];
for(const font of allFonts().filter(f=>f.kind==='builtin')){await loadFace(font);await loadFace(fontCatalog.fallback);const family=await alignedTerminalFontFamily(font);const c=document.createElement('canvas').getContext('2d');c.font=`100px ${family}`;widths.push({id:font.id,latin:c.measureText('M').width,han:c.measureText('中').width});}
await DengFontLibrary.open('ui-font');
const uiCount=document.querySelectorAll('.library-card').length;
let uiJob=await post('/api/font-library/downloads',{fontId:'ui-marker'});while(uiJob.status==='downloading'){await wait(150);uiJob=await api('/api/font-library/downloads/'+uiJob.id)}
if(uiJob.status!=='complete')throw Error(JSON.stringify(uiJob));await loadProfiles();await DengUIAppearance.useFont(uiJob.asset.id);
const uiActive=document.documentElement.dataset.uiFont===uiJob.asset.id;
let job=await post('/api/font-library/downloads',{fontId:'shell-iosevka'});while(job.status==='downloading'){await wait(150);job=await api('/api/font-library/downloads/'+job.id)}
if(job.status!=='complete')throw Error(JSON.stringify(job));await loadProfiles();
const face=allFonts().find(f=>f.id===job.asset.id);await loadFace(face);await alignedTerminalFontFamily(face);await chooseAppearance({fontId:face.id});
const c=document.createElement('canvas').getContext('2d');c.font=`100px ${terminalFontFamily}`;const narrow={latin:c.measureText('M').width,han:c.measureText('中').width,family:terminalFontFamily};
await DengFontLibrary.open('font');
const shellCount=document.querySelectorAll('.library-card').length;
document.querySelector('#library-preview-bold').click();
if(getComputedStyle(document.querySelector('.library-live-preview')).fontWeight!=='700')throw Error('Bold preview did not update');
await wait(1500);const preview=[...document.querySelectorAll('.library-live-preview')].some(x=>!x.hidden&&x.style.fontFamily.includes('Deng online preview'));
if(uiCount!==20||shellCount!==30||!uiActive||!preview||Math.abs(narrow.han-2*narrow.latin)>.2)throw Error(JSON.stringify({uiCount,shellCount,uiActive,preview,narrow}));
window.webkit.messageHandlers.qa.postMessage(JSON.stringify({passed:true,uiCount,shellCount,uiActive,preview,widths,narrow}));
}catch(error){window.webkit.messageHandlers.qa.postMessage(JSON.stringify({passed:false,error:String(error),stack:error.stack,errors:window.__qaErrors,scripts:[...document.scripts].map(s=>s.src),body:document.body?.innerText?.slice(0,1200),url:location.href.split('#')[0],ready:document.readyState}));}})();'''
def message(manager,result):
 global finished
 data=json.loads(result.get_js_value().to_string());data['webkit']='.'.join(map(str,[WebKit2.get_major_version(),WebKit2.get_minor_version(),WebKit2.get_micro_version()]))
 (stage/'webkit-results.json').write_text(json.dumps(data,ensure_ascii=False,indent=2)+'\n');print(json.dumps(data,ensure_ascii=False),flush=True);finished=data.get('passed',False);Gtk.main_quit()
manager.connect('script-message-received::qa',message)
started=False
def loaded(view,event):
 global started
 if event==WebKit2.LoadEvent.FINISHED and not started:
  started=True;view.evaluate_javascript(script,-1,None,None,None,None,None)
view.connect('load-changed',loaded)
view.connect('load-failed',lambda v,event,uri,error: (print('Load failed:',str(error),flush=True),False)[1])
window.show_all();view.load_uri(fixture['url']);GLib.timeout_add_seconds(50,lambda:(Gtk.main_quit(),False)[1]);Gtk.main();window.destroy();sys.exit(0 if finished else 1)
