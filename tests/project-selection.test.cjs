'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
function page(){
 const nodes=new Map(),events={};
 class Element{
  constructor(){this.content='fixture-csrf';this.dataset={};this.children=[];this.value='';this.hidden=false;this.listeners={};}
  addEventListener(type,fn){this.listeners[type]=fn;}
  append(...items){this.children.push(...items);}
  replaceChildren(...items){this.children=items;}
  contains(){return false;}
  setAttribute(){}
 }
 const find=selector=>{if(!nodes.has(selector))nodes.set(selector,new Element());return nodes.get(selector);};
 const window={addEventListener(type,fn){(events[type]??=[]).push(fn);},dispatchEvent(event){for(const fn of events[event.type]||[])fn(event);}};
 const context=vm.createContext({window,document:{querySelector:find,querySelectorAll:()=>[],createElement:()=>new Element(),addEventListener(){}},Option:function(text,value){this.text=text;this.value=value;},fetch:()=>new Promise(()=>{}),AbortController,URLSearchParams,setTimeout(){},clearTimeout(){},CustomEvent:function(type){this.type=type;},ReefUI:{operationSteps:()=>[]}});
 vm.runInContext(fs.readFileSync('web/app.js','utf8'),context);
 vm.runInContext("projects=[{id:'p1',name:'Media',files:['compose.yaml'],operation:{}}];selected='p1';loaded=true;dirty=true;revision='rev1';$('#editor').value='unsaved compose';render();",context);
 function view(detail,projects=true){find('#projects-view').hidden=!projects;find('#project-detail').hidden=!detail;window.dispatchEvent({type:'reef-view-changed'});}
 return {nodes,context,view,render:()=>vm.runInContext('render()',context)};
}
test('project highlight follows the displayed detail across overview, polling and reopening without losing drafts',()=>{
 const p=page(),buttons=()=>p.nodes.get('#projects').children,picker=p.nodes.get('#managed-project-select');
 p.view(true);assert.equal(buttons()[0].className,'project active');assert.equal(picker.value,'p1');
 p.view(false);assert.equal(buttons()[0].className,'project');assert.equal(picker.value,'');
 p.render();assert.equal(buttons()[0].className,'project');assert.equal(picker.value,'');
 assert.equal(vm.runInContext('selected',p.context),'p1');assert.equal(vm.runInContext('dirty',p.context),true);
 assert.equal(vm.runInContext('revision',p.context),'rev1');assert.equal(p.nodes.get('#editor').value,'unsaved compose');
 p.view(true);assert.equal(buttons()[0].className,'project active');assert.equal(picker.value,'p1');
 p.view(false,false);p.render();assert.equal(buttons()[0].className,'project');assert.equal(picker.value,'');
});
function imageUpdatePage(){
 const p=page();p.context.calls=[];p.context.confirmations=[];p.context.actionConfirm=async text=>{p.context.confirmations.push(text);return true};
 vm.runInContext("dirty=false;projects[0].image_updates={services:[{status:'available'}]};editorHost='remote-one';window.reefHost='remote-one';window.reefHostName='Remote one';window.reefCapabilities=['control-v1'];ask=(text)=>actionConfirm(text);request=async(path,options,host)=>{calls.push({path,options,host});};",p.context);return p;
}
test('image panel action reuses confirmed project Apply update with saved revision, host and CSRF',async()=>{
 const p=imageUpdatePage();await p.context.window.ReefApplyImageUpdate();assert.equal(p.context.confirmations.length,1);assert.match(p.context.confirmations[0],/Media on Remote one/);assert.match(p.context.confirmations[0],/stop and recreate this project.*Downtime/);assert.equal(p.context.calls.length,1);
 const call=p.context.calls[0];assert.equal(call.path,'/api/action');assert.equal(call.host,'remote-one');assert.equal(call.options.body.get('action'),'apply');assert.equal(call.options.body.get('id'),'p1');assert.equal(call.options.body.get('revision'),'rev1');assert.equal(call.options.body.get('csrf'),'fixture-csrf');
});
test('cancelled, edited, replaced, busy and read-only project confirmations cannot deploy',async()=>{
 for(const change of ["dirty=true","selected='another-project'","editorEpoch++","projects[0].busy=true","window.reefAccess={canWrite:false}","loaded=false","projects[0].image_updates.services=[]"]){const p=imageUpdatePage();p.context.actionConfirm=async()=>{vm.runInContext(change,p.context);return true};await p.context.window.ReefApplyImageUpdate();assert.equal(p.context.calls.length,0,change);}
 const cancelled=imageUpdatePage();cancelled.context.actionConfirm=async()=>false;await cancelled.context.window.ReefApplyImageUpdate();assert.equal(cancelled.context.calls.length,0);
 const dirty=imageUpdatePage();vm.runInContext('dirty=true',dirty.context);await dirty.context.window.ReefApplyImageUpdate();assert.equal(dirty.context.confirmations.length,0);assert.equal(dirty.context.calls.length,0);
});
test('manual tag selection prepares a draft through its separate endpoint without deploying',async()=>{
 const p=imageUpdatePage();let alive=true;p.context.window.ReefImageTags={isCurrent:()=>alive,close(){alive=false}};p.context.preparedReply={content:'services: selected tag draft',revision:'rev1'};
 vm.runInContext('request=async(path,options,host)=>{calls.push({path,options,host});return {json:async()=>preparedReply};}',p.context);
 await p.context.window.ReefPrepareImageVersion('web',{id:'p1',host:'remote-one',service:'web',image:'example/web:1.0.0',candidate:'example/web:2.0.0',revision:'rev1'});
 assert.equal(p.context.calls[0].path,'/api/image-tags/prepare');assert.equal(p.context.calls[0].options.body.get('candidate'),'example/web:2.0.0');assert.equal(p.context.calls[0].options.body.get('confirmed'),'yes');assert.match(p.context.confirmations[0],/cross major\/minor/);assert.equal(vm.runInContext('dirty',p.context),true);assert.equal(p.nodes.get('#editor').value,'services: selected tag draft');assert.equal(p.context.calls.some(c=>c.path==='/api/action'),false);
});
test('closing the tag browser while a draft request is pending discards its response',async()=>{
 const p=imageUpdatePage();let alive=true,resolve;p.context.window.ReefImageTags={isCurrent:()=>alive,close(){alive=false}};p.context.tagRequest=()=>new Promise(r=>resolve=r);vm.runInContext('request=()=>tagRequest()',p.context);
 const pending=p.context.window.ReefPrepareImageVersion('web',{id:'p1',host:'remote-one',service:'web',image:'example/web:1.0',candidate:'example/web:2.0',revision:'rev1'});for(let i=0;i<10;i++)await Promise.resolve();alive=false;resolve({json:async()=>({content:'late draft',revision:'rev1'})});await pending;assert.equal(vm.runInContext('dirty',p.context),false);assert.notEqual(p.nodes.get('#editor').value,'late draft');
});
function restartPage(){const p=imageUpdatePage();vm.runInContext("window.reefCapabilities.push('project-restart-v1');projects[0].image_updates={};",p.context);return p;}
test('Restart project confirms downtime and sends the exact saved revision, host and CSRF',async()=>{
 const p=restartPage();await vm.runInContext("runAction('restart')",p.context);assert.equal(p.context.confirmations.length,1);assert.match(p.context.confirmations[0],/Restart Media on Remote one/);assert.match(p.context.confirmations[0],/including stopped containers.*Downtime/);assert.equal(p.context.calls.length,1);const call=p.context.calls[0];assert.equal(call.host,'remote-one');assert.equal(call.path,'/api/action');for(const [key,value] of Object.entries({action:'restart',id:'p1',revision:'rev1',csrf:'fixture-csrf',confirmed:'yes'}))assert.equal(call.options.body.get(key),value);assert.equal(p.nodes.get('#editor').value,'unsaved compose');
});
test('restart cancellation and changed host, revision, draft, permissions or capability never dispatch',async()=>{
 for(const change of ["dirty=true","revision='new-revision'","selected='other'","editorEpoch++","projects[0].busy=true","projects[0].image_updates.checking=true","projects[0].operation.needs_start=true","window.reefCapabilities=['control-v1']","window.reefAccess={canWrite:false}"]){const p=restartPage();p.context.actionConfirm=async()=>{vm.runInContext(change,p.context);return true};await vm.runInContext("runAction('restart')",p.context);assert.equal(p.context.calls.length,0,change)}
 const cancelled=restartPage();cancelled.context.actionConfirm=async()=>false;await vm.runInContext("runAction('restart')",cancelled.context);assert.equal(cancelled.context.calls.length,0);
 for(const change of ["dirty=true","window.reefCapabilities=['control-v1']","projects[0].operation.needs_start=true"]){const p=restartPage();vm.runInContext(change,p.context);await vm.runInContext("runAction('restart')",p.context);assert.equal(p.context.calls.length,0);assert.equal(p.context.confirmations.length,0)}
});
test('Restart project menu explains old-agent and recovery blocks and shares project operation locks',()=>{
 const p=restartPage(),button=p.nodes.get('[data-action="restart"]');button.dataset.action='restart';p.context.document.querySelectorAll=selector=>selector==='[data-action]'?[button]:[];
 vm.runInContext('controls()',p.context);assert.equal(button.disabled,false);
 vm.runInContext("window.reefCapabilities=['control-v1'];controls()",p.context);assert.equal(button.disabled,true);assert.match(button.title,/0.28.0/);
 vm.runInContext("window.reefCapabilities.push('project-restart-v1');projects[0].operation.needs_start=true;controls()",p.context);assert.equal(button.disabled,true);assert.match(button.title,/Start \/ retry/);
 vm.runInContext("projects[0].operation.needs_start=false;dirty=true;controls()",p.context);assert.equal(button.disabled,true);
});
