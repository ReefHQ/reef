'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const source=fs.readFileSync(require('node:path').join(__dirname,'../web/networks.js'),'utf8');
const network=(name='app-data',overrides={})=>({id:require('node:crypto').createHash('sha256').update(name).digest('hex'),name,mutable:true,subnets:['172.30.0.0/16'],gateways:['172.30.0.1'],driver:'bridge',scope:'local',project:'app',created:'2026-10-27',mountpoint:'/docker/networks/'+name+'/_data',fingerprint:'a'.repeat(64),containers:[],removable:true,...overrides});
function page({host='local',capabilities=[],rows=[network()],defer=false,report={},containers=[],confirm=async()=>true}={}){
 const nodes=new Map(),events={},requests=[],timers=[],pending=[];
 class Node{
  constructor(tag='div'){this.tagName=tag;this.value='';this.hidden=false;this.disabled=false;this.dataset={};this.children=[];this.listeners={};this.checked=false;this.textContent='';}
  addEventListener(type,fn){(this.listeners[type]??=[]).push(fn);}async fire(type){for(const fn of this.listeners[type]||[])await fn({target:this,preventDefault(){}});}
  append(...nodes){this.children.push(...nodes);}replaceChildren(...nodes){this.children=nodes;}setAttribute(){}focus(){}showModal(){this.open=true;}close(){this.open=false;}
 }
 const find=id=>{if(!nodes.has(id))nodes.set(id,new Node());return nodes.get(id);};
 const window={reefHost:host,reefHostName:host,reefCapabilities:capabilities,addEventListener(type,fn){(events[type]??=[]).push(fn);},dispatchEvent(event){for(const fn of events[event.type]||[])fn(event);}};
 const context={window,document:{getElementById:find,createElement:tag=>new Node(tag),addEventListener(){}},CustomEvent:function(type){this.type=type;},Option:function(text,value){this.text=text;this.value=value;},URLSearchParams,csrf:'fixture-csrf',textNode:(tag,value)=>Object.assign(new Node(tag),{textContent:value}),replaceKeepingFocus:(parent,nodes)=>parent.replaceChildren(...nodes),ask:confirm,setTimeout:fn=>{timers.push(fn);return timers.length;},clearTimeout(){},request:async(url,opts,host)=>{requests.push({url,opts,host});if(defer&&url==='/api/networks')return await new Promise(resolve=>pending.push(resolve));return {json:async()=>url==='/api/networks'?{networks:rows,containers,operation:report}:url==='/api/networks/operation'?report:{id:'worker',action:'create',running:true,results:[]}};}};
 vm.runInNewContext(fs.readFileSync(require('node:path').join(__dirname,'../web/operation-panels.js'),'utf8'),context);
 vm.runInNewContext(source,context);
 return {nodes,window,requests,find,pending,timers,async settle(){for(let i=0;i<20;i++)await Promise.resolve();},async host(host,capabilities=[]){window.reefHost=host;window.reefCapabilities=capabilities;window.dispatchEvent({type:'reef-host-changed'});await this.settle();}};
}
test('network details render only metadata as text and stop deletion for used or unidentified networks',async()=>{
 const p=page({rows:[network('app-data',{project:'<script>bad</script>'}),network('database',{removable:false,containers:['stopped-db'],retain_reason:'Used by a running or stopped container'}),network('plugin-data',{removable:false,retain_reason:'Creation identity unavailable'})]});await p.settle();
 const rows=p.find('networks-rows').children;assert.equal(rows.length,3);assert.equal(rows[0].children[3].textContent,'<script>bad</script>');assert.equal(rows[1].children[0].children[0].disabled,true);assert.equal(rows[2].children[5].children[0].disabled,true);
 await rows[1].children[1].children[0].fire('click');assert.equal(p.find('network-details').open,true);assert.equal(p.find('network-detail-delete').disabled,true);assert.ok(p.find('network-detail-fields').children.some(n=>n.textContent==='stopped-db'));
});
test('remote creation pins host and uses CSRF plus explicit confirmation',async()=>{
 const old=page({host:'remote',capabilities:['control-v1','images-v1']});await old.settle();assert.equal(old.requests.length,0);assert.equal(old.find('networks-create-submit').disabled,true);
 const p=page({host:'ego',capabilities:['control-v1','networks-v1']});await p.settle();p.find('networks-name').value='new-data';await p.find('networks-create').fire('submit');await p.settle();const call=p.requests.at(-1);assert.equal(call.host,'ego');assert.equal(call.url,'/api/networks/action');assert.equal(call.opts.method,'POST');assert.equal(call.opts.body.get('csrf'),'fixture-csrf');assert.equal(call.opts.body.get('confirmed'),'yes');assert.equal(call.opts.body.get('action'),'create');assert.equal(call.opts.body.get('name'),'new-data');
});
test('cleanup reviews exact removable networks and preserves running or stopped references',async()=>{
 let message='';const p=page({rows:[network('old-data'),network('db',{removable:false,containers:['stopped-db']}),network('plugin',{removable:false})],confirm:async text=>{message=text;return true;}});await p.settle();await p.find('networks-cleanup').fire('click');await p.settle();const body=p.requests.at(-1).opts.body;assert.equal(body.get('action'),'cleanup');assert.deepEqual(JSON.parse(body.get('targets')),[{id:network('old-data').id,fingerprint:'a'.repeat(64)}]);assert.match(message,/Delete/);assert.match(message,/running or stopped/);assert.ok(message.includes('old-data'));assert.ok(message.includes('local'));
});
test('switching hosts while confirmation waits discards the former host operation',async()=>{
 let approve;const p=page({confirm:()=>new Promise(resolve=>approve=resolve)});await p.settle();p.find('networks-name').value='new-data';const promise=p.find('networks-create').fire('submit');await p.settle();await p.host('ego',['networks-v1']);approve(true);await promise;await p.settle();assert.equal(p.requests.filter(r=>r.opts?.method==='POST').length,0);
});
test('late inventory from another host is discarded and selection stays isolated',async()=>{
 const p=page({defer:true});await p.settle();assert.equal(p.pending.length,1);assert.equal(p.find('networks-loading').hidden,false);await p.host('ego',['networks-v1']);p.pending[0]({json:async()=>({networks:[network()],operation:{}})});await p.settle();assert.equal(p.find('networks-rows').children.length,0);assert.equal(p.window.reefNetworksReady,false);assert.equal(p.find('networks-loading').hidden,false);const fresh=p.find('networks-refresh').fire('click');await p.settle();p.pending[1]({json:async()=>({networks:[],operation:{}})});await fresh;await p.settle();assert.equal(p.find('networks-loading').hidden,true);const failed=p.find('networks-refresh').fire('click');await p.settle();p.pending[2]({json:async()=>{throw Error('Inventory unavailable');}});await failed;await p.settle();assert.equal(p.find('networks-loading').hidden,true);assert.equal(p.find('networks-error').hidden,false);
});
test('batch selection excludes referenced networks and cancellation never sends deletion',async()=>{
 const p=page({rows:[network('first'),network('second',{id:'3'.repeat(64)}),network('used',{removable:false,containers:['stopped']})]});await p.settle();p.find('networks-select-all').checked=true;await p.find('networks-select-all').fire('change');await p.find('networks-delete').fire('click');await p.settle();assert.equal(JSON.parse(p.requests.at(-1).opts.body.get('targets')).length,2);
 const cancel=page({confirm:async()=>false});await cancel.settle();await cancel.find('networks-cleanup').fire('click');await cancel.settle();assert.equal(cancel.requests.filter(r=>r.opts?.method==='POST').length,0);
});

