'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const source=fs.readFileSync(require('node:path').join(__dirname,'../web/volumes.js'),'utf8');
const volume=(name='app-data',overrides={})=>({name,driver:'local',scope:'local',project:'app',created:'2026-10-27',mountpoint:'/docker/volumes/'+name+'/_data',fingerprint:'a'.repeat(64),containers:[],removable:true,...overrides});
function page({host='local',capabilities=[],rows=[volume()],defer=false,report={},confirm=async()=>true}={}){
 const nodes=new Map(),events={},requests=[],timers=[],pending=[];
 class Node{
  constructor(tag='div'){this.tagName=tag;this.value='';this.hidden=false;this.disabled=false;this.dataset={};this.children=[];this.listeners={};this.checked=false;this.textContent='';}
  addEventListener(type,fn){(this.listeners[type]??=[]).push(fn);}async fire(type){for(const fn of this.listeners[type]||[])await fn({target:this,preventDefault(){}});}
  append(...nodes){this.children.push(...nodes);}replaceChildren(...nodes){this.children=nodes;}setAttribute(){}focus(){}showModal(){this.open=true;}close(){this.open=false;}
 }
 const find=id=>{if(!nodes.has(id))nodes.set(id,new Node());return nodes.get(id);};
 const window={reefHost:host,reefHostName:host,reefCapabilities:capabilities,addEventListener(type,fn){(events[type]??=[]).push(fn);},dispatchEvent(event){for(const fn of events[event.type]||[])fn(event);}};
 const context={window,document:{getElementById:find,createElement:tag=>new Node(tag),addEventListener(){}},CustomEvent:function(type){this.type=type;},URLSearchParams,csrf:'fixture-csrf',textNode:(tag,value)=>Object.assign(new Node(tag),{textContent:value}),replaceKeepingFocus:(parent,nodes)=>parent.replaceChildren(...nodes),ask:confirm,setTimeout:fn=>{timers.push(fn);return timers.length;},clearTimeout(){},request:async(url,opts,host)=>{requests.push({url,opts,host});if(defer&&url==='/api/volumes')return await new Promise(resolve=>pending.push(resolve));return {json:async()=>url==='/api/volumes'?{volumes:rows,operation:report}:url==='/api/volumes/operation'?report:{id:'worker',action:'create',running:true,results:[]}};}};
 vm.runInNewContext(fs.readFileSync(require('node:path').join(__dirname,'../web/operation-panels.js'),'utf8'),context);
 vm.runInNewContext(source,context);
 return {nodes,window,requests,find,pending,timers,async settle(){for(let i=0;i<20;i++)await Promise.resolve();},async host(host,capabilities=[]){window.reefHost=host;window.reefCapabilities=capabilities;window.dispatchEvent({type:'reef-host-changed'});await this.settle();}};
}
test('volume details render only metadata as text and stop deletion for used or unidentified volumes',async()=>{
 const p=page({rows:[volume('app-data',{project:'<script>bad</script>'}),volume('database',{removable:false,containers:['stopped-db'],retain_reason:'Used by a running or stopped container'}),volume('plugin-data',{removable:false,retain_reason:'Creation identity unavailable'})]});await p.settle();
 const rows=p.find('volumes-rows').children;assert.equal(rows.length,3);assert.equal(rows[0].children[3].textContent,'<script>bad</script>');assert.equal(rows[1].children[0].children[0].disabled,true);assert.equal(rows[2].children[5].children[0].disabled,true);
 await rows[1].children[1].children[0].fire('click');assert.equal(p.find('volume-details').open,true);assert.equal(p.find('volume-detail-delete').disabled,true);assert.ok(p.find('volume-detail-fields').children.some(n=>n.textContent==='stopped-db'));
});
test('remote creation pins host and uses CSRF plus explicit confirmation',async()=>{
 const old=page({host:'remote',capabilities:['control-v1','images-v1']});await old.settle();assert.equal(old.requests.length,0);assert.equal(old.find('volumes-create-submit').disabled,true);
 const p=page({host:'ego',capabilities:['control-v1','volumes-v1']});await p.settle();p.find('volumes-name').value='new-data';await p.find('volumes-create').fire('submit');await p.settle();const call=p.requests.at(-1);assert.equal(call.host,'ego');assert.equal(call.url,'/api/volumes/action');assert.equal(call.opts.method,'POST');assert.equal(call.opts.body.get('csrf'),'fixture-csrf');assert.equal(call.opts.body.get('confirmed'),'yes');assert.equal(call.opts.body.get('action'),'create');assert.equal(call.opts.body.get('name'),'new-data');
});
test('cleanup reviews exact removable volumes and warns about permanent data loss',async()=>{
 let message='';const p=page({rows:[volume('old-data'),volume('db',{removable:false,containers:['stopped-db']}),volume('plugin',{removable:false})],confirm:async text=>{message=text;return true;}});await p.settle();await p.find('volumes-cleanup').fire('click');await p.settle();const body=p.requests.at(-1).opts.body;assert.equal(body.get('action'),'cleanup');assert.deepEqual(JSON.parse(body.get('targets')),[{name:'old-data',fingerprint:'a'.repeat(64)}]);assert.match(message,/Permanently delete/);assert.match(message,/ALL DATA/);assert.match(message,/no undo/);assert.ok(message.includes('old-data'));assert.ok(message.includes('local'));
});
test('switching hosts while confirmation waits discards the former host operation',async()=>{
 let approve;const p=page({confirm:()=>new Promise(resolve=>approve=resolve)});await p.settle();p.find('volumes-name').value='new-data';const promise=p.find('volumes-create').fire('submit');await p.settle();await p.host('ego',['volumes-v1']);approve(true);await promise;await p.settle();assert.equal(p.requests.filter(r=>r.opts?.method==='POST').length,0);
});
test('late inventory from another host is discarded and selection stays isolated',async()=>{
 const p=page({defer:true});await p.settle();assert.equal(p.pending.length,1);assert.equal(p.find('volumes-loading').hidden,false);await p.host('ego',['volumes-v1']);p.pending[0]({json:async()=>({volumes:[volume()],operation:{}})});await p.settle();assert.equal(p.find('volumes-rows').children.length,0);assert.equal(p.window.reefVolumesReady,false);assert.equal(p.find('volumes-loading').hidden,false);const fresh=p.find('volumes-refresh').fire('click');await p.settle();p.pending[1]({json:async()=>({volumes:[],operation:{}})});await fresh;await p.settle();assert.equal(p.find('volumes-loading').hidden,true);const failed=p.find('volumes-refresh').fire('click');await p.settle();p.pending[2]({json:async()=>{throw Error('Inventory unavailable');}});await failed;await p.settle();assert.equal(p.find('volumes-loading').hidden,true);assert.equal(p.find('volumes-error').hidden,false);
});
test('batch selection excludes referenced volumes and cancellation never sends deletion',async()=>{
 const p=page({rows:[volume('first'),volume('second'),volume('used',{removable:false,containers:['stopped']})]});await p.settle();p.find('volumes-select-all').checked=true;await p.find('volumes-select-all').fire('change');await p.find('volumes-delete').fire('click');await p.settle();assert.equal(JSON.parse(p.requests.at(-1).opts.body.get('targets')).length,2);
 const cancel=page({confirm:async()=>false});await cancel.settle();await cancel.find('volumes-cleanup').fire('click');await cancel.settle();assert.equal(cancel.requests.filter(r=>r.opts?.method==='POST').length,0);
});

