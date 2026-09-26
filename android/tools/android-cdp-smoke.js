// Minimal raw CDP client for Android WebView, which lacks full Chrome domains.
(async () => {
  const port = Number(process.env.CDP_PORT || 9222);
  const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
  const target = targets.find(item => item.type === 'page' && item.url.startsWith('http://127.0.0.1:'));
  if (!target) throw new Error('DengShell WebView page not found');
  console.error('CDP target', target.url);
  const socket = new WebSocket(target.webSocketDebuggerUrl);
  const pending = new Map();
  let nextId = 1;
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('CDP WebSocket open timed out')), 20000);
    const done = handler => value => { clearTimeout(timer); handler(value); };
    socket.addEventListener('open', done(resolve), {once:true});
    socket.addEventListener('error', done(reject), {once:true});
  });
  console.error('CDP socket open');
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
  await send('Runtime.enable');
  const before = await evaluate(`({title:document.title,width:innerWidth,scale:getComputedStyle(document.documentElement).zoom,mobile:document.body.classList.contains('dengshell-android'),view:document.body.dataset.mobileView,nav:!!document.getElementById('mobile-navigation')})`);
  await evaluate(`document.querySelector('#mobile-navigation [data-mobile-target="files"]').click()`);
  const files = await evaluate(`({view:document.body.dataset.mobileView,panel:getComputedStyle(document.getElementById('files-panel')).display})`);
  await evaluate(`document.querySelector('#mobile-navigation [data-mobile-target="servers"]').click()`);
  const drawer = await evaluate(`({open:!document.getElementById('connections-drawer').hidden,rect:(()=>{const r=document.getElementById('connections-drawer').getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,height:r.height}})()})`);
  await evaluate(`document.getElementById('new-connection').click()`);
  const form = await evaluate(`({open:document.getElementById('connection-dialog').open,group:document.getElementById('connection-form').elements.groupId.value,auth:document.getElementById('connection-form').elements.auth.value})`);
  console.log(JSON.stringify({before,files,drawer,form},null,2));
  socket.close();
})().catch(error => { console.error(error); process.exit(1); });