test('network creation uses a browser-compatible pattern accepting Docker network names',()=>{
 const html=fs.readFileSync(require('node:path').join(__dirname,'../web/index.html'),'utf8');const input=html.match(/<input id="networks-name"[^>]+>/)[0];const pattern=input.match(/pattern="([^"]+)"/)[1],valid=new RegExp('^(?:'+pattern+')$','v');
 assert.ok(valid.test('application-data'));assert.ok(valid.test('Project_data.1'));assert.equal(valid.test('../data'),false);assert.equal(valid.test('--help'),false);
});

test('successful network cleanup hides its completed panel on every refresh',async()=>{
 const report={id:'network-cleanup',action:'cleanup',running:true,total:1,results:[]},p=page({report});await p.settle();assert.equal(p.find('networks-operation').hidden,false);
 report.running=false;report.results=[{id:'old-data',ok:true,message:'Removed'}];await p.timers.at(-1)();assert.equal(p.find('networks-operation').hidden,true);await p.timers.at(-1)();assert.equal(p.find('networks-operation').hidden,true);
});
test('network cleanup errors remain readable until dismissed, without returning on a poll',async()=>{
 const report={id:'network-cleanup',action:'cleanup',running:false,total:1,results:[{id:'old-data',ok:false,message:'Network retained'}]},p=page({report});await p.settle();assert.equal(p.find('networks-operation').hidden,false);await p.find('networks-operation').children[0].children.at(-1).fire('click');await p.timers.at(-1)();assert.equal(p.find('networks-operation').hidden,true);
});

 test('connection dialog pins immutable IDs, protects administration containers and requires explicit confirmation',async()=>{
 let message='';const row=network(),containers=[{id:'a'.repeat(64),name:'web',state:'running',mode:'default',networks:{}},{id:'b'.repeat(64),name:'reef',state:'running',protected:true,networks:{}},{id:'c'.repeat(64),name:'stopped',state:'exited',networks:{}}];
 const p=page({rows:[row],containers,confirm:async text=>{message=text;return true;}});await p.settle();await p.find('networks-rows').children[0].children[1].children[0].fire('click');
 assert.equal(p.find('network-container').children.length,2);assert.equal(p.find('network-connect').disabled,true);
 p.find('network-container').value=containers[0].id;p.find('network-container').onchange();assert.equal(p.find('network-connect').disabled,false);assert.equal(p.find('network-disconnect').disabled,true);
 p.find('network-connect').onclick();await p.settle();const call=p.requests.at(-1);assert.equal(call.opts.body.get('action'),'connect');assert.equal(call.opts.body.get('container'),containers[0].id);assert.deepEqual(JSON.parse(call.opts.body.get('targets')),[{id:row.id,fingerprint:row.fingerprint}]);assert.match(message,/does not edit Compose/);
 });
 test('disconnect targets only attached containers and stale dialogs cannot mutate a replaced host',async()=>{
 const row=network(),containers=[{id:'a'.repeat(64),name:'web',state:'running',networks:{app:row.id}}];const p=page({rows:[row],containers});await p.settle();await p.find('networks-rows').children[0].children[1].children[0].fire('click');p.find('network-container').value=containers[0].id;p.find('network-container').onchange();assert.equal(p.find('network-connect').disabled,true);assert.equal(p.find('network-disconnect').disabled,false);const stale=p.find('network-disconnect').onclick;await p.host('other',['networks-v1']);stale();await p.settle();assert.equal(p.requests.filter(r=>r.opts?.method==='POST').length,0);
 });
