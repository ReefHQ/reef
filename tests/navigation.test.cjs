'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
const path=require('node:path');
const source=name=>fs.readFileSync(path.join(__dirname,'../web/',name),'utf8');
function page(saved,{blocked=false,hosts=['local'],containers=[],projects=[],earlyState=false,hostScript=false,capabilities=[]}={}){
 const storage=new Map(saved?[['reef.navigation.v1',JSON.stringify(saved)]]:[]),events={},nodes=new Map(),frames=[];
 class Element{
  constructor(tag='DIV'){this.tagName=tag;this.value='';this.dataset={};this.options=[];this.listeners={};this.hidden=false;this.open=false;this.textContent='';}
  addEventListener(name,fn){(this.listeners[name]??=[]).push(fn);}
  fire(name){for(const fn of this.listeners[name]||[])fn({target:this});}
  replaceChildren(...children){this.children=children;if(this.tagName==='SELECT')this.options=children;}
  append(){} setAttribute(){} removeAttribute(){} contains(){return false;} focus(){} close(){this.open=false;}
 }
 const selects=['host-select','container-state','container-project','container-access','project-access','stats-state'];
 const find=selector=>{if(!nodes.has(selector))nodes.set(selector,new Element(selects.includes(selector.slice(1))?'SELECT':'DIV'));return nodes.get(selector);};
 for(const id of ['container-access','project-access'])find('#'+id).options=[{value:''},{value:'managed'},{value:'view-only'}];
 const nav=['containers','projects'].map(view=>{const el=new Element();el.dataset.view=view;return el;});
 const window={reefHost:'local',scrollY:0,addEventListener(name,fn){(events[name]??=[]).push(fn);},dispatchEvent(event){for(const fn of events[event.type]||[])fn(event);},scrollTo(x,y){this.scrollY=y;}};
 const document={body:new Element(),querySelector:find,querySelectorAll(selector){return selector==='[data-view]'?nav:[];},createElement:tag=>new Element(tag.toUpperCase())};
 let selected='',documentLoads=0,inventoryRequests=0;
 const context={window,document,history:{scrollRestoration:'auto'},sessionStorage:{getItem:key=>{if(blocked)throw Error('blocked');return storage.get(key)||null;},setItem:(key,value)=>{if(blocked)throw Error('blocked');storage.set(key,value);}},Option:function(text,value){this.text=text;this.value=value;},CustomEvent:function(type,opts){this.type=type;this.detail=opts?.detail;},setInterval(){},setTimeout(){return 1;},clearTimeout(){},requestAnimationFrame:fn=>frames.push(fn),current:()=>projects.find(p=>p.id===selected),controls(){},textNode:()=>new Element(),replaceKeepingFocus:(node,children)=>node.replaceChildren(...children),ReefContainers:{update(){},name:()=>new Element(),state:()=>new Element(),actions:()=>new Element()},request:async url=>{if(url==='/api/hosts')return {json:async()=>({hosts:hosts.filter(h=>h!=='local').map(id=>({id,name:id,status:'online',capabilities})),enabled:true})};inventoryRequests++;return {json:async()=>({containers,project_access:{roots:[]},checked_at:new Date().toISOString(),host:{status:'online'}})};},selectProject:async id=>{selected=id;documentLoads++;window.dispatchEvent({type:'reef-open-project'});}};
 context.window.window=window;context.window.document=document;
 vm.createContext(context);vm.runInContext(source('navigation.js'),context);context.ReefNavigation=window.ReefNavigation;
 vm.runInContext(source('model.js'),context);context.ReefUI=window.ReefUI;
 let ready;if(hostScript)vm.runInContext(source('hosts.js'),context);else window.reefHostsReady=new Promise(resolve=>ready=resolve);
 if(earlyState)window.reefProjects=projects;
 vm.runInContext(source('inventory.js'),context);
 return {storage,nodes,window,nav,context,flushFrames(){while(frames.length)frames.shift()();},get documentLoads(){return documentLoads;},get inventoryRequests(){return inventoryRequests;},async start(){if(!earlyState)window.dispatchEvent({type:'reef-state',detail:projects});const host=hosts.includes(saved?.host)?saved.host:'local';if(!hostScript&&host!=='local'){window.reefHost=host;window.dispatchEvent({type:'reef-host-changed',detail:{id:host,name:host,restoring:true}});}if(!hostScript)ready();for(let i=0;i<15;i++)await Promise.resolve();window.reefProjects=projects;window.dispatchEvent({type:'reef-state',detail:projects});for(let i=0;i<15;i++)await Promise.resolve();while(frames.length)frames.shift()();},reloadState(){return JSON.parse(storage.get('reef.navigation.v1'));}};
}
test('reload restores project detail, filters and scroll after host/inventory/state validation',async()=>{
 const saved={view:'projects',host:'local',project:'p1',scroll:420,filters:{'project-search':'media'}};
 const p=page(saved,{projects:[{id:'p1',name:'Media',project_name:'media',operation:{}}],earlyState:true});
 assert.equal(p.inventoryRequests,0);await p.start();
 assert.equal(p.documentLoads,1);assert.equal(p.nodes.get('#project-detail').hidden,false);
 assert.equal(p.nodes.get('#project-search').value,'media');assert.equal(p.window.scrollY,420);
 p.window.dispatchEvent({type:'pagehide'});assert.equal(p.reloadState().project,'p1');
});
test('removed project returns to overview without reading a document',async()=>{
 const p=page({view:'projects',host:'local',project:'gone',scroll:0});await p.start();
 assert.equal(p.documentLoads,0);assert.equal(p.nodes.get('#project-overview').hidden,false);assert.equal(p.reloadState().project,'');
});
test('remote reload restores its inventory and filters without loading a local document',async()=>{
 const p=page({view:'containers',host:'remote1',project:'p1',filters:{'container-state':'running'},scroll:80},{hostScript:true,hosts:['local','remote1'],containers:[{id:'abc',name:'app',image:'image',state:'running'}]});await p.start();
 assert.equal(p.window.reefHost,'remote1');assert.equal(p.documentLoads,0);assert.equal(p.nodes.get('#container-state').value,'running');assert.equal(p.window.scrollY,80);
});
test('revoked host falls back to local and drops remote filters',async()=>{
 const p=page({view:'projects',host:'revoked',project:'gone',filters:{'project-search':'remote'},scroll:300},{hostScript:true});await p.start();
 assert.equal(p.reloadState().host,'local');assert.equal(p.nodes.get('#project-search').value,'');assert.equal(p.window.scrollY,0);
});
test('manual navigation while initialization waits takes precedence over saved location',async()=>{
 const p=page({view:'projects',host:'local',project:'p1'},{projects:[{id:'p1'}]});p.nav[0].fire('click');await p.start();
 assert.equal(p.documentLoads,0);assert.equal(p.nodes.get('#containers-view').hidden,false);
});
test('blocked storage and malformed saved state do not break navigation',async()=>{
 const p=page({view:'unexpected',host:[],project:{},scroll:-2,filters:{'project-search':{}}},{blocked:true});await p.start();p.nav[1].fire('click');assert.equal(p.nodes.get('#projects-view').hidden,false);
 const normal=p.context.ReefNavigation.read();assert.equal(normal.view,'containers');assert.equal(normal.host,'local');
});

