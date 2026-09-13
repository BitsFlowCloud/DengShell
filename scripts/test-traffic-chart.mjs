import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import vm from 'node:vm';
const context={window:{}};
vm.runInNewContext(readFileSync(new URL('../web/workspace-refinements.js',import.meta.url),'utf8'),context);
const {trafficChartModel,rememberNetworkSample,trafficChartRuns,trafficSamplesContinuous,currentNetworkInterfaces}=context;
const now=Date.parse('2026-09-13T05:00:00Z');
const sample=(tx,rx,age=0)=>({tx,rx,sampledAt:new Date(now-age).toISOString()});
const units=['B/s','KiB/s','MiB/s','GiB/s'];
for(const peak of [0,.125,1,2.5,27,999,1024,2500,1024**2*2.5,1024**3*39,1024**4]){
 const result=trafficChartModel([sample(peak,peak/4)],now),{axis}=result;
 assert(axis.max>=peak && axis.max>0,'shared range contains both actual rates');
 assert.equal(axis.labels[2],'0','traffic always has a truthful zero baseline');
 const factor=1024**units.indexOf(axis.unit);
 assert.equal(Number(axis.labels[0])*factor,axis.max,'top label matches true byte rate');
 assert.equal(Number(axis.labels[1])*factor,axis.max/2,'middle label is exact, including 1.25');
 assert.equal(peak===0?0:peak/axis.max/(peak/4/axis.max),peak===0?0:4,'tx/rx keep their actual ratio');
}
assert.equal(trafficChartModel([],now).axis,null);
assert.equal(trafficChartModel([sample(null,NaN),sample(-1,-3)],now).axis,null,'unknown/invalid rates are never zero samples');
const timed=trafficChartModel([sample(1,2,30000),sample(3,4,29999),sample(5,6,2000),sample(7,8,-1)],now);
assert.deepEqual(Array.from(timed.points,p=>p.tx),[3,5],'actual thirty-second timestamps exclude stale and future samples');
const state={};
const first={sampledAt:new Date(now-5000).toISOString(),interfaces:[{name:'eth0',ready:true,rx:400,tx:100},{name:'eth1',ready:true,rx:2,tx:1}]};
rememberNetworkSample(state,first);rememberNetworkSample(state,first);
assert.equal(state.interfaceCharts.get('eth0').length,1,'cached backend response is not duplicated');
rememberNetworkSample(state,{sampledAt:new Date(now).toISOString(),interfaces:[{name:'eth0',ready:false,rx:0,tx:0}]});
assert.equal(state.interfaceCharts.get('eth0')[1].rx,null,'counter reset or missing sample remains a gap');
assert.equal(state.interfaceCharts.get('eth1')[0].rx,2,'interface histories remain independent');
/* R9 dynamic-axis behavior: full peaks stay visible, small rates recover after 30s + 5s. */
const axisState={};
const peakPoint=sample(2*1024**2,1000);
let axis=trafficChartModel([peakPoint],now,axisState).axis;
assert(axis.max>=2*1024**2*1.1,'a new peak receives headroom immediately');
const large=axis.max;
for(const age of [1000,29000]){axis=trafficChartModel([peakPoint,{...sample(4096,819),sampledAt:new Date(now+age).toISOString()}],now+age,axisState).axis;assert.equal(axis.max,large,'a visible peak is not cropped');}
for(const age of [30000,31000,32000,34000]){axis=trafficChartModel([{...sample(age%2000?5000:4096,819),sampledAt:new Date(now+age).toISOString()}],now+age,axisState).axis;assert.equal(axis.max,large,'downshift waits five seconds despite varying smaller buckets');}
axis=trafficChartModel([{...sample(4096,819),sampledAt:new Date(now+35000).toISOString()}],now+35000,axisState).axis;
assert(axis.max<large&&axis.unit==='KiB/s','an expired MB peak cannot permanently flatten KB traffic');
assert.equal(trafficChartRuns([{time:0,tx:1},{time:1000,tx:2},{time:3000,tx:4}], 'tx',x=>x,y=>y).length,2,'one missing second makes a real gap');
/* R11: counter-proven continuity tolerates delivery jitter, never missed data. */
const firstCounter={time:0,rx:100,tx:200,rxBytes:1000,txBytes:2000,elapsedMilliseconds:1000};
const jitteredCounter={time:2300,rx:100,tx:200,rxBytes:1200,txBytes:2400,elapsedMilliseconds:2000};
const nextCounter={time:3300,rx:150,tx:250,rxBytes:1350,txBytes:2650,elapsedMilliseconds:1000};
for(const key of ['rx','tx']){
 assert.equal(trafficChartRuns([firstCounter,jitteredCounter,nextCounter],key,x=>x,y=>y).length,1,'valid two-second remote counter interval stays connected despite SSH delivery jitter');
 assert.equal(trafficChartRuns([firstCounter,{...jitteredCounter,elapsedMilliseconds:1000}],key,x=>x,y=>y).length,2,'skipping a real backend observation breaks both series instead of inventing a sample');
 assert.equal(trafficChartRuns([firstCounter,{...jitteredCounter,time:4500}],key,x=>x,y=>y).length,2,'a long unobserved wall-clock period is not silently filled');
 assert.equal(trafficChartRuns([firstCounter,{...jitteredCounter,rx:null,tx:null},nextCounter],key,x=>x,y=>y).length,2,'counter reset or hidden-window baseline stays visibly missing');
}
assert.equal(trafficSamplesContinuous(firstCounter,{...jitteredCounter,time:1900,elapsedMilliseconds:1000,rxBytes:1100,txBytes:2200}),true,'a one-second observation with delayed delivery does not create a false gap');
assert.equal(trafficSamplesContinuous(firstCounter,{...jitteredCounter,rxBytes:900}),false,'counter rollback never connects');
assert.equal(trafficSamplesContinuous(firstCounter,{...jitteredCounter,time:0}),false,'duplicate or out-of-order timestamps never connect');
assert.equal(trafficSamplesContinuous(firstCounter,{...jitteredCounter,time:3200,elapsedMilliseconds:3000,rxBytes:1300,txBytes:2600}),true,'the backend maximum valid three-second interval remains continuous');
assert.equal(trafficSamplesContinuous(firstCounter,{...jitteredCounter,time:3200,elapsedMilliseconds:3100,rxBytes:1310,txBytes:2620}),false,'invalid remote sampling intervals are not treated as observations');
assert.equal(trafficSamplesContinuous(firstCounter,{...jitteredCounter,time:1000,elapsedMilliseconds:0}),false,'a new baseline is not a valid sampled interval');
assert.equal(trafficSamplesContinuous({time:0,rx:1,tx:1},{time:2200,rx:1,tx:1,elapsedMilliseconds:2000}),true,'legacy snapshots use the supplied sampling interval with bounded jitter');
assert.equal(trafficSamplesContinuous({time:0,rx:1,tx:1},{time:1000,rx:1,tx:1}),true,'older snapshots without interval metadata retain ordinary one-second continuity');
assert.equal(trafficSamplesContinuous({time:0,rx:1,tx:1},{time:2200,rx:1,tx:1}),false,'legacy unknown intervals do not justify filling a missing observation');
assert.equal(trafficSamplesContinuous({time:0,rxBytes:1e18,txBytes:1e18},{time:2100,rxBytes:1e18+1025,txBytes:1e18+2049,rx:1025,tx:2049,elapsedMilliseconds:1000}),true,'large uint64 byte counters allow only their unavoidable JSON number rounding');
assert.equal(trafficSamplesContinuous({time:0,rxBytes:1e18,txBytes:1e18},{time:2100,rxBytes:1e18+4096,txBytes:1e18+8192,rx:1025,tx:2049,elapsedMilliseconds:1000}),false,'large-counter tolerance still rejects a skipped observation outside rounding precision');
const sampledState={};
rememberNetworkSample(sampledState,{sampledAt:new Date(now-2300).toISOString(),elapsedMilliseconds:1000,interfaces:[{name:'eth0',ready:true,...firstCounter}]});
rememberNetworkSample(sampledState,{sampledAt:new Date(now).toISOString(),elapsedMilliseconds:2000,interfaces:[{name:'eth0',ready:true,...jitteredCounter}]});
const sampled=trafficChartModel(sampledState.interfaceCharts.get('eth0'),now).points;
assert.equal(sampled[1].elapsedMilliseconds,2000,'actual remote observation interval survives interface history storage');
assert.equal(sampled[1].txBytes,2400,'actual cumulative counters survive history storage');
assert.equal(trafficChartRuns(sampled,'tx',x=>x,y=>y).length,1,'the complete API-to-model path preserves truthful sample continuity');
const metadata={metadataSampledAt:new Date(now).toISOString(),serverIPv4:['203.0.113.1'],interfaces:[{name:'eth0',addresses:['203.0.113.1'],default:true,up:true,rx:999,tx:999}]};
const live={stats:metadata,networkStats:{metadataSampledAt:'0001-01-01T00:00:00Z',interfaces:[{name:'eth0',addresses:[],default:false,up:false,rx:100,tx:10,ready:true}]}};
const combined=currentNetworkInterfaces(live)[0];assert.equal(combined.rx,100);assert.equal(combined.default,true);assert.equal(combined.addresses[0],'203.0.113.1');assert.equal(metadata.interfaces[0].rx,999,'lightweight traffic never mutates core metadata');
console.log('Traffic chart: adaptive byte-rate scale, actual 30s window, counter-proven continuity under delivery jitter, real missing-data gaps and NIC history deduplication passed.');

