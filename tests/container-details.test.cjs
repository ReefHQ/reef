'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const source=fs.readFileSync(path.join(__dirname,'../web/container-details.js'),'utf8');
function page(){
 const nodes=new Map(),logs=[],consoles=[];
 const element=tag=>({tag,textContent:'',dataset:{},events:{},attributes:{},children:[],open:false,hidden:false,append(...children){this.children.push(...children);},replaceChildren(...children){this.children=children;},addEventListener(name,fn){this.events[name]=fn;},setAttribute(name,value){this.attributes[name]=value;},showModal(){this.open=true;},close(){this.open=false;this.events.close?.();}});
 const find=id=>{if(!nodes.has(id))nodes.set(id,element('div'));return nodes.get(id);};const window={openContainerConsole(value){consoles.push(value);},openContainerLogs(value){logs.push(value);}};
 vm.runInNewContext(source,{window,document:{querySelector:find,createElement:element},encodeURIComponent});return {find,logs,consoles,window,api:window.ReefContainers};
}
const container={id:'a'.repeat(64),name:'<app>',image:'app:0.2.0',state:'running',status:'Up 1 hour',compose_project:'managed',compose_service:'web'};
test('container actions always place Logs before Console and disable Console for stopped containers',()=>{
 const p=page(),running=p.api.actions(container),stopped=p.api.actions({...container,state:'exited'});
 assert.deepEqual(Array.from(running.children,c=>c.textContent),['Logs','Console','Restart']);assert.deepEqual(Array.from(stopped.children,c=>c.textContent),['Logs','Console','Restart']);running.children[1].events.click();assert.equal(p.consoles[0].id,container.id);assert.equal(stopped.children[1].disabled,true);
});
test('container names open safe metadata details, and Logs transitions to the log viewer',()=>{
 const p=page();p.api.update([container]);p.api.name(container).events.click();assert.equal(p.find('#container-details').open,true);assert.equal(p.find('#container-detail-title').textContent,'<app>');
 const fields=p.find('#container-detail-fields').children;assert.ok(fields.some(c=>c.textContent===container.id));assert.ok(fields.some(c=>c.textContent==='web'));
 p.find('#container-detail-actions').children[0].children[0].events.click();assert.equal(p.find('#container-details').open,false);assert.equal(p.logs[0].id,container.id);
});
test('container details update state and remove stale actions when a container disappears',()=>{
 const p=page();p.api.open(container);p.api.update([{...container,state:'exited',status:'Exited'}]);assert.equal(p.find('#container-detail-actions').children[0].children[1].disabled,true);
 p.api.update([]);assert.equal(p.find('#container-detail-error').hidden,false);assert.equal(p.find('#container-detail-actions').children.length,0);
});
test('remote inventory exposes metadata with its host but cannot open local logs or consoles',()=>{
 const p=page(),remote={...container,host_id:'nas',host_name:'NAS',inventory_only:true};p.api.open(remote);
 const actions=p.find('#container-detail-actions').children[0].children;assert.equal(actions[0].disabled,true);assert.equal(actions[1].disabled,true);
 assert.ok(p.find('#container-detail-fields').children.some(c=>c.textContent==='NAS'));assert.equal(p.logs.length,0);assert.equal(p.consoles.length,0);
});

test('Compose containers expose individual restart with admin, running-state and negotiated remote guards',()=>{
 const p=page(),opened=[];p.window.ReefContainerManagement={open:c=>opened.push(c)};p.api.actions(container).children[2].events.click();assert.equal(opened[0].id,container.id);
 const remote={...container,host_id:'remote'};p.window.reefCapabilities=['containers-v1'];let button=p.api.actions(remote).children[2];assert.equal(button.disabled,true);assert.match(button.title,/0.28.0/);button.events.click();assert.equal(opened.length,1);
 p.window.reefCapabilities.push('compose-container-restart-v1');button=p.api.actions(remote).children[2];assert.equal(button.disabled,false);
 assert.equal(p.api.actions({...container,state:'exited'}).children[2].disabled,true);
 p.window.reefAccess={canWrite:false};assert.equal(p.api.actions(container).children.some(n=>n.textContent==='Restart'),false);
 p.window.reefAccess={canWrite:true};assert.equal(p.api.actions({...container,is_reef:true}).children.some(n=>n.textContent==='Restart'),false);
});
