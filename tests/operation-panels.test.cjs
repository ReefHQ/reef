'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const source=fs.readFileSync(require('node:path').join(__dirname,'../web/operation-panels.js'),'utf8');
function page(storage=new Map(),blocked=false){
 class Node{constructor(tag='div'){this.tagName=tag;this.children=[];this.dataset={};this.listeners={};this.scrollTop=0;this.hidden=false;}append(...nodes){this.children.push(...nodes);}replaceChildren(...nodes){this.children=nodes;}setAttribute(name,value){this[name]=value;}addEventListener(type,fn){this.listeners[type]=fn;}}
 const window={},panel=new Node(),context={window,document:{createElement:tag=>new Node(tag)},localStorage:{getItem:key=>{if(blocked)throw Error('blocked');return storage.get(key)||null;},setItem:(key,value)=>{if(blocked)throw Error('blocked');storage.set(key,value);}}};
 vm.runInNewContext(source,context);
 return {panel,render(op,host='local',kind='images'){window.ReefOperationPanels.render(panel,op,{host,kind,summary:'Cleanup result',result:row=>row.message});},dismiss(){panel.children[0].children[1].listeners.click();}};
}
const partial=(id='job')=>({id,action:'cleanup',running:false,total:1,results:[{id:'image',ok:false,message:'Retained: private diagnostic fixture'}]});
test('dismissal survives reload and stores only the operation ID',()=>{
 const storage=new Map(),first=page(storage);first.render(partial());first.dismiss();assert.equal(first.panel.hidden,true);
 const reload=page(storage);reload.render(partial());assert.equal(reload.panel.hidden,true);assert.deepEqual([...storage.values()],['job']);assert.equal([...storage.values()].some(value=>value.includes('private diagnostic')),false);
});
test('dismissal is isolated by host, operation and resource kind',()=>{
 const storage=new Map(),first=page(storage);first.render(partial(),'ego','images');first.dismiss();
 for(const [op,host,kind] of [[partial(),'other','images'],[partial(),'ego','volumes'],[partial('new-job'),'ego','images']]){const next=page(storage);next.render(op,host,kind);assert.equal(next.panel.hidden,false);}
 const same=page(storage);same.render(partial(),'ego','images');assert.equal(same.panel.hidden,true);
});
test('Dismiss is in the first heading, before long result lists, and polling keeps reading position',()=>{
 const p=page(),op=partial();op.results=Array.from({length:200},()=>op.results[0]);op.total=200;p.render(op);
 assert.equal(p.panel.children[0].className,'catalog-operation-heading');assert.equal(p.panel.children[0].children[1].textContent,'Dismiss');assert.equal(p.panel.children[1].tagName,'p');
 p.panel.scrollTop=150;p.render(op);assert.equal(p.panel.scrollTop,150);p.render(partial('another-job'));assert.equal(p.panel.scrollTop,0);
});
test('storage failures preserve in-page dismissal and keep rendering usable',()=>{
 const p=page(new Map(),true);p.render(partial());p.dismiss();p.render(partial());assert.equal(p.panel.hidden,true);p.render(partial('next'));assert.equal(p.panel.hidden,false);
});
test('successful results auto-hide and running operations never expose Dismiss',()=>{
 const p=page(),op=partial();op.results[0].ok=true;p.render(op);assert.equal(p.panel.hidden,true);
 op.running=true;p.render(op);assert.equal(p.panel.hidden,false);assert.equal(p.panel.children[0].children.length,1);
});
test('a stale Dismiss button cannot hide a new host or a restarted operation',()=>{
 const p=page();p.render(partial(),'ego');const close=p.panel.children[0].children[1];p.render(partial('next'),'other');close.listeners.click();assert.equal(p.panel.hidden,false);
 p.render(partial());const old=p.panel.children[0].children[1];const running=partial();running.running=true;p.render(running);old.listeners.click();assert.equal(p.panel.hidden,false);
});