/* R12: returning to a tab backfills actual backend samples, including gaps. */
const frameAt=(index)=>({sampledAt:new Date(now-12000+index*1000).toISOString(),elapsedMilliseconds:1000,interfaces:[
 {name:'eth0',ready:true,rx:100,tx:200,rxBytes:1000+index*100,txBytes:2000+index*200},
 {name:'lo',ready:true,rx:30,tx:30,rxBytes:300+index*30,txBytes:300+index*30}
]});
const frames=Array.from({length:11},(_,i)=>frameAt(i));
const tab={};rememberNetworkSample(tab,frames[0]);rememberNetworkSample(tab,frames[10]);
assert.equal(trafficChartRuns(trafficChartModel(tab.interfaceCharts.get('eth0'),now).points,'tx',x=>x,y=>y).length,2,'before history arrives, an unobserved interval is not invented');
rememberNetworkSample(tab,{...frames[10],history:frames});
assert.equal(tab.interfaceCharts.get('eth0').length,11,'all genuine background samples fill in behind an already-known latest point');
assert.equal(trafficChartRuns(trafficChartModel(tab.interfaceCharts.get('eth0'),now).points,'tx',x=>x,y=>y).length,1,'returning to a tab restores a continuous counter-verified curve');
assert.equal(tab.interfaceCharts.get('lo')[7].rx,30,'a second NIC keeps its own independent background rates');
rememberNetworkSample(tab,{...frames[10],history:frames});
assert.equal(tab.interfaceCharts.get('eth0').length,11,'overlapping rolling histories never duplicate points');
rememberNetworkSample(tab,frameAt(12));
rememberNetworkSample(tab,{...frames[6],history:frames.slice(0,7)});
assert.equal(tab.interfaceCharts.get('eth0').at(-1).sampledAt,frameAt(12).sampledAt,'an older response cannot evict a newer observation');
const failureTab={};const withFailure=frames.map((frame,index)=>index===5?{...frame,interfaces:[],elapsedMilliseconds:0}:frame);
rememberNetworkSample(failureTab,{...withFailure.at(-1),history:withFailure});
for(const name of ['eth0','lo']){
 const stored=failureTab.interfaceCharts.get(name);assert.equal(stored[5].tx,null,'backend error is retained as a real missing sample for every NIC');
 assert.equal(trafficChartRuns(trafficChartModel(stored,now).points,'tx',x=>x,y=>y).length,2,'background error must not be hidden by drawing through it');
}
const removedNIC={};const removedFrames=frames.map((frame,index)=>index===5?{...frame,interfaces:frame.interfaces.slice(1)}:frame);
rememberNetworkSample(removedNIC,{...removedFrames.at(-1),history:removedFrames});
assert.equal(removedNIC.interfaceCharts.get('eth0')[5].tx,null,'removed NIC receives a missing point, not another NIC rate');
assert.equal(removedNIC.interfaceCharts.get('lo')[5].tx,30,'remaining NIC is not interrupted by another interface disappearing');
const retained={};const minute=Array.from({length:62},(_,i)=>frameAt(i-49));rememberNetworkSample(retained,{...minute.at(-1),history:minute});
assert.equal(retained.interfaceCharts.get('eth0').length,30,'history is bounded to the latest thirty seconds');
assert.equal(trafficChartRuns(trafficChartModel(retained.interfaceCharts.get('eth0'),now).points,'tx',x=>x,y=>y).length,1,'returning after more than the visible window retrieves a full recent curve');
console.log('Network history: true background backfill, ordered deduplication, NIC isolation, real error gaps and 30s retention passed.');