test('volume creation uses a browser-compatible pattern accepting Docker volume names',()=>{
 const html=fs.readFileSync(require('node:path').join(__dirname,'../web/index.html'),'utf8');const input=html.match(/<input id="volumes-name"[^>]+>/)[0];const pattern=input.match(/pattern="([^"]+)"/)[1],valid=new RegExp('^(?:'+pattern+')$','v');
 assert.ok(valid.test('application-data'));assert.ok(valid.test('Project_data.1'));assert.equal(valid.test('../data'),false);assert.equal(valid.test('--help'),false);
});

test('successful volume cleanup hides its completed panel on every refresh',async()=>{
 const report={id:'volume-cleanup',action:'cleanup',running:true,total:1,results:[]},p=page({report});await p.settle();assert.equal(p.find('volumes-operation').hidden,false);
 report.running=false;report.results=[{id:'old-data',ok:true,message:'Removed'}];await p.timers.at(-1)();assert.equal(p.find('volumes-operation').hidden,true);await p.timers.at(-1)();assert.equal(p.find('volumes-operation').hidden,true);
});
test('volume cleanup errors remain readable until dismissed, without returning on a poll',async()=>{
 const report={id:'volume-cleanup',action:'cleanup',running:false,total:1,results:[{id:'old-data',ok:false,message:'Volume retained'}]},p=page({report});await p.settle();assert.equal(p.find('volumes-operation').hidden,false);await p.find('volumes-operation').children[0].children.at(-1).fire('click');await p.timers.at(-1)();assert.equal(p.find('volumes-operation').hidden,true);
});

test('viewer can inspect volumes but cannot select, create or delete them',async()=>{const p=page();p.window.reefAccess={canWrite:false};await p.settle();assert.equal(p.find('volumes-refresh').disabled,false);assert.equal(p.find('volumes-create-submit').disabled,true);assert.equal(p.find('volumes-cleanup').disabled,true);const row=p.find('volumes-rows').children[0];assert.equal(row.children[0].children[0].disabled,true);await row.children[1].children[0].fire('click');assert.equal(p.find('volume-detail-delete').disabled,true)});
