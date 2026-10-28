'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const source=fs.readFileSync(require('node:path').join(__dirname,'../web/images.js'),'utf8');
const image=(id='a',overrides={})=>({id:'sha256:'+id.repeat(64),tags:['example/app:'+id],digests:[],fingerprint:id.repeat(64),containers:[],size:2048,os:'linux',architecture:'amd64',created:'2026-10-27',...overrides});
function page({host='local',capabilities=[],rows=[image()],defer=false,report={},confirm=async()=>true}={}){
 const nodes=new Map(),events={},requests=[],timers=[],pending=[];
 class Node{
  constructor(tag='div'){this.tagName=tag;this.value='';this.hidden=false;this.disabled=false;this.dataset={};this.children=[];this.listeners={};this.checked=false;this.textContent='';}
  addEventListener(type,fn){(this.listeners[type]??=[]).push(fn);}async fire(type){for(const fn of this.listeners[type]||[])await fn({target:this,preventDefault(){}});}
  append(...nodes){this.children.push(...nodes);}replaceChildren(...nodes){this.children=nodes;}setAttribute(){}focus(){}showModal(){this.open=true;}close(){this.open=false;}
 }
 const find=id=>{if(!nodes.has(id))nodes.set(id,new Node());return nodes.get(id);};find('images-cleanup-mode').value='untagged';
 const window={reefHost:host,reefHostName:host,reefCapabilities:capabilities,addEventListener(type,fn){(events[type]??=[]).push(fn);},dispatchEvent(event){for(const fn of events[event.type]||[])fn(event);}};
 const context={window,document:{getElementById:find,createElement:tag=>new Node(tag),addEventListener(){}},CustomEvent:function(type){this.type=type;},URLSearchParams,csrf:'fixture-csrf',textNode:(tag,value)=>Object.assign(new Node(tag),{textContent:value}),replaceKeepingFocus:(parent,nodes)=>parent.replaceChildren(...nodes),ask:confirm,setTimeout:fn=>{timers.push(fn);return timers.length;},clearTimeout(){},request:async(url,opts,host)=>{requests.push({url,opts,host});if(defer&&url==='/api/images')return await new Promise(resolve=>pending.push(resolve));return {json:async()=>url==='/api/images'?{images:rows,operation:report}:url==='/api/images/operation'?report:{id:'worker',action:'pull',running:true,results:[]}};}};
 vm.runInNewContext(fs.readFileSync(require('node:path').join(__dirname,'../web/operation-panels.js'),'utf8'),context);
 vm.runInNewContext(source,context);
 return {nodes,window,requests,find,pending,timers,async settle(){for(let i=0;i<20;i++)await Promise.resolve();},async host(host,capabilities=[]){window.reefHost=host;window.reefCapabilities=capabilities;window.dispatchEvent({type:'reef-host-changed'});await this.settle();}};
}
test('images render selected metadata as text and protect used images including stopped containers',async()=>{
 const p=page({rows:[image('a',{tags:['<script>bad</script>']}),image('b',{containers:['stopped']})]});await p.settle();
 const rows=p.find('images-rows').children;assert.equal(rows.length,2);assert.equal(rows[0].children[1].children[0].textContent,'<script>bad</script>');assert.equal(rows[1].children[0].children[0].disabled,true);
 await rows[0].children[1].children[0].fire('click');assert.equal(p.find('image-details').open,true);assert.ok(p.find('image-detail-fields').children.some(n=>n.textContent==='<script>bad</script>'));
});
test('legacy agents cannot read or mutate image management while capable hosts receive pinned CSRF actions',async()=>{
 const legacy=page({host:'remote',capabilities:['control-v1']});await legacy.settle();assert.equal(legacy.requests.length,0);assert.equal(legacy.find('images-pull-submit').disabled,true);
 const p=page({host:'ego',capabilities:['control-v1','images-v1']});await p.settle();p.find('images-reference').value='example/app:latest';await p.find('images-pull').fire('submit');await p.settle();
 const call=p.requests.at(-1);assert.equal(call.url,'/api/images/action');assert.equal(call.host,'ego');assert.equal(call.opts.method,'POST');assert.equal(call.opts.body.get('csrf'),'fixture-csrf');assert.equal(call.opts.body.get('action'),'pull');assert.equal(call.opts.body.get('reference'),'example/app:latest');
});
test('cleanup reviews exactly unused candidates and preserves IDs and tag fingerprints',async()=>{
 let message='';const p=page({rows:[image('a',{tags:[]}),image('b'),image('c',{containers:['stopped']})],confirm:async text=>{message=text;return true;}});await p.settle();
 await p.find('images-cleanup').fire('click');await p.settle();const body=p.requests.at(-1).opts.body;assert.equal(body.get('action'),'cleanup');assert.deepEqual(JSON.parse(body.get('targets')),[{id:image().id,fingerprint:image().fingerprint}]);assert.ok(message.includes(image('a').id.slice(7,19)));assert.ok(message.includes('local'));
 const all=page({rows:[image('a',{tags:[]}),image('b'),image('c',{containers:['stopped']})]});await all.settle();all.find('images-cleanup-mode').value='all';await all.find('images-cleanup').fire('click');await all.settle();assert.equal(JSON.parse(all.requests.at(-1).opts.body.get('targets')).length,2);
});
test('host changes while confirmation waits cannot send a mutation to either host',async()=>{
 let approve;const p=page({confirm:()=>new Promise(resolve=>approve=resolve)});await p.settle();p.find('images-reference').value='app:tag';const promise=p.find('images-pull').fire('submit');await p.settle();await p.host('ego',['images-v1']);approve(true);await promise;await p.settle();assert.equal(p.requests.filter(r=>r.opts?.method==='POST').length,0);
});
test('late image response from a previous host cannot replace its successor inventory',async()=>{
 const p=page({defer:true});await p.settle();assert.equal(p.pending.length,1);assert.equal(p.find('images-loading').hidden,false);await p.host('ego',['images-v1']);p.pending[0]({json:async()=>({images:[image('a')],operation:{}})});await p.settle();assert.equal(p.find('images-rows').children.length,0);assert.equal(p.window.reefImagesReady,false);assert.equal(p.find('images-loading').hidden,false);const fresh=p.find('images-refresh').fire('click');await p.settle();p.pending[1]({json:async()=>({images:[],operation:{}})});await fresh;await p.settle();assert.equal(p.find('images-loading').hidden,true);const failed=p.find('images-refresh').fire('click');await p.settle();p.pending[2]({json:async()=>{throw Error('Inventory unavailable');}});await failed;await p.settle();assert.equal(p.find('images-loading').hidden,true);assert.equal(p.find('images-error').hidden,false);
});
test('batch selection deletes multiple unused images and ignores used rows',async()=>{
 const p=page({rows:[image('a'),image('b'),image('c',{containers:['stopped']})]});await p.settle();p.find('images-select-all').checked=true;await p.find('images-select-all').fire('change');await p.find('images-delete').fire('click');await p.settle();const body=p.requests.at(-1).opts.body;assert.equal(body.get('action'),'remove');assert.equal(JSON.parse(body.get('targets')).length,2);
});