test('invalid preference data is normalized and expired select values are discarded',async()=>{
 const p=page({view:'invalid',host:[],project:{},scroll:-1,filters:{'container-state':'gone','project-search':{}}});await p.start();
 assert.equal(p.reloadState().view,'containers');assert.equal(p.reloadState().host,'local');assert.equal(p.reloadState().scroll,0);assert.equal(p.nodes.get('#container-state').value,'');assert.equal(p.nodes.get('#project-search').value,'');
});

test('upgraded remote host restores its selected managed project after reload',async()=>{
 const p=page({view:'projects',host:'remote',project:'p1',scroll:70},{hosts:['local','remote'],hostScript:true,capabilities:['control-v1'],projects:[{id:'p1',name:'Remote project',project_name:'remote-app',operation:{}}]});await p.start();assert.equal(p.documentLoads,1);assert.equal(p.nodes.get('#project-detail').hidden,false);assert.equal(p.reloadState().host,'remote');assert.equal(p.reloadState().project,'p1');
});

test('Images restoration waits for its inventory before restoring filters and scroll',async()=>{
 const p=page({view:'images',host:'local',filters:{'images-search':'alpine'},scroll:240});await p.start();
 assert.equal(p.nodes.get('#images-view').hidden,false);assert.equal(p.window.scrollY,0);
 p.window.reefImagesReady=true;p.window.dispatchEvent({type:'reef-images-ready'});
 // Restoration schedules one animation frame, just as inventory-backed views do.
 p.flushFrames();assert.equal(p.window.scrollY,240);assert.equal(p.nodes.get('#images-search').value,'alpine');assert.equal(p.reloadState().view,'images');
});

