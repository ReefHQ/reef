
'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const source=fs.readFileSync(require('node:path').join(__dirname,'../web/alerts.js'),'utf8');
const incident=(extra={})=>({id:'1'.repeat(48),host_id:'local',host:'Local',kind:'unhealthy',target:'database',status:'firing',opened:'2026-10-28T17:00:00Z',updated:'2026-10-28T17:00:00Z',...extra});
const state=()=>({config:{enabled:true,disconnect_seconds:120,unhealthy_seconds:120,restart_count:3,restart_window_seconds:300,recovery_seconds:60},incidents:[incident()],hosts:[{host_id:'local',host:'Local',status:'ready',checked_at:'2026-10-28T17:00:00Z',containers:3,failed:0}],pending:0,persistence_error:false,checked_at:'2026-10-28T17:00:00Z'});
function page({host='local',hidden=false,defer=false,saved=null,data=state()}={}){
 const nodes=new Map(),events={},documentEvents={},requests=[],pending=[],timers=new Map();let nextTimer=0;
 class Node{constructor(){this.value='';this.dataset={};this.hidden=false;this.disabled=false;this.children=[];this.listeners={};this.attrs={};this.textContent='';}addEventListener(type,fn){(this.listeners[type]??=[]).push(fn);}async fire(type){for(const fn of this.listeners[type]||[])await fn({target:this,preventDefault(){}});}append(...items){this.children.push(...items);}replaceChildren(...items){this.children=items;}setAttribute(key,value){this.attrs[key]=value;}showModal(){this.open=true;}close(){this.open=false;}}
 const find=id=>{if(!nodes.has(id))nodes.set(id,new Node());return nodes.get(id);};find('alerts-view').hidden=hidden;
 const window={reefHost:host,ReefNavigation:saved?{read:()=>saved}:undefined,addEventListener(type,fn){(events[type]??=[]).push(fn);},dispatchEvent(event){for(const fn of events[event.type]||[])fn(event);}};
 const document={hidden:false,getElementById:find,createElement:()=>new Node(),addEventListener(type,fn){(documentEvents[type]??=[]).push(fn);}};
 const context={window,document,csrf:"csrf",ask:async()=>true,AbortController,URLSearchParams,CustomEvent:function(type){this.type=type;},replaceKeepingFocus:(parent,nodes)=>parent.replaceChildren(...nodes),setTimeout(fn){const id=++nextTimer;timers.set(id,fn);return id;},clearTimeout(id){timers.delete(id);},request:async(url,opts,requestHost)=>{requests.push({url,opts,host:requestHost});if(defer)return new Promise((resolve,reject)=>pending.push({resolve,reject}));return {json:async()=>data};}};
 vm.runInNewContext(source,context);
 return {find,window,document,requests,pending,timers,data,async settle(){for(let i=0;i<30;i++)await Promise.resolve();},async tick(){const entry=[...timers].at(-1);if(entry){timers.delete(entry[0]);await entry[1]();}await this.settle();},async view(hidden){find('alerts-view').hidden=hidden;window.dispatchEvent({type:'reef-view-changed'});await this.settle();},async host(id){window.reefHost=id;window.dispatchEvent({type:'reef-host-changed',detail:{id}});await this.settle();},async visibility(hidden){document.hidden=hidden;for(const fn of documentEvents.visibilitychange||[])fn();await this.settle();}};
}
const text=node=>[node.textContent||'',...node.children.map(text)].join(' ');

test('alerts loads lazily, shows coverage and incidents, and preserves policy drafts on refresh',async()=>{
 const p=page({hidden:true});await p.settle();assert.equal(p.requests.length,0);await p.view(false);assert.match(text(p.find('alerts-rows')),/Firing/);assert.match(text(p.find('alerts-host-rows')),/Collecting/);p.find('alerts-unhealthy').value='240';await p.tick();assert.equal(p.find('alerts-unhealthy').value,'240');assert.equal(p.find('alerts-enabled').checked,true);
});
test('all-host filtering remains central and incident pages are bounded',async()=>{
 const data=state();data.incidents=Array.from({length:70},(_,i)=>incident({id:String(i),host_id:'a'.repeat(64),host:'Remote',target:'<script>'+i}));const p=page({host:'a'.repeat(64),data});await p.settle();assert.equal(p.requests[0].host,'local');assert.equal(p.find('alerts-rows').children.length,50);assert.match(text(p.find('alerts-rows')),/<script>/);await p.find('alerts-next').fire('click');assert.equal(p.find('alerts-rows').children.length,20);await p.host('local');assert.equal(p.find('alerts-rows').children.length,0);p.find('alerts-scope').value='all';await p.find('alerts-scope').fire('change');assert.equal(p.find('alerts-rows').children.length,50);
});
test('view/tab closure aborts reads and ignores late results',async()=>{
 const p=page({defer:true});await p.settle();await p.view(true);assert.equal(p.requests[0].opts.signal.aborted,true);p.pending[0].resolve({json:async()=>p.data});await p.settle();assert.equal(p.find('alerts-rows').children.length,0);assert.equal(p.timers.size,0);await p.view(false);await p.visibility(true);assert.equal(p.requests[1].opts.signal.aborted,true);
});
test('policy saves explicit confirmed central settings and does not run a Docker action',async()=>{
 const p=page();await p.settle();p.find('alerts-enabled').checked=false;await p.find('alerts-form').fire('submit');await p.settle();const request=p.requests.find(r=>r.opts.method==='POST');assert.equal(request.host,'local');assert.equal(request.url,'/api/monitoring');assert.equal(request.opts.body.get('csrf'),'csrf');assert.equal(request.opts.body.get('confirmed'),'yes');assert.equal(request.opts.body.get('enabled'),'false');assert.equal(request.opts.body.get('restart_count'),'3');
});
test('collection gaps, capacity and persistence failures remain explicit',async()=>{
 const data=state();data.hosts[0].status='collection_unavailable';data.persistence_error=true;const p=page({data});await p.settle();assert.match(text(p.find('alerts-host-rows')),/outcome unknown/);assert.equal(p.find('alerts-warning').hidden,false);assert.match(p.find('alerts-warning').textContent,/persisted/);data.capacity_limited=true;await p.tick();assert.match(p.find('alerts-warning').textContent,/capacity/);
});
test('failed reads retain stale rows and do not overwrite policy inputs',async()=>{
 const p=page({defer:true});await p.settle();p.pending.shift().resolve({json:async()=>p.data});await p.settle();p.find('alerts-restarts').value='9';const read=p.tick();await p.settle();p.pending.shift().reject(Error('unavailable'));await read;assert.match(text(p.find('alerts-rows')),/database/);assert.match(p.find('alerts-error').textContent,/stale/);assert.equal(p.find('alerts-restarts').value,'9');
});
