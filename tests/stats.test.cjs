'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const source=fs.readFileSync(require('node:path').join(__dirname,'../web/stats.js'),'utf8');
const metrics=(read='2026-10-28T16:00:00Z',extra={})=>({read,cpu_percent:25,cpu_interval_seconds:1,memory_bytes:1024,memory_limit_bytes:4096,memory_percent:25,network_rx_bytes:100,network_tx_bytes:200,block_read_bytes:1000,block_write_bytes:2000,...extra});
const row=(extra={})=>({id:'a'.repeat(64),name:'web',state:'running',image:'example:latest',project:'app',stats:metrics(),...extra});
function page({host='local',caps=[],hidden=false,defer=false,saved=null,data={containers:[row()],total:1,host_total:1,offset:0,limit:32,checked_at:'2026-10-28T16:00:00Z'}}={}){
 const nodes=new Map(),events={},documentEvents={},requests=[],pending=[],timers=new Map();let timerID=0;
 class Node{constructor(){this.value='';this.hidden=false;this.disabled=false;this.children=[];this.listeners={};this.attrs={};}addEventListener(type,fn){(this.listeners[type]??=[]).push(fn);}async fire(type){for(const fn of this.listeners[type]||[])await fn({target:this,preventDefault(){}});}append(...items){this.children.push(...items);}replaceChildren(...items){this.children=items;}setAttribute(key,value){this.attrs[key]=value;}}
 const find=id=>{if(!nodes.has(id))nodes.set(id,new Node());return nodes.get(id);};find('stats-view').hidden=hidden;
 const window={ReefNavigation:saved?{read:()=>saved}:undefined,reefHost:host,reefCapabilities:caps,addEventListener(type,fn){(events[type]??=[]).push(fn);},dispatchEvent(event){for(const fn of events[event.type]||[])fn(event);}};
 const document={hidden:false,getElementById:find,createElement:()=>new Node(),addEventListener(type,fn){(documentEvents[type]??=[]).push(fn);}};
 const context={window,document,AbortController,URLSearchParams,CustomEvent:function(type){this.type=type;},setTimeout(fn){const id=++timerID;timers.set(id,fn);return id;},clearTimeout(id){timers.delete(id);},request:async(url,opts,host)=>{requests.push({url,opts,host});if(defer)return new Promise((resolve,reject)=>pending.push({resolve,reject}));return {json:async()=>data};}};
 vm.runInNewContext(source,context);
 return {find,window,document,requests,pending,timers,data,async settle(){for(let i=0;i<30;i++)await Promise.resolve();},async tick(){const entry=[...timers].at(-1);if(entry){timers.delete(entry[0]);await entry[1]();}await this.settle();},async view(hidden){find('stats-view').hidden=hidden;window.dispatchEvent({type:'reef-view-changed'});await this.settle();},async host(id,caps=[]){window.reefHost=id;window.reefCapabilities=caps;window.dispatchEvent({type:'reef-host-changed'});await this.settle();},async visibility(hidden){document.hidden=hidden;for(const fn of documentEvents.visibilitychange||[])fn();await this.settle();}};
}
const text=node=>[node.textContent||'',...node.children.map(text)].join(' ');
test('statistics stay idle while hidden and expose explicit units only when opened',async()=>{
 const p=page({hidden:true});await p.settle();assert.equal(p.requests.length,0);await p.view(false);assert.equal(p.requests.length,1);const rows=p.find('stats-rows').children;assert.equal(rows.length,1);assert.match(text(rows[0]),/25%/);assert.match(text(rows[0]),/1 KiB/);assert.match(text(rows[0]),/Σ RX 100 B/);assert.equal(p.window.reefStatsReady,true);assert.equal(p.find('stats-loading').hidden,true);
});
test('network and disk rates require two samples and reset counters do not produce negative rates',async()=>{
 const p=page();await p.settle();assert.match(text(p.find('stats-rows')),/↓ —/);
 p.data.containers=[row({stats:metrics('2026-10-28T16:00:05Z',{network_rx_bytes:612,block_read_bytes:1512})})];await p.tick();assert.match(text(p.find('stats-rows')),/↓ 102.4 B\/s/);assert.match(text(p.find('stats-rows')),/R 102.4 B\/s/);
 p.data.containers=[row({stats:metrics('2026-10-28T16:00:10Z',{network_rx_bytes:1,block_read_bytes:1})})];await p.tick();assert.match(text(p.find('stats-rows')),/↓ —/);assert.doesNotMatch(text(p.find('stats-rows')),/-102/);
});
test('pause stops automatic collection, Refresh works while paused, and resume collects again',async()=>{
 const p=page();await p.settle();await p.find('stats-pause').fire('click');assert.equal(p.timers.size,0);assert.equal(p.find('stats-pause').textContent,'Resume');await p.find('stats-refresh').fire('click');await p.settle();assert.equal(p.requests.length,2);assert.equal(p.timers.size,0);await p.find('stats-pause').fire('click');await p.settle();assert.equal(p.requests.length,3);assert.equal(p.timers.size,1);
});
test('view and visibility changes abort outstanding requests and discard late samples',async()=>{
 const p=page({defer:true});await p.settle();assert.equal(p.find('stats-loading').hidden,false);await p.view(true);assert.equal(p.requests[0].opts.signal.aborted,true);p.pending[0].resolve({json:async()=>p.data});await p.settle();assert.equal(p.find('stats-rows').children.length,0);assert.equal(p.timers.size,0);
 await p.view(false);await p.visibility(true);assert.equal(p.requests[1].opts.signal.aborted,true);await p.visibility(false);assert.equal(p.requests.length,3);
});
test('legacy agents request an upgrade and a host switch isolates pending responses and rate baselines',async()=>{
 const legacy=page({host:'old',caps:['control-v1']});await legacy.settle();assert.equal(legacy.requests.length,0);assert.equal(legacy.find('stats-refresh').disabled,true);assert.match(legacy.find('stats-error').textContent,/Upgrade/);assert.equal(legacy.window.reefStatsReady,true);
 const p=page({defer:true});await p.settle();await p.host('remote',['stats-v1']);assert.equal(p.requests[0].opts.signal.aborted,true);p.pending[0].resolve({json:async()=>p.data});await p.settle();assert.equal(p.find('stats-rows').children.length,0);assert.equal(p.requests[1].host,'remote');p.pending[1].resolve({json:async()=>({...p.data,containers:[row({name:'remote-web'})]})});await p.settle();assert.match(text(p.find('stats-rows')),/remote-web/);assert.match(text(p.find('stats-rows')),/↓ —/);
});
test('pagination and debounced filters request bounded pages on the current host',async()=>{
 const p=page({host:'remote',caps:['stats-v1']});p.data.total=65;p.data.host_total=100;await p.settle();assert.equal(p.find('stats-next').disabled,false);p.data.offset=32;await p.find('stats-next').fire('click');await p.settle();assert.match(p.requests.at(-1).url,/offset=32/);p.find('stats-search').value='db';p.find('stats-state').value='running';await p.find('stats-search').fire('input');p.data.offset=0;await p.tick();assert.match(p.requests.at(-1).url,/offset=0&search=db&state=running/);assert.equal(p.requests.at(-1).host,'remote');assert.equal(p.find('stats-previous').disabled,true);assert.match(p.find('stats-summary').textContent,/100 containers/);
});
test('errors keep earlier samples visibly stale and missing metrics are never presented as zeros',async()=>{
 const p=page({defer:true});await p.settle();p.pending[0].resolve({json:async()=>p.data});await p.settle();const reload=p.find('stats-refresh').fire('click');await p.settle();p.pending[1].reject(Error('Agent disconnected'));await reload;await p.settle();assert.equal(p.find('stats-rows').children.length,1);assert.match(p.find('stats-error').textContent,/may be stale/);assert.match(p.find('stats-status').textContent,/unavailable/);assert.equal(p.find('stats-loading').hidden,true);
 const stopped=page({data:{containers:[row({name:'<script>',state:'exited',stats:null})],total:1,host_total:1,offset:0,checked_at:'2026-10-28T16:00:00Z'}});await stopped.settle();assert.match(text(stopped.find('stats-rows')),/No live sample/);assert.doesNotMatch(text(stopped.find('stats-rows')),/0%/);assert.equal(stopped.find('stats-rows').children[0].children[0].children[0].textContent,'<script>');
});

 test('restoring the already visible view does not collect twice and pausing an initial read stays paused',async()=>{
 const p=page();await p.settle();await p.view(false);assert.equal(p.requests.length,1);await p.find('stats-pause').fire('click');await p.view(true);await p.view(false);assert.equal(p.requests.length,1);
 const first=page({defer:true});await first.settle();await first.find('stats-pause').fire('click');assert.equal(first.requests[0].opts.signal.aborted,true);assert.equal(first.window.reefStatsReady,true);first.pending[0].resolve({json:async()=>first.data});await first.settle();assert.equal(first.timers.size,0);assert.equal(first.find('stats-rows').children.length,0);
 });

 test('initial saved filters and pagination are used by the first collection request',async()=>{
 const p=page({saved:{view:'stats',host:'local',statsOffset:32,filters:{'stats-search':'worker','stats-state':'running'}}});p.data.offset=32;p.data.total=65;await p.settle();assert.match(p.requests[0].url,/offset=32&search=worker&state=running/);assert.equal(p.window.reefStatsOffset,32);
 });
