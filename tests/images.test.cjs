'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const source=fs.readFileSync(require('node:path').join(__dirname,'../web/images.js'),'utf8');
const image=(id='a',overrides={})=>({id:'sha256:'+id.repeat(64),tags:['example/app:'+id],digests:[],fingerprint:id.repeat(64),containers:[],size:2048,os:'linux',architecture:'amd64',created:'2026-10-27',...overrides});
function page({host='local',capabilities=[],rows=[image()],defer=false,confirm=async()=>true}={}){
 const nodes=new Map(),events={},requests=[],timers=[],pending=[];
 class Node{
  constructor(tag='div'){this.tagName=tag;this.value='';this.hidden=false;this.disabled=false;this.dataset={};this.children=[];this.listeners={};this.checked=false;this.textContent='';}
  addEventListener(type,fn){(this.listeners[type]??=[]).push(fn);}async fire(type){for(const fn of this.listeners[type]||[])await fn({target:this,preventDefault(){}});}
  append(...nodes){this.children.push(...nodes);}replaceChildren(...nodes){this.children=nodes;}setAttribute(){}focus(){}showModal(){this.open=true;}close(){this.open=false;}
 }
 const find=id=>{if(!nodes.has(id))nodes.set(id,new Node());return nodes.get(id);};find('images-cleanup-mode').value='untagged';
 const window={reefHost:host,reefHostName:host,reefCapabilities:capabilities,addEventListener(type,fn){(events[type]??=[]).push(fn);},dispatchEvent(event){for(const fn of events[event.type]||[])fn(event);}};
 const context={window,document:{getElementById:find,createElement:tag=>new Node(tag),addEventListener(){}},CustomEvent:function(type){this.type=type;},URLSearchParams,csrf:'fixture-csrf',textNode:(tag,value)=>Object.assign(new Node(tag),{textContent:value}),replaceKeepingFocus:(parent,nodes)=>parent.replaceChildren(...nodes),ask:confirm,setTimeout:fn=>{timers.push(fn);return timers.length;},clearTimeout(){},request:async(url,opts,host)=>{requests.push({url,opts,host});if(defer&&url==='/api/images')return await new Promise(resolve=>pending.push(resolve));return {json:async()=>url==='/api/images'?{images:rows,operation:{}}:{id:'worker',action:'pull',running:true,results:[]}};}};
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
 const p=page({defer:true});await p.settle();assert.equal(p.pending.length,1);await p.host('ego',['images-v1']);p.pending[0]({json:async()=>({images:[image('a')],operation:{}})});await p.settle();assert.equal(p.find('images-rows').children.length,0);assert.equal(p.window.reefImagesReady,false);
});
test('batch selection deletes multiple unused images and ignores used rows',async()=>{
 const p=page({rows:[image('a'),image('b'),image('c',{containers:['stopped']})]});await p.settle();p.find('images-select-all').checked=true;await p.find('images-select-all').fire('change');await p.find('images-delete').fire('click');await p.settle();const body=p.requests.at(-1).opts.body;assert.equal(body.get('action'),'remove');assert.equal(JSON.parse(body.get('targets')).length,2);
});
