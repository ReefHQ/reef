'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const source=fs.readFileSync(require('node:path').join(__dirname,'../web/builds.js'),'utf8');
const record=(id,extra={})=>({id,size:2048,type:'regular',created:'2026-10-27',last_used:'2026-10-27',fingerprint:'a'.repeat(64),reclaimable:true,mutable:false,shared:false,...extra});
function page({host='local',capabilities=[],confirm=async()=>true,history=[],cache=[record('cachefixture001')],cacheOperation={}}={}){
 const nodes=new Map(),events={},requests=[],timers=[];let state=[{id:'test',name:'Application',busy:false,operation:{}}];
 class Node{
  constructor(tag='div'){this.tagName=tag;this.value='';this.hidden=false;this.disabled=false;this.checked=false;this.dataset={};this.children=[];this.listeners={};this.textContent='';this.open=false;this.scrollHeight=0;this.scrollTop=0;this.clientHeight=0;}
  addEventListener(type,fn){(this.listeners[type]??=[]).push(fn);}async fire(type){for(const fn of this.listeners[type]||[])await fn({target:this,preventDefault(){}});}append(...nodes){this.children.push(...nodes);}replaceChildren(...nodes){this.children=nodes;}setAttribute(name,value){this[name]=value;}showModal(){this.open=true;}close(){this.open=false;for(const fn of this.listeners.close||[])fn();}
 }
 const find=id=>{if(!nodes.has(id))nodes.set(id,new Node());return nodes.get(id);};
 const window={reefHost:host,reefHostName:host,reefCapabilities:capabilities,addEventListener(type,fn){(events[type]??=[]).push(fn);},dispatchEvent(event){for(const fn of events[event.type]||[])fn(event);}};
 const context={window,document:{getElementById:find,createElement:tag=>new Node(tag),addEventListener(){}},Option:function(label,value){this.textContent=label;this.value=value;},CustomEvent:function(type){this.type=type;},URLSearchParams,csrf:'test-csrf',textNode:(tag,text)=>Object.assign(new Node(tag),{textContent:text}),replaceKeepingFocus:(parent,nodes)=>parent.replaceChildren(...nodes),ask:confirm,setTimeout:fn=>{timers.push(fn);return timers.length;},clearTimeout(){},request:async(path,opts,chosenHost)=>{requests.push({path,opts,host:chosenHost});return {json:async()=>path==='/api/builds'?state:path.startsWith('/api/builds/project')?{revision:'saved-revision',services:['web']}:path.startsWith('/api/builds/history')?history:path.startsWith('/api/builds/log')?{running:true,phase:'build',log:'<script>build output</script>'}:path==='/api/builds/cache'?{records:cache,operation:cacheOperation}:path==='/api/builds/cache/operation'?cacheOperation:path==='/api/builds/cache/action'?{id:'cache-op',action:'cache cleanup',running:true,total:1,results:[]}:{}};}};
 vm.runInNewContext(source,context);
 return {nodes,find,window,requests,timers,setState:value=>state=value,async settle(){for(let i=0;i<30;i++)await Promise.resolve();},async select(){find('build-project').value='test';await find('build-project').fire('change');await this.settle();},async host(id,capabilities=[]){window.reefHost=id;window.reefCapabilities=capabilities;window.dispatchEvent({type:'reef-host-changed'});await this.settle();}};
}
test('build uses saved revision and explicit options on the selected host without any editor content',async()=>{
 const p=page({host:'ego',capabilities:['control-v1','builds-v1']});await p.settle();await p.select();p.find('build-service').value='web';p.find('build-no-cache').checked=true;p.find('build-pull').checked=true;await p.find('build-form').fire('submit');await p.settle();const call=p.requests.find(r=>r.path==='/api/builds/action');assert.equal(call.host,'ego');assert.equal(call.opts.body.get('csrf'),'test-csrf');assert.equal(call.opts.body.get('confirmed'),'yes');assert.equal(call.opts.body.get('revision'),'saved-revision');assert.equal(call.opts.body.get('service'),'web');assert.equal(call.opts.body.get('no_cache'),'yes');assert.equal(call.opts.body.get('pull'),'yes');assert.equal(call.opts.body.has('content'),false);
});
test('an old agent cannot run Builds and a confirmation from a replaced host is discarded',async()=>{
 const old=page({host:'old',capabilities:['control-v1']});await old.settle();assert.equal(old.requests.length,0);assert.equal(old.find('build-submit').disabled,true);assert.equal(old.window.reefBuildsReady,true);
 let approve;const p=page({confirm:()=>new Promise(resolve=>approve=resolve)});await p.settle();await p.select();const pending=p.find('build-form').fire('submit');await p.settle();await p.host('ego',['builds-v1']);approve(true);await pending;assert.equal(p.requests.filter(r=>r.path==='/api/builds/action').length,0);
});
test('cache selection excludes mutable and in-use records and submits exact IDs and fingerprints',async()=>{
 const p=page({cache:[record('cachefixture001'),record('cachefixture002',{mutable:true}),record('cachefixture003',{reclaimable:false})]});await p.settle();await p.find('build-cache-refresh').fire('click');const rows=p.find('build-cache-rows').children;assert.equal(rows[1].children[0].children[0].disabled,true);assert.equal(rows[2].children[0].children[0].disabled,true);await p.find('build-cache-cleanup').fire('click');const call=p.requests.find(r=>r.path==='/api/builds/cache/action');assert.deepEqual(JSON.parse(call.opts.body.get('targets')),[{id:'cachefixture001',fingerprint:'a'.repeat(64)}]);assert.equal(call.opts.body.get('confirmed'),'yes');
});
test('build logs render plain text and polling keeps the selected form and its options',async()=>{
 const op={id:'a'.repeat(48),build:{service:'web'},started:'2026-10-27',phase:'completed',result:'Done'};const p=page({history:[op]});await p.settle();await p.select();p.find('build-no-cache').checked=true;const pickerOptions=p.find('build-project').children;await p.find('builds-rows').children[0].children[4].children[0].fire('click');assert.equal(p.find('build-log-output').textContent,'<script>build output</script>');await p.timers.at(-1)();assert.equal(p.find('build-no-cache').checked,true);assert.equal(p.find('build-project').children,pickerOptions);assert.equal(p.find('build-log-dialog').open,true);
});
test('cancelled build/cache confirmations never send mutations',async()=>{
 const p=page({confirm:async()=>false});await p.settle();await p.select();await p.find('build-form').fire('submit');await p.find('build-cache-refresh').fire('click');await p.find('build-cache-cleanup').fire('click');assert.equal(p.requests.filter(r=>r.opts?.method==='POST').length,0);
});

