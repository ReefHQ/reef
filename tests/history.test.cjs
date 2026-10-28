'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const source=fs.readFileSync(require('node:path').join(__dirname,'../web/history.js'),'utf8');
const row=(extra={})=>({id:'1'.repeat(64),host_id:'local',host:'Local',scope:'volume',action:'cleanup',status:'partial',target:'database',operation_id:'2'.repeat(48),started:'2026-10-28T17:00:00Z',time:'2026-10-28T17:00:02Z',recorded:'2026-10-28T17:00:03Z',count:2,succeeded:1,failed:1,...extra});
function page({host='local',hidden=false,defer=false,saved=null,data={rows:[row()],total:1,offset:0,limit:50,cursor:'150',retained_events:2,dropped:0,persistence_error:false,checked_at:'2026-10-28T17:00:04Z'}}={}){
 const nodes=new Map(),events={},documentEvents={},requests=[],pending=[],timers=new Map();let nextTimer=0;
 class Node{constructor(){this.value='';this.dataset={};this.hidden=false;this.disabled=false;this.children=[];this.listeners={};this.attrs={};this.textContent='';}addEventListener(type,fn){(this.listeners[type]??=[]).push(fn);}async fire(type){for(const fn of this.listeners[type]||[])await fn({target:this,preventDefault(){}});}append(...items){this.children.push(...items);}replaceChildren(...items){this.children=items;}setAttribute(key,value){this.attrs[key]=value;}showModal(){this.open=true;}close(){this.open=false;}}
 const find=id=>{if(!nodes.has(id))nodes.set(id,new Node());return nodes.get(id);};find('history-view').hidden=hidden;
 const window={reefHost:host,ReefNavigation:saved?{read:()=>saved}:undefined,addEventListener(type,fn){(events[type]??=[]).push(fn);},dispatchEvent(event){for(const fn of events[event.type]||[])fn(event);}};
 const document={hidden:false,getElementById:find,createElement:()=>new Node(),addEventListener(type,fn){(documentEvents[type]??=[]).push(fn);}};
 const context={window,document,AbortController,URLSearchParams,CustomEvent:function(type){this.type=type;},replaceKeepingFocus:(parent,nodes)=>parent.replaceChildren(...nodes),setTimeout(fn){const id=++nextTimer;timers.set(id,fn);return id;},clearTimeout(id){timers.delete(id);},request:async(url,opts,requestHost)=>{requests.push({url,opts,host:requestHost});if(defer)return new Promise((resolve,reject)=>pending.push({resolve,reject}));return {json:async()=>data};}};
 vm.runInNewContext(source,context);
 return {find,window,document,requests,pending,timers,data,async settle(){for(let i=0;i<30;i++)await Promise.resolve();},async tick(){const entry=[...timers].at(-1);if(entry){timers.delete(entry[0]);await entry[1]();}await this.settle();},async view(hidden){find('history-view').hidden=hidden;window.dispatchEvent({type:'reef-view-changed'});await this.settle();},async host(id){window.reefHost=id;window.dispatchEvent({type:'reef-host-changed',detail:{id}});await this.settle();},async visibility(hidden){document.hidden=hidden;for(const fn of documentEvents.visibilitychange||[])fn();await this.settle();}};
}
const text=node=>[node.textContent||'',...node.children.map(text)].join(' ');
test('history is lazy, displays grouped outcomes and details as text without command output',async()=>{
 const p=page({hidden:true});assert.equal(p.find('history-loading').hidden,false);await p.settle();assert.equal(p.requests.length,0);await p.view(false);assert.equal(p.requests.length,1);assert.match(text(p.find('history-rows')),/Partial/);assert.match(p.find('history-summary').textContent,/1 actions matching/);await p.find('history-rows').children[0].children.at(-1).children[0].fire('click');assert.equal(p.find('history-details').open,true);assert.match(text(p.find('history-detail-fields')),/1 succeeded · 1 failed/);await p.find('history-detail-close').fire('click');assert.equal(p.find('history-details').open,false);
 const unsafe=page({data:{...p.data,rows:[row({target:'<script>',host:'<img>'})]}});await unsafe.settle();assert.equal(unsafe.find('history-rows').children[0].children[3].textContent,'<script>');
});
test('remote and all-host history always read the central endpoint without requiring an online agent',async()=>{
 const p=page({host:'a'.repeat(64)});await p.settle();assert.match(p.requests[0].url,new RegExp('host='+'a'.repeat(64)));assert.equal(p.requests[0].host,'local');p.find('history-hosts').value='all';await p.find('history-hosts').fire('change');await p.tick();assert.match(p.requests.at(-1).url,/host=all/);assert.equal(p.requests.at(-1).host,'local');assert.ok(p.requests.every(r=>!r.opts.method));
});
test('older pages use a stable cursor and pause automatic refresh while latest refresh resets the snapshot',async()=>{
 const p=page();p.data.total=80;await p.settle();p.data.offset=50;await p.find('history-next').fire('click');await p.settle();assert.match(p.requests.at(-1).url,/offset=50/);assert.match(p.requests.at(-1).url,/cursor=150/);assert.equal(p.timers.size,0);assert.equal(p.find('history-pause').disabled,true);assert.match(p.find('history-status').textContent,/Older snapshot/);
 p.data.offset=0;await p.find('history-refresh').fire('click');await p.settle();assert.match(p.requests.at(-1).url,/offset=0/);assert.doesNotMatch(p.requests.at(-1).url,/cursor=/);assert.equal(p.timers.size,1);
});
test('pause, view closure and hidden tabs stop pending reads and discard late responses',async()=>{
 const p=page({defer:true});await p.settle();await p.view(true);assert.equal(p.requests[0].opts.signal.aborted,true);p.pending[0].resolve({json:async()=>p.data});await p.settle();assert.equal(p.find('history-rows').children.length,0);assert.equal(p.timers.size,0);
 await p.view(false);await p.visibility(true);assert.equal(p.requests[1].opts.signal.aborted,true);await p.visibility(false);assert.equal(p.requests.length,3);
 const paused=page();await paused.settle();await paused.find('history-pause').fire('click');assert.equal(paused.timers.size,0);await paused.find('history-refresh').fire('click');await paused.settle();assert.equal(paused.requests.length,2);assert.equal(paused.timers.size,0);
});
test('host changes close details, clear the old rows and ignore the former host response',async()=>{
 const p=page({defer:true});await p.settle();await p.host('b'.repeat(64));assert.equal(p.requests[0].opts.signal.aborted,true);p.pending[0].resolve({json:async()=>p.data});await p.settle();assert.equal(p.find('history-rows').children.length,0);assert.match(p.requests[1].url,new RegExp('host='+'b'.repeat(64)));p.pending[1].resolve({json:async()=>p.data});await p.settle();assert.equal(p.find('history-rows').children.length,1);
});
test('saved filters and older-page cursor are used in the first request and explicit filters reset the page',async()=>{
 const saved={view:'history',host:'local',historyOffset:50,historyCursor:'99',filters:{'history-search':'db','history-scope':'volume','history-status-filter':'failed','history-hosts':'all'}};
 const p=page({saved});p.data.offset=50;p.data.total=80;await p.settle();assert.match(p.requests[0].url,/host=all/);assert.match(p.requests[0].url,/offset=50/);assert.match(p.requests[0].url,/status=failed/);assert.match(p.requests[0].url,/cursor=99/);assert.equal(p.window.reefHistoryOffset,50);p.find('history-search').value='new';p.data.offset=0;await p.find('history-search').fire('input');await p.tick();assert.match(p.requests.at(-1).url,/offset=0/);assert.doesNotMatch(p.requests.at(-1).url,/cursor=/);
});
test('persistence and queue loss warnings remain explicit; request failure preserves visibly stale results',async()=>{
 const p=page({defer:true});await p.settle();p.pending[0].resolve({json:async()=>({...p.data,persistence_error:true,dropped:3})});await p.settle();assert.equal(p.find('history-warning').hidden,false);assert.match(p.find('history-warning').textContent,/3 local metadata/);assert.match(p.find('history-warning').textContent,/Actions still work/);
 const reload=p.find('history-refresh').fire('click');await p.settle();p.pending[1].reject(Error('History unavailable'));await reload;await p.settle();assert.equal(p.find('history-rows').children.length,1);assert.match(p.find('history-error').textContent,/previous results/);assert.equal(p.find('history-loading').hidden,true);
});
