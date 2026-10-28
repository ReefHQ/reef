'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
function page(){
 const nodes=new Map(),events={};
 class Element{
  constructor(){this.dataset={};this.children=[];this.value='';this.hidden=false;this.listeners={};}
  addEventListener(type,fn){this.listeners[type]=fn;}
  append(...items){this.children.push(...items);}
  replaceChildren(...items){this.children=items;}
  contains(){return false;}
  setAttribute(){}
 }
 const find=selector=>{if(!nodes.has(selector))nodes.set(selector,new Element());return nodes.get(selector);};
 const window={addEventListener(type,fn){(events[type]??=[]).push(fn);},dispatchEvent(event){for(const fn of events[event.type]||[])fn(event);}};
 const context=vm.createContext({window,document:{querySelector:find,querySelectorAll:()=>[],createElement:()=>new Element(),addEventListener(){}},Option:function(text,value){this.text=text;this.value=value;},fetch:()=>new Promise(()=>{}),AbortController,setTimeout(){},clearTimeout(){},CustomEvent:function(type){this.type=type;},ReefUI:{operationSteps:()=>[]}});
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
