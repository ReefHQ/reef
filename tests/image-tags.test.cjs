'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
function page({deferred=false,write=true,host='remote-a',supported=true}={}){
 const nodes=new Map(),events={},requests=[],prepared=[],pending=[];let editor={id:'p1',revision:'rev1',dirty:false},prepare=async()=>{},fail=false;
 class Node{constructor(){this.value='';this.textContent='';this.children=[];this.hidden=false;this.disabled=false;this.open=false;this.listeners={}}addEventListener(type,fn){this.listeners[type]=fn}replaceChildren(...n){this.children=n;this.value=''}showModal(){this.open=true}close(){this.open=false;this.listeners.close?.()}async fire(type){return await this.listeners[type]?.({preventDefault(){}})}}
 const find=id=>{if(!nodes.has(id))nodes.set(id,new Node());return nodes.get(id)};
 const window={reefHost:host,reefHostName:'Remote A',reefCapabilities:supported?['control-v1','image-tags-v1']:['control-v1'],reefAccess:{canWrite:write},ReefImageTagContext:()=>editor,ReefPrepareImageVersion:async(service,choice)=>{prepared.push({service,choice});await prepare()},addEventListener(type,fn){events[type]=fn}};
 const ctx={window,document:{getElementById:find},Option:function(text,value){this.textContent=text;this.value=value},URLSearchParams,AbortController,request:async(path,options,chosenHost)=>{requests.push({path,options,host:chosenHost});if(deferred)return await new Promise(resolve=>pending.push(resolve));if(fail)throw Error('Hub unavailable');const page=Number(new URLSearchParams(path.split('?')[1]).get('page'));return {json:async()=>({tags:['latest','22.0.5','23.0.0'],total:5000,page,more:page<3,image:'dolibarr/dolibarr:22.0.5',revision:'rev1'})}}};
 vm.runInNewContext(fs.readFileSync('web/image-tags.js','utf8'),ctx);
 return {window,find,events,requests,prepared,pending,setEditor(value){editor=value},setPrepare(fn){prepare=fn},setFail(value){fail=value},async open(){window.ReefImageTags.open({id:'p1',name:'ERP'},{service:'web',image:'dolibarr/dolibarr:22.0.5'});await this.flush()},async flush(){for(let i=0;i<25;i++)await Promise.resolve()}};
}
test('tags load only on demand, search and paginate on the captured host without a mutation',async()=>{
 const p=page();assert.equal(p.requests.length,0);await p.open();assert.match(p.find('image-tags-status').textContent,/5000 matching tags/);assert.equal(p.requests[0].host,'remote-a');assert.equal(p.find('image-tags-previous').disabled,true);assert.equal(p.find('image-tags-next').disabled,false);
 await p.find('image-tags-next').fire('click');await p.flush();assert.equal(new URLSearchParams(p.requests[1].path.split('?')[1]).get('page'),'2');p.find('image-tags-search').value='alpine';await p.find('image-tags-search-form').fire('submit');await p.flush();const params=new URLSearchParams(p.requests.at(-1).path.split('?')[1]);assert.equal(params.get('page'),'1');assert.equal(params.get('search'),'alpine');assert.equal(params.get('revision'),'rev1');assert.equal(p.requests.some(r=>r.options?.method==='POST'),false);
});
test('choosing a tag passes an exact draft proposal, while drafts and viewers cannot prepare',async()=>{
 const p=page();await p.open();p.find('image-tags-list').value='23.0.0';await p.find('image-tags-list').fire('change');assert.equal(p.find('image-tags-prepare').disabled,false);await p.find('image-tags-prepare').fire('click');assert.equal(p.prepared[0].choice.candidate,'dolibarr/dolibarr:23.0.0');assert.equal(p.prepared[0].choice.host,'remote-a');assert.equal(p.prepared[0].choice.revision,'rev1');assert.equal(p.window.ReefImageTags.isCurrent(p.prepared[0].choice),true);
 for(const option of [{write:false},{write:true},{write:true,busy:true}]){const x=page(option);await x.open();if(option.write)x.setEditor({id:'p1',revision:'rev1',dirty:!option.busy,busy:!!option.busy});x.find('image-tags-list').value='23.0.0';await x.find('image-tags-list').fire('change');await x.find('image-tags-prepare').fire('click');assert.equal(x.prepared.length,0)}
});
test('closed, replaced and stale tag browser responses cannot populate or prepare a new selection',async()=>{
 const p=page({deferred:true});await p.open();const pending=p.pending.shift();await p.find('image-tags-close').fire('click');pending({json:async()=>({tags:['wrong'],image:'wrong',revision:'wrong'})});await p.flush();assert.equal(p.find('image-tags-dialog').open,false);assert.equal(p.find('image-tags-list').children.length,0);
 const x=page();await x.open();x.find('image-tags-list').value='latest';await x.find('image-tags-list').fire('change');await x.find('image-tags-prepare').fire('click');const choice=x.prepared[0].choice;x.window.reefHost='remote-b';x.events['reef-host-changed']();assert.equal(x.window.ReefImageTags.isCurrent(choice),false);
 const old=page({supported:false});await old.open();assert.equal(old.requests.length,0);
});
test('failed reads are explicit, and closing during preparation invalidates its token',async()=>{
 const p=page();p.setFail(true);await p.open();assert.match(p.find('image-tags-status').textContent,/unavailable/);assert.equal(p.find('image-tags-prepare').disabled,true);
 const x=page();await x.open();let resolve;x.setPrepare(()=>new Promise(r=>resolve=r));x.find('image-tags-list').value='latest';await x.find('image-tags-list').fire('change');const preparing=x.find('image-tags-prepare').fire('click');await x.flush();const choice=x.prepared[0].choice;await x.find('image-tags-close').fire('click');assert.equal(x.window.ReefImageTags.isCurrent(choice),false);resolve();await preparing;
});
