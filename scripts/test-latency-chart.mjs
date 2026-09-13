import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import vm from 'node:vm';
const context={window:{}};
vm.runInNewContext(readFileSync(new URL('../web/latency-chart.js',import.meta.url),'utf8'),context);
const {model,range,label,curve}=context.window.DengLatencyChart;
for(const values of [[0],[.001,.005],[.12,.14,.13],[46,55,79,62,50],[258.2,258.5,259.4],[258],[0,900],[999,1000],[10000,10001]]){
 const axis=range(values);
 assert(axis.low<=Math.min(...values) && axis.high>=Math.max(...values),'every measured value fits the scale');
 assert(axis.low>=0 && axis.high>axis.low,'nonnegative, nondegenerate scale');
 assert(Math.abs(axis.middle-axis.low-(axis.high-axis.middle))<1e-7,'three evenly spaced ticks');
 assert.notEqual(label(axis.high,axis.step),label(axis.middle,axis.step),'tick labels remain distinct');
 assert.notEqual(label(axis.middle,axis.step),label(axis.low,axis.step),'sub-ms tick labels do not collapse');
}
assert.equal(range([]),null);
const now=Date.parse('2026-09-13T03:00:00Z');
const sample=(sequence,age,status='reply',milliseconds=50)=>({sequence,sampledAt:new Date(now-age).toISOString(),status,milliseconds});
const result=model({samples:[sample(1,60000),sample(2,59999),sample(3,40000,'timeout',99999),sample(4,20000),sample(5,0,'unavailable',99999),sample(6,-1),{sequence:7,sampledAt:'invalid',status:'reply',milliseconds:123}]},now);
assert.deepEqual(Array.from(result.samples,s=>s.sequence),[2,3,4,5],'only timestamps inside actual rolling minute');
assert.equal(result.samples[2].time-result.samples[1].time,20000,'missing intervals remain actual elapsed time');
assert(result.range.high<99999,'no artificial timeout or unavailable latency');
assert.equal(model({samples:[sample(1,0,'timeout')]},now).range,null,'timeouts alone do not invent a scale');
assert.equal(model(null,now).samples.length,0);
const equal=model({samples:[sample(1,3000,'reply',258),sample(2,2000,'reply',258),sample(3,1000,'reply',258)]},now);
assert.equal(new Set(equal.samples.map(s=>s.milliseconds)).size,1,'constant measurements stay constant');
const interrupted=model({samples:[sample(1,9000),sample(2,8000),sample(3,7000,'timeout'),sample(4,6000),sample(5,5000,'unavailable'),sample(6,4000),sample(7,1000),sample(8,0)]},now);
assert.deepEqual(Array.from(interrupted.segments,s=>Array.from(s,p=>p.sequence)),[[1,2],[4],[6],[7,8]],'lost replies, local errors and missing seconds must break the curve');
const sequenceGap=model({samples:[sample(1,1000),sample(3,0)]},now);
assert.equal(sequenceGap.segments.length,2,'missing probe sequence never silently joins two replies');
assert.equal(model({samples:[sample(1,0,'pending')]},now).segments.length,0,'pending probe has no invented curve point');
function checkCurve(values,times=values.map((_,i)=>i*3.3)){
 const points=values.map((y,i)=>({x:times[i],y})),path=curve(points);
 assert(path.startsWith(`M${points[0].x} ${points[0].y}`));
 const pieces=[...path.matchAll(/C([^C]+)/g)].map(m=>m[1].trim().split(/\s+/).map(Number));
 assert.equal(pieces.length,points.length-1,'every adjacent real sample has exactly one curve segment');
 pieces.forEach(([x1,y1,x2,y2,x3,y3],i)=>{
  const a=points[i],b=points[i+1];
  assert.equal(x3,b.x);assert.equal(y3,b.y,'curve reaches actual sample, including a one-second spike');
  assert(x1>=a.x&&x1<=x2&&x2<=b.x,'time never runs backward');
  for(let j=0;j<=100;j++){
   const t=j/100,u=1-t,y=u*u*u*a.y+3*u*u*t*y1+3*u*t*t*y2+t*t*t*y3;
   assert(y>=Math.min(a.y,b.y)-1e-8&&y<=Math.max(a.y,b.y)+1e-8,'interpolation never invents overshoot or negative latency');
  }
 });
}
for(const values of [[258,258,258],[0,0,.005,0],[50,50,900,50,50],[79,62,46,55],[1,4,20,21,1000],[100,1,100,1]])checkCurve(values);
checkCurve([1,6,2,30],[0,.3,3.7,6.4]);
assert.equal(curve([]),'');assert.equal(curve([{x:4,y:5}]),'M4 5','a lone reply is a point');
console.log('Latency chart: exact sample endpoints, bounded smooth curves, spike preservation, loss/time gaps, rolling minute, true timestamps and axis labels passed.');