test('Volumes restoration waits for volume inventory and restores its filters and scroll',async()=>{
 const p=page({view:'volumes',host:'local',filters:{'volumes-search':'database','volumes-usage':'unused'},scroll:280});await p.start();
 assert.equal(p.nodes.get('#volumes-view').hidden,false);assert.equal(p.window.scrollY,0);
 p.window.reefVolumesReady=true;p.window.dispatchEvent({type:'reef-volumes-ready'});p.flushFrames();
 assert.equal(p.window.scrollY,280);assert.equal(p.nodes.get('#volumes-search').value,'database');assert.equal(p.reloadState().view,'volumes');
});

test('Builds view restoration waits for its host state without opening the project editor',async()=>{
 const p=page({view:'builds',host:'local',scroll:180});await p.start();assert.equal(p.nodes.get('#builds-view').hidden,false);assert.equal(p.documentLoads,0);p.window.reefBuildsReady=true;p.window.dispatchEvent({type:'reef-builds-ready'});p.flushFrames();assert.equal(p.window.scrollY,180);assert.equal(p.reloadState().view,'builds');
});

 test('Networks restoration waits for inventory and restores filters and scroll',async()=>{
 const p=page({view:'networks',host:'local',filters:{'networks-search':'app'},scroll:280});await p.start();assert.equal(p.nodes.get('#networks-view').hidden,false);assert.equal(p.window.scrollY,0);p.window.reefNetworksReady=true;p.window.dispatchEvent({type:'reef-networks-ready'});p.flushFrames();assert.equal(p.window.scrollY,280);assert.equal(p.nodes.get('#networks-search').value,'app');assert.equal(p.reloadState().view,'networks');
 });

test('Statistics restoration opens the lazy view first then waits for its first sample before restoring scroll',async()=>{
 const p=page({view:'stats',host:'local',filters:{'stats-search':'web'},scroll:320});await p.start();assert.equal(p.nodes.get('#stats-view').hidden,false);assert.equal(p.nodes.get('#stats-search').value,'web');assert.equal(p.window.scrollY,0);p.window.reefStatsReady=true;p.window.dispatchEvent({type:'reef-stats-ready'});p.flushFrames();assert.equal(p.window.scrollY,320);assert.equal(p.nodes.get('#stats-search').value,'web');assert.equal(p.reloadState().view,'stats');
});

 test('explicit Statistics interactions override pending saved restoration and persist its current page',async()=>{
 const p=page({view:'stats',host:'local',statsOffset:32,filters:{'stats-search':'old'},scroll:320});await p.start();p.nodes.get('#stats-search').value='new';p.window.reefStatsOffset=64;p.window.dispatchEvent({type:'reef-stats-interaction'});p.window.reefStatsReady=true;p.window.dispatchEvent({type:'reef-stats-ready'});p.flushFrames();assert.equal(p.nodes.get('#stats-search').value,'new');assert.equal(p.reloadState().statsOffset,64);assert.equal(p.window.scrollY,0);
 });

test('History reload waits for central records before restoring its view, filters, cursor and scroll',async()=>{
 const p=page({view:'history',host:'local',historyOffset:50,historyCursor:'99',filters:{'history-search':'db'},scroll:360});await p.start();assert.equal(p.nodes.get('#history-view').hidden,false);assert.equal(p.window.scrollY,0);p.window.reefHistoryOffset=50;p.window.reefHistoryCursor='99';p.window.reefHistoryReady=true;p.window.dispatchEvent({type:'reef-history-ready'});p.flushFrames();assert.equal(p.nodes.get('#history-search').value,'db');assert.equal(p.window.scrollY,360);assert.equal(p.reloadState().historyOffset,50);assert.equal(p.reloadState().historyCursor,'99');
});