const manyImages=n=>Array.from({length:n},(_,i)=>image('a',{id:'sha256:'+(i+1).toString(16).padStart(64,'0'),tags:[]}));
test('cleanup submits a selection above 100 once with every reviewed fingerprint',async()=>{
 let confirmation='';const p=page({rows:manyImages(250),confirm:async text=>{confirmation=text;return true;}});await p.settle();await p.find('images-cleanup').fire('click');await p.settle();
 const calls=p.requests.filter(r=>r.opts?.method==='POST');assert.equal(calls.length,1);const targets=JSON.parse(calls[0].opts.body.get('targets'));assert.equal(targets.length,250);assert.equal(targets[249].id,manyImages(250)[249].id);assert.match(confirmation,/250/);assert.match(confirmation,/automatically in batches/);
});
test('large remote cleanup requires negotiated batch support and pins its host',async()=>{
 const old=page({host:'old-agent',capabilities:['control-v1','images-v1'],rows:manyImages(150)});await old.settle();await old.find('images-cleanup').fire('click');await old.settle();assert.equal(old.requests.filter(r=>r.opts?.method==='POST').length,0);assert.match(old.find('images-error').textContent,/Upgrade/);
 const ready=page({host:'ego',capabilities:['control-v1','images-v1','images-batch-v1'],rows:manyImages(150)});await ready.settle();await ready.find('images-cleanup').fire('click');await ready.settle();const call=ready.requests.at(-1);assert.equal(call.host,'ego');assert.equal(JSON.parse(call.opts.body.get('targets')).length,150);
});

test('image operation reports progress without replacing rows on every update',async()=>{
 const report={id:'large-job',action:'cleanup',running:true,total:250,completed:121,batch:2,batches:3,results:[{id:'used-image',ok:false,message:'Image retained: used by a running or stopped container'}]};
 const p=page({report});await p.settle();const lines=p.find('images-operation').children;assert.match(lines[0].children[0].textContent,/121 \/ 250 images processed/);assert.match(lines[0].children[0].textContent,/batch 2 \/ 3/);assert.match(lines[1].textContent,/Image retained/);assert.equal(p.find('images-cleanup').disabled,true);
 const originalRow=p.find('images-rows').children[0];report.completed=122;await p.timers.at(-1)();assert.equal(p.find('images-rows').children[0],originalRow);assert.match(p.find('images-operation').children[0].children[0].textContent,/122 \/ 250 images processed/);report.running=false;report.skipped=128;await p.timers.at(-1)();assert.match(p.find('images-operation').children[0].children[0].textContent,/stopped/);assert.match(p.find('images-operation').children[0].children[0].textContent,/128 not processed/);
});

test('successful image cleanup hides its progress panel and stays hidden on later polls',async()=>{
 const report={id:'cleanup-job',action:'cleanup',running:true,total:1,completed:0,results:[]},p=page({report});await p.settle();assert.equal(p.find('images-operation').hidden,false);
 report.running=false;report.completed=1;report.results=[{id:'image',ok:true,message:'Removed'}];await p.timers.at(-1)();assert.equal(p.find('images-operation').hidden,true);await p.timers.at(-1)();assert.equal(p.find('images-operation').hidden,true);assert.equal(p.find('images-select-all').disabled,false);
});
test('partial image cleanup can be dismissed without reappearing and dismissal stays scoped to its host and operation',async()=>{
 const report={id:'cleanup-job',action:'cleanup',running:false,total:1,completed:1,results:[{id:'image',ok:false,message:'Image retained'}]},p=page({report});await p.settle();assert.equal(p.find('images-operation').hidden,false);
 await p.find('images-operation').children[0].children.at(-1).fire('click');await p.timers.at(-1)();assert.equal(p.find('images-operation').hidden,true);
 await p.host('ego',['images-v1']);await p.timers.at(-1)();assert.equal(p.find('images-operation').hidden,false);
 await p.host('local');await p.timers.at(-1)();assert.equal(p.find('images-operation').hidden,true);
 report.id='new-job';report.running=true;report.results=[];await p.timers.at(-1)();assert.equal(p.find('images-operation').hidden,false);assert.equal(p.find('images-operation').children.some(n=>n.textContent==='Dismiss'),false);
});
