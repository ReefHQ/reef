'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs');
function page({capability=true,current='0.10.0',central='0.11.0',available=true,operation={}}={}){
 const nodes=new Map(),requests=[];let confirm=async()=>true,host={id:'remote-one',name:'Remote One',status:'connected',version:current,capabilities:capability?['agent-update-v1']:[]};
 class Element{
  constructor(tag='div'){this.tagName=tag.toUpperCase();this.children=[];this.listeners={};this.value='';this.textContent='';this.open=false;this.hidden=false;this.disabled=false;}
  append(...items){this.children.push(...items);}replaceChildren(...items){this.children=items;}setAttribute(){}contains(){return false;}addEventListener(type,fn){this.listeners[type]=fn;}showModal(){this.open=true;}close(){this.open=false;}get options(){return this.children;}
 }
 const find=selector=>{if(!nodes.has(selector))nodes.set(selector,new Element());return nodes.get(selector);};
 const dialog=find('#hosts-dialog');dialog.open=true;
 const window={dispatchEvent(){}};
 const context={window,document:{querySelector:find,createElement:tag=>new Element(tag),activeElement:null},ReefNavigation:{read:()=>({host:'local'})},Option:function(label,value){this.value=value;},URLSearchParams,CustomEvent:function(){},csrf:'private-csrf',setInterval(fn){context.poll=fn;},ask:message=>confirm(message),request:async(url,options)=>{
  requests.push({url,options});
  if(url==='/api/hosts')return {json:async()=>({enabled:true,hosts:[host],version:central,updates_available:true})};
  if(options?.method==='POST')return {status:202};
  return {json:async()=>({available,operation})};
 }};
 vm.createContext(context);vm.runInContext(fs.readFileSync('web/hosts.js','utf8'),context);
 const flush=async()=>{for(let i=0;i<30;i++)await Promise.resolve();};
 const all=el=>[el,...el.children.flatMap(all)];
 return {context,requests,flush,nodes,async ready(){await window.reefHostsReady;},button:()=>all(find('#hosts-list')).find(n=>n.tagName==='BUTTON'&&n.textContent.startsWith('Update to')),text:()=>all(find('#hosts-list')).map(n=>n.textContent).join('\n'),confirm(fn){confirm=fn;},revoke(){host={...host,status:'revoked'};},async refresh(){await context.poll();await flush();}};
}
test('agent update pins its host and central version and requires confirmation',async()=>{
 const p=page();await p.ready();const button=p.button();assert.equal(button.disabled,false);
 let message='';p.confirm(async text=>{message=text;return true;});await button.listeners.click();
 const actions=p.requests.filter(r=>r.options?.method==='POST');assert.equal(actions.length,1);assert.equal(actions[0].url,'/api/agent-update?host=remote-one');
 const form=actions[0].options.body;assert.equal(form.get('version'),'0.11.0');assert.equal(form.get('confirmed'),'yes');assert.equal(form.get('csrf'),'private-csrf');assert.equal(form.has('image'),false);assert.equal(form.has('revision'),false);assert.match(message,/Remote One.*0\.10\.0.*0\.11\.0/);
});
test('legacy, offline updater, equal/newer agents and running updates cannot be updated',async()=>{
 for(const options of [{capability:false},{available:false},{current:'0.11.0'},{current:'0.12.0'},{operation:{id:'job',phase:'reconnecting',version:'0.11.0',message:'Waiting for reconnection'}}]){
  const p=page(options);await p.ready();assert.equal(p.button().disabled,true,JSON.stringify(options));
 }
});
test('cancelled and revoked-host confirmations never dispatch an update',async()=>{
 const p=page();await p.ready();p.confirm(async()=>false);await p.button().listeners.click();assert.equal(p.requests.filter(r=>r.options?.method==='POST').length,0);
 p.confirm(async()=>{p.revoke();await p.refresh();return true;});await p.button().listeners.click();assert.equal(p.requests.filter(r=>r.options?.method==='POST').length,0);
});
test('durable update status is rendered as text after page load and polled without replay',async()=>{
 const p=page({operation:{id:'job',version:'0.11.0',phase:'unknown',message:'<script>private output</script>'}});await p.ready();await p.refresh();assert.match(p.text(),/Update to 0\.11\.0: unknown/);assert.match(p.text(),/<script>private output<\/script>/);assert.equal(p.requests.filter(r=>r.options?.method==='POST').length,0);
});
