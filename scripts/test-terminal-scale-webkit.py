#!/usr/bin/env python3
"""Run under a dedicated Xvfb display; uses real pointer events in native WebKit."""
import os
os.environ['GDK_BACKEND'] = 'x11'
import gi, json, subprocess, sys
from pathlib import Path
gi.require_version('Gtk', '3.0')
gi.require_version('WebKit2', '4.1')
from gi.repository import Gtk, WebKit2, GLib

stage = Path(sys.argv[1])
fixture = json.loads((stage / 'browser-fixture.json').read_text())
manager = WebKit2.UserContentManager()
manager.register_script_message_handler('qa')
context = WebKit2.WebContext.new_ephemeral()
context.get_website_data_manager().set_network_proxy_settings(WebKit2.NetworkProxyMode.NO_PROXY, None)
view = WebKit2.WebView(web_context=context, user_content_manager=manager)
window = Gtk.Window()
window.set_decorated(False)
window.set_default_size(1600, 1100)
window.move(0, 0)
window.add(view)
passed = False
script = r'''(async () => {
  const wait = ms => new Promise(r => setTimeout(r, ms));
  const checks = [], errors = [];
  addEventListener('error', e => errors.push(e.message));
  const click = point => new Promise(resolve => {
    window.qaPointerDone = resolve;
    webkit.messageHandlers.qa.postMessage(JSON.stringify({type:'click', ...point}));
  });
  try {
    for (let i=0; i<100 && !window.DengCommandComposer; i++) await wait(100);
    await loadProfiles(); await chooseAppearance({startupAnimation:false,onboardingCompleted:true,uiScale:1});
    await document.fonts.ready; await wait(500);
    document.querySelectorAll('dialog[open]').forEach(d => d.close()); setDrawer(false);
    pollStats = () => {}; pollNetwork = () => {};
    const id='webkit-mouse'; profiles.push({id,name:'WebKit mouse',host:'fixture.invalid',user:'qa',port:22});
    const state={...makeSessionState({id,profileId:id,home:'/'}),localOnly:true};
    sessions.set(id,state); createTerminal(state); state.connectionView.remove(); state.localOnly=false;
    state.ready=true; state.term.options.disableStdin=false; window.qaFrames=[];
    state.ws={readyState:WebSocket.OPEN,send(data){qaFrames.push(JSON.parse(data));},close(){}};
    activate(id);
    window.qaClicks=[]; document.addEventListener('mousedown',e=>qaClicks.push({x:e.clientX,y:e.clientY,detail:e.detail,target:e.target.className}),true);
    for(const scale of [.75,.9,1,1.1,1.25,1.5]) {
      appearance.uiScale=scale; applyUIScale(); await wait(150);
      const term=state.term; term.reset();
      await new Promise(r=>term.write(Array.from({length:Math.min(18,term.rows-1)},(_,i)=>`ROW${String(i).padStart(3,'0')} abcdefghijklmnopqrstuvwxyz`).join('\r\n'),r)); await wait(100);
      const span=state.host.querySelectorAll('.xterm-rows > div')[12]?.querySelector('span');
      if(!span) throw Error(JSON.stringify({rows:term.rows,viewport:[innerWidth,innerHeight],host:state.host.getBoundingClientRect().toJSON(),lines:[...state.host.querySelectorAll('.xterm-rows > div')].map(e=>e.textContent),hidden:document.hidden}));
      const rect=span.getBoundingClientRect();
      const point=column=>({x:rect.left+rect.width/span.textContent.length*(column+.2),y:rect.top+rect.height/2});
      await click({...point(2),count:2}); await wait(50);
      if(term.getSelection()!=='ROW012') throw Error(JSON.stringify({scale,selection:term.getSelection(),point:point(2),rect:rect.toJSON(),screen:term._core.screenElement.getBoundingClientRect().toJSON(),clicks:qaClicks,probe:[...term._core.screenElement.children].at(-1).getBoundingClientRect().toJSON(),model:{start:term._core._selectionService._model.selectionStart,length:term._core._selectionService._model.selectionStartLength}}));
      await new Promise(r=>term.write('\x1b[?1000h\x1b[?1006h',r)); qaFrames=[];
      await click({...point(9),count:1});
      if(!qaFrames.some(f=>f.data==='\x1b[<0;10;13M')) throw Error(`mouse scale=${scale}: ${JSON.stringify(qaFrames)}`);
      await new Promise(r=>term.write('\x1b[?1000l\x1b[?1006l',r));
      checks.push({scale,dpr:devicePixelRatio,selection:true,mouse:true});
    }
    webkit.messageHandlers.qa.postMessage(JSON.stringify({type:'result',passed:errors.length===0,checks,errors}));
  } catch(error) { webkit.messageHandlers.qa.postMessage(JSON.stringify({type:'result',passed:false,checks,error:String(error),errors})); }
})();'''

def message(_manager, result):
    global passed
    data = json.loads(result.get_js_value().to_string())
    if data['type'] == 'click':
        scale = view.get_scale_factor()
        origin = view.get_window().get_origin()
        x = round((origin[-2] + data['x']) * scale)
        y = round((origin[-1] + data['y']) * scale)
        subprocess.run(['xdotool', 'mousemove', '--sync', str(x), str(y), 'click', '--repeat', str(data['count']), '--delay', '70', '1'], check=True)
        GLib.timeout_add(100, lambda: (view.evaluate_javascript('qaPointerDone()', -1, None, None, None, None, None), False)[1])
    else:
        passed = data['passed']
        data['webkit'] = '.'.join(str(f()) for f in [WebKit2.get_major_version, WebKit2.get_minor_version, WebKit2.get_micro_version])
        (stage / 'evidence' / f'terminal-webkit-{view.get_scale_factor()}.json').write_text(json.dumps(data, indent=2))
        print(json.dumps(data), flush=True)
        Gtk.main_quit()

manager.connect('script-message-received::qa', message)
started = False
def loaded(_view, event):
    global started
    if event == WebKit2.LoadEvent.FINISHED and not started:
        started = True
        view.evaluate_javascript(script, -1, None, None, None, None, None)
view.connect('load-changed', loaded)
window.show_all()
view.load_uri(fixture['url'])
GLib.timeout_add_seconds(60, lambda: (Gtk.main_quit(), False)[1])
Gtk.main()
window.destroy()
sys.exit(0 if passed else 1)
