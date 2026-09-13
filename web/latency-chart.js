'use strict';
window.DengLatencyChart = (() => {
  const NS = 'http://www.w3.org/2000/svg', views = new WeakMap();
  const valid = sample => sample.status === 'reply' && Number.isFinite(sample.milliseconds) && sample.milliseconds >= 0;
  function range(values) {
    if (!values.length) return null;
    const low = Math.min(...values), high = Math.max(...values);
    if (high === 0) return { low:0, middle:.5, high:1, step:.5 };
    const span = Math.max(high - low, high * .08, .02), unit = 10 ** Math.floor(Math.log10(span / 2));
    for (const factor of [1, 2, 2.5, 5, 10, 20, 25, 50]) {
      const step = unit * factor;
      // The two equal intervals share the same origin as the bars. Reserve
      // headroom around constant/near-constant latency without inventing jitter.
      const base = Math.max(0, Math.floor(((low + high) / 2 - step) / step + 1e-9) * step);
      for (const bottom of [base, base + step].sort((a,b) => Math.abs(a+step-(low+high)/2)-Math.abs(b+step-(low+high)/2))) {
        if (bottom <= low && bottom + step * 2 >= high && (low !== high || bottom < low || low === 0)) return { low:bottom, middle:bottom+step, high:bottom+step*2, step };
      }
    }
    return { low:0, middle:high/2, high, step:high/2 };
  }
  function label(value, step) { return value.toFixed(Math.min(6, Math.max(0, Math.ceil(-Math.log10(step)) + 1))).replace(/(\.\d*?[1-9])0+$|\.0+$/, '$1'); }
  function model(ping, now = Date.now()) {
    const samples = (ping?.samples || []).map(sample => ({...sample, time:Date.parse(sample.sampledAt)})).filter(sample => Number.isFinite(sample.time) && sample.time > now - 60000 && sample.time <= now).sort((a,b) => a.time-b.time || a.sequence-b.sequence);
    const segments = []; let segment = [], previous;
    for (const sample of samples) {
      // A missing second, local error or lost reply breaks the curve. Never
      // interpolate across a period for which the server did not return data.
      const consecutive = previous && sample.time > previous.time && sample.time - previous.time <= 1600 && (!Number.isFinite(previous.sequence) || !Number.isFinite(sample.sequence) || sample.sequence === previous.sequence + 1);
      if (!valid(sample) || !consecutive) { if (segment.length) segments.push(segment); segment = []; }
      if (valid(sample)) segment.push(sample);
      previous = sample;
    }
    if (segment.length) segments.push(segment);
    return { samples, segments, range:range(samples.filter(valid).map(s=>s.milliseconds)), now };
  }
  // Shape-preserving cubic interpolation passes through every actual sample.
  // Local extrema retain their exact height; tangent limiting prevents invented
  // overshoots or smoothing away a one-second spike.
  function curve(points) {
    if (!points.length) return '';
    let d = `M${points[0].x} ${points[0].y}`;
    if (points.length === 1) return d;
    const slopes = points.slice(1).map((p,i) => (p.y-points[i].y)/(p.x-points[i].x));
    const tangents = points.map((_,i) => i === 0 ? slopes[0] : i === points.length-1 ? slopes[i-1] : slopes[i-1]*slopes[i] <= 0 ? 0 : 2/(1/slopes[i-1]+1/slopes[i]));
    slopes.forEach((s,i) => {
      if (!s) { tangents[i]=0; tangents[i+1]=0; return; }
      const a=tangents[i]/s,b=tangents[i+1]/s,length=Math.hypot(a,b);
      if (length>3) { tangents[i]=3*a/length*s; tangents[i+1]=3*b/length*s; }
    });
    points.slice(1).forEach((p,i) => { const a=points[i],dx=(p.x-a.x)/3; d+=`C${a.x+dx} ${a.y+tangents[i]*dx} ${p.x-dx} ${p.y-tangents[i+1]*dx} ${p.x} ${p.y}`; });
    return d;
  }
  function svgElement(tag, attributes, text) { const e=document.createElementNS(NS,tag);for(const [name,value] of Object.entries(attributes || {}))e.setAttribute(name,String(value));if(text!=null)e.textContent=text;return e; }
  function response(value) { return value === 0 ? '<1 ms' : value < .01 ? '<0.01 ms' : value.toFixed(2)+' ms'; }
  function hide(view) { view.index=-1;view.tooltip.hidden=true;view.cursor?.setAttribute('visibility','hidden'); }
  function inspect(view, index) {
    const probe=view.data.samples[index];if(!probe){hide(view);return;}
    view.index=index;
    const description={ timeout:'回包超时 · 延迟未知',unreachable:'目标不可达 · 延迟未知',unavailable:'本机探测不可用 · 不计丢包',pending:'等待回包 · 暂不计丢包' };
    const time=new Date(probe.time).toLocaleTimeString('zh-CN',{hour12:false});
    view.tooltip.replaceChildren();
    const clock=document.createElement('span');clock.textContent=time;const value=document.createElement('strong');value.textContent=valid(probe)?response(probe.milliseconds):description[probe.status]||'无有效回包';
    const detail=document.createElement('small');detail.textContent=`第 ${probe.sequence} 次${valid(probe)?' · 已收到回应':''}`;
    view.tooltip.append(clock,value,detail);view.tooltip.hidden=false;
    const x=view.positions[index];view.cursor?.setAttribute('x1',x);view.cursor?.setAttribute('x2',x);view.cursor?.setAttribute('visibility','visible');
    const box=view.host.getBoundingClientRect(),zoom=box.width/view.width||1,t=view.tooltip.getBoundingClientRect();
    const left=box.left+x*zoom-t.width/2,top=box.top-t.height-8;
    view.tooltip.style.left=Math.max(6,Math.min(left/zoom,(innerWidth-t.width-6)/zoom))+'px';
    view.tooltip.style.top=Math.max(6,(top<6?box.bottom+8:top)/zoom)+'px';
  }
  function draw(view) {
    const width=Math.max(100,view.host.clientWidth),height=54,top=5,bottom=36;
    view.width=width;view.data=model(view.ping);
    const {samples,segments,range:axis,now}=view.data, labels=axis?[axis.high,axis.middle,axis.low].map(v=>label(v,axis.step)):['—','—','—'];
    const left=Math.max(29,...labels.map(s=>s.length*5.8+11)),right=width-3,plotWidth=Math.max(1,right-left);
    const y=value=>bottom-(value-axis.low)/(axis.high-axis.low)*(bottom-top);
    const x=time=>left+(time-(now-60000))/60000*plotWidth;
    const svg=svgElement('svg',{viewBox:`0 0 ${width} ${height}`,width:'100%',height,role:'img','aria-label':`最近 60 秒延迟，每秒真实采样，左旧右新。${axis?`纵轴 ${labels[2]} 至 ${labels[0]} ms。`:''}曲线穿过每个回包读数；丢包或缺失区间断开，红叉表示超时或不可达，灰点表示待回包或本机探测不可用。聚焦后使用左右方向键查看精确读数。`});
    const grid=svgElement('g',{'class':'latency-grid'});
    [top,(top+bottom)/2,bottom].forEach((pos,i)=>{grid.append(svgElement('line',{x1:left,y1:pos,x2:right,y2:pos,'class':i===2?'latency-baseline':''}),svgElement('text',{x:left-8,y:pos+3,'text-anchor':'end'},labels[i]));});svg.append(grid);
    // Explicit axis break: a zoomed nonzero baseline is never presented as zero.
    if(axis?.low>0)svg.append(svgElement('path',{d:`M${left-4} ${bottom-2}l2 -2 2 2 2 -2`,'class':'latency-axis-break'}));
    const clipID='latency-clip-'+view.id,gradientID='latency-fill-'+view.id,defs=svgElement('defs'),clip=svgElement('clipPath',{id:clipID});clip.append(svgElement('rect',{x:left,y:top-2,width:plotWidth,height:bottom-top+7}));defs.append(clip);
    const gradient=svgElement('linearGradient',{id:gradientID,x1:0,y1:0,x2:0,y2:1});gradient.append(svgElement('stop',{offset:'0%','stop-color':'currentColor','stop-opacity':'.27'}),svgElement('stop',{offset:'100%','stop-color':'currentColor','stop-opacity':'.025'}));defs.append(gradient);svg.append(defs);
    const graph=svgElement('g',{'clip-path':`url(#${clipID})`,'class':'latency-series'});view.positions=samples.map(probe=>x(probe.time));
    for(const run of segments){
      const points=run.map(probe=>({x:x(probe.time),y:y(probe.milliseconds)})),d=curve(points),first=points[0],last=points.at(-1);
      if(points.length>1){ graph.append(svgElement('path',{d:`${d}L${last.x} ${bottom}L${first.x} ${bottom}Z`,fill:`url(#${gradientID})`,'class':'latency-area'}),svgElement('path',{d,'class':'latency-line'})); }
      else graph.append(svgElement('circle',{cx:first.x,cy:first.y,r:1.6,'class':'latency-point'}));
    }
    for(const probe of samples){
      const cx=x(probe.time);
      if(probe.status==='timeout'||probe.status==='unreachable'){
        graph.append(svgElement('line',{x1:cx,y1:top,x2:cx,y2:bottom-6,'class':'latency-loss-guide'}),svgElement('path',{d:`M${cx-2} ${bottom-4}l4 4m-4 0l4 -4`,'class':'latency-missed'}));
      }else if(!valid(probe))graph.append(svgElement('circle',{cx,cy:bottom-2,r:1.3,'class':'latency-unknown'}));
    }
    svg.append(graph,svgElement('text',{x:left,y:51,'class':'latency-time'},'60 秒前'),svgElement('text',{x:right,y:51,'text-anchor':'end','class':'latency-time'},'现在'));
    if(!samples.length)svg.append(svgElement('text',{x:left+plotWidth/2,y:25,'text-anchor':'middle','class':'latency-empty'},view.ping?'等待探测结果':'连接后显示延迟'));
    view.cursor=svgElement('line',{x1:0,y1:top,x2:0,y2:bottom,'class':'latency-cursor',visibility:'hidden'});svg.append(view.cursor);view.host.replaceChildren(svg);
    if(view.index>=0)inspect(view,Math.min(view.index,samples.length-1));
  }
  let serial=0;
  function render(host,ping,key='') {
    if(!host)return;
    let view=views.get(host);
    if(!view){
      const tooltip=document.createElement('div');tooltip.className='latency-tooltip';tooltip.hidden=true;tooltip.setAttribute('role','status');document.body.append(tooltip);
      view={host,tooltip,id:++serial,index:-1,key,ping};views.set(host,view);host.tabIndex=0;
      host.onpointermove=event=>{const box=host.getBoundingClientRect(),x=(event.clientX-box.left)*view.width/box.width;let nearest=-1,distance=Infinity;view.positions.forEach((position,i)=>{const d=Math.abs(x-position);if(d<distance){nearest=i;distance=d;}});if(distance>Math.max(8,view.width/60*1.5))hide(view);else inspect(view,nearest);};
      host.onpointerleave=()=>hide(view);host.onblur=()=>hide(view);host.onfocus=()=>inspect(view,view.data.samples.length-1);
      host.onkeydown=event=>{if(event.key==='Escape'){hide(view);return;}if(!['ArrowLeft','ArrowRight','Home','End'].includes(event.key))return;event.preventDefault();const last=view.data.samples.length-1,next=event.key==='Home'?0:event.key==='End'?last:view.index<0?last:Math.max(0,Math.min(last,view.index+(event.key==='ArrowLeft'?-1:1)));inspect(view,next);};
      const observer=new ResizeObserver(()=>{cancelAnimationFrame(view.frame);view.frame=requestAnimationFrame(()=>draw(view));});observer.observe(host);view.observer=observer;
    }
    if(view.key!==key){hide(view);view.key=key;}
    view.ping=ping;draw(view);
  }
  return { render,model,range,label,curve };
})();
