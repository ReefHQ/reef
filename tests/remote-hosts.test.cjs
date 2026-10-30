'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs'),path=require('node:path');
const source=name=>fs.readFileSync(path.join(__dirname,'../web/',name),'utf8');
function page(){
 const nodes=new Map(),events={},requests=[];let deferredDocument=null,deferredPrepare=null;
 class Element{
  constructor(){this.value='';this.textContent='';this.dataset={};this.listeners={};this.children=[];this.open=false;this.hidden=false;this.content='csrf';this.className='';}
  append(...nodes){this.children.push(...nodes);}replaceChildren(...nodes){this.children=nodes;}contains(){return false;}setAttribute(){}removeAttribute(){}focus(){}showModal(){this.open=true;}addEventListener(name,fn){(this.listeners[name]??=[]).push(fn);}fire(name){for(const fn of this.listeners[name]||[])fn({target:this});}
 }
 const find=selector=>{if(!nodes.has(selector))nodes.set(selector,new Element());return nodes.get(selector);};
 const project=host=>({id:'same-id',name:host,project_name:'same-name',directory:'/fixtures/'+host,files:['compose.yaml'],operation:{id:'',phase:'',log:'',needs_start:false},busy:false});
 const window={reefHost:'local',reefHostName:'Local',reefCapabilities:[],addEventListener(type,fn){(events[type]??=[]).push(fn);},dispatchEvent(event){for(const fn of events[event.type]||[])fn(event);}};
 const document={addEventListener(){},querySelector:find,querySelectorAll:()=>[],createElement:()=>new Element()};
 const context={window,document,URLSearchParams,AbortController,CustomEvent:function(type,options){this.type=type;this.detail=options?.detail;},Option:function(label,value){this.value=value;},setTimeout(){return 1;},clearTimeout(){},location:{href:''},fetch:async(url,opts)=>{
  requests.push({url,opts});const parsed=new URL(url,'http://reef.test'),host=parsed.searchParams.get('host')||'local';
  const body=parsed.pathname==='/api/state'?[project(host)]:{content:(parsed.pathname==='/api/image-updates/prepare'?'prepared-':'compose-')+host,revision:'revision-'+host};
  if(parsed.pathname==='/api/document'&&deferredDocument){const pause=deferredDocument;deferredDocument=null;await pause;}
  if(parsed.pathname==='/api/image-updates/prepare'&&deferredPrepare){const pause=deferredPrepare;deferredPrepare=null;await pause;}
  return {ok:true,status:200,headers:{get(){return null;}},json:async()=>body};
 }};
 vm.createContext(context);vm.runInContext(source('model.js'),context);context.ReefUI=window.ReefUI;vm.runInContext(source('app.js'),context);
 const flush=async()=>{for(let i=0;i<20;i++)await Promise.resolve();};
 return {nodes,window,requests,context,flush,run:code=>vm.runInContext(code,context),async select(){await vm.runInContext("selectProject('same-id')",context);},async change(host){window.reefHost=host;window.reefHostName=host;window.reefCapabilities=host==='legacy'?[]:['control-v1'];window.dispatchEvent({type:'reef-host-changed',detail:{id:host,name:host}});await flush();},pauseNextPrepare(){let resolve;deferredPrepare=new Promise(r=>resolve=r);return resolve;},pauseNextDocument(){let resolve;deferredDocument=new Promise(r=>resolve=r);return resolve;}};
}
test('same project IDs on different hosts keep independent editor drafts and selected documents',async()=>{
 const p=page();await p.flush();await p.select();p.nodes.get('#editor').value='local draft';p.nodes.get('#editor').fire('input');
 await p.change('remote');await p.select();assert.equal(p.nodes.get('#editor').value,'compose-remote');p.nodes.get('#editor').value='remote draft';p.nodes.get('#editor').fire('input');
 await p.change('local');assert.equal(p.nodes.get('#editor').value,'local draft');assert.equal(p.run('dirty'),true);
 await p.change('remote');assert.equal(p.nodes.get('#editor').value,'remote draft');assert.equal(p.run('dirty'),true);
 assert.ok(p.requests.some(r=>r.url==='/api/state?host=remote'));assert.ok(p.requests.some(r=>r.url==='/api/document?id=same-id&host=remote'));
});
test('late document response from another host cannot replace the current editor',async()=>{
 const p=page();await p.flush();const release=p.pauseNextDocument(),opening=p.select();await p.flush();await p.change('remote');await p.select();assert.equal(p.nodes.get('#editor').value,'compose-remote');
 release();await opening;assert.equal(p.nodes.get('#editor').value,'compose-remote');assert.equal(p.run('revision'),'revision-remote');
});
test('legacy inventory agents never route project requests to local or remote backends',async()=>{
 const p=page();await p.flush();const count=p.requests.length;await p.change('legacy');await p.select();assert.equal(p.requests.length,count);assert.equal(p.run('projects.length'),0);
});

test('a confirmation from the previous host cannot dispatch an action after host replacement',async()=>{
 const p=page();await p.flush();await p.select();const action=p.run("runAction('apply')");await p.flush();await p.change('remote');await p.select();const dialog=p.nodes.get('#confirmation');dialog.returnValue='confirm';dialog.open=false;dialog.fire('close');await action;assert.equal(p.requests.filter(r=>r.url.startsWith('/api/action')).length,0);assert.equal(p.nodes.get('#editor').value,'compose-remote');
});

async function setupImageProposal(p){await p.flush();await p.select();p.run("current().image_updates={revision,services:[{service:'web',image:'gitlab/gitlab-ce:19.5.1-ce.0',candidate:'gitlab/gitlab-ce:19.5.3-ce.0',version_status:'available'}]}");}
async function confirmImageProposal(p){await p.flush();const d=p.nodes.get('#confirmation');d.returnValue='confirm';d.open=false;d.fire('close');await p.flush();}
test('confirmed image tag preparation fills only an unsaved editor draft with a pinned host and checked revision',async()=>{
 const p=page();await setupImageProposal(p);const action=p.window.ReefPrepareImageVersion('web');await confirmImageProposal(p);await action;
 const request=p.requests.find(r=>r.url.startsWith('/api/image-updates/prepare'));assert.ok(request);assert.equal(request.opts.body.get('revision'),'revision-local');assert.equal(request.opts.body.get('confirmed'),'yes');assert.equal(p.nodes.get('#editor').value,'prepared-local');assert.equal(p.run('dirty'),true);assert.ok(!p.requests.some(r=>r.url.startsWith('/api/action')));
});
test('image tag preparation preserves dirty drafts and discards replies from the old host',async()=>{
 const p=page();await setupImageProposal(p);p.nodes.get('#editor').value='my draft';p.run('dirty=true');await p.window.ReefPrepareImageVersion('web');assert.ok(!p.requests.some(r=>r.url.startsWith('/api/image-updates/prepare')));assert.equal(p.nodes.get('#editor').value,'my draft');
 p.run('dirty=false');const release=p.pauseNextPrepare(),action=p.window.ReefPrepareImageVersion('web');await confirmImageProposal(p);await p.change('remote');await p.select();release();await action;assert.equal(p.nodes.get('#editor').value,'compose-remote');assert.equal(p.run('dirty'),false);
});
test('a cancelled image tag proposal never submits preparation',async()=>{
 const p=page();await setupImageProposal(p);const action=p.window.ReefPrepareImageVersion('web');await p.flush();const d=p.nodes.get('#confirmation');d.returnValue='cancel';d.open=false;d.fire('close');await action;assert.ok(!p.requests.some(r=>r.url.startsWith('/api/image-updates/prepare')));
});