test('successful cache cleanup hides its completed panel without resetting its form',async()=>{
 const cacheOperation={id:'cache-cleanup',action:'cache cleanup',running:true,total:1,results:[]},p=page({cacheOperation});await p.settle();await p.select();p.find('build-service').value='web';p.find('build-no-cache').checked=true;await p.find('build-cache-refresh').fire('click');assert.equal(p.find('build-cache-operation').hidden,false);
 cacheOperation.running=false;cacheOperation.results=[{id:'cachefixture001',ok:true,message:'Removed'}];await p.timers.at(-1)();assert.equal(p.find('build-cache-operation').hidden,true);await p.timers.at(-1)();assert.equal(p.find('build-cache-operation').hidden,true);assert.equal(p.find('build-service').value,'web');assert.equal(p.find('build-no-cache').checked,true);
});
test('cache cleanup retains incomplete results until dismissed and later polls do not reopen them',async()=>{
 const cacheOperation={id:'cache-cleanup',action:'cache cleanup',running:false,total:2,results:[{id:'cachefixture001',ok:true,message:'Removed'}]},p=page({cacheOperation});await p.settle();await p.find('build-cache-refresh').fire('click');assert.equal(p.find('build-cache-operation').hidden,false);await p.find('build-cache-operation').children.at(-1).fire('click');await p.timers.at(-1)();assert.equal(p.find('build-cache-operation').hidden,true);
});
