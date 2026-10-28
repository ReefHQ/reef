'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
function page(){
 const nodes=new Map(),calls=[];let confirm=async()=>true,pause=null;
 class Element{constructor(){this.listeners={};this.value='';this.checked=false;this.disabled=false;this.open=false;this.hidden=false;this.textContent='';}addEventListener(type,fn){this.listeners[type]=fn;}showModal(){this.open=true;}close(){this.open=false;this.listeners.close?.();}}
 const find=s=>{if(!nodes.has(s))nodes.set(s,new Element());return nodes.get(s);};
 let state={enabled:true,format:'json',configured:true,destination:'receiver.invalid',pending:0,sent:2,dropped:0,last_error:'',last_success:'0001'};
 const context={document:{querySelector:find},csrf:'private-csrf',URLSearchParams,setInterval(fn){context.tick=fn;},ask:message=>confirm(message),request:async(url,options,host)=>{
  calls.push({url,options,host});if(pause){const pending=pause;pause=null;await pending;}
  if(options?.method==='POST'){const fields=options.body;if(fields.get('action')==='save')state={...state,enabled:fields.get('enabled')==='true',configured:fields.get('clear')!=='yes'};else state={...state,pending:1};}
  return {json:async()=>({...state})};
 }};
 vm.createContext(context);vm.runInContext(fs.readFileSync('web/webhooks.js','utf8'),context);
 const flush=async()=>{for(let i=0;i<20;i++)await Promise.resolve();};
 return {nodes,calls,find,context,flush,confirm(fn){confirm=fn;},change(next){state={...state,...next};},pause(){let resolve;pause=new Promise(r=>resolve=r);return resolve;},async open(){find('#open-webhook').listeners.click();await flush();}};
}
test('webhook settings are global and never request or display the saved token',async()=>{
 const p=page();await p.open();assert.equal(p.calls[0].url,'/api/webhook');assert.equal(p.calls[0].host,'local');assert.equal(p.find('#webhook-url').value,'');assert.match(p.find('#webhook-destination').textContent,/receiver\.invalid/);assert.equal(p.find('#webhook-test').disabled,false);
});
test('save submits explicit format, destination, CSRF and confirmation then clears the URL field',async()=>{
 const p=page();await p.open();p.find('#webhook-url').value='https://receiver.invalid/private-token';p.find('#webhook-format').value='json';
 await p.find('#webhook-form').listeners.submit({preventDefault(){}});
 const r=p.calls.find(r=>r.options?.method==='POST');assert.equal(r.host,'local');assert.equal(r.options.body.get('url'),'https://receiver.invalid/private-token');assert.equal(r.options.body.get('confirmed'),'yes');assert.equal(r.options.body.get('csrf'),'private-csrf');assert.equal(r.options.body.get('format'),'json');assert.equal(p.find('#webhook-url').value,'');
});
test('status polling preserves unsaved fields and shows delivery failures as text',async()=>{
 const p=page();await p.open();p.find('#webhook-url').value='unsaved URL';p.find('#webhook-format').value='discord';p.find('#webhook-enabled').checked=false;
 p.change({pending:3,sent:4,dropped:2,last_error:'<script>receiver error</script>'});await p.context.tick();await p.flush();
 assert.equal(p.find('#webhook-url').value,'unsaved URL');assert.equal(p.find('#webhook-format').value,'discord');assert.equal(p.find('#webhook-enabled').checked,false);assert.match(p.find('#webhook-status').textContent,/3 waiting · 4 sent · 2 dropped/);assert.match(p.find('#webhook-status').textContent,/<script>/);
});
test('cancelled saves and confirmations belonging to a closed panel never write settings',async()=>{
 const p=page();await p.open();p.confirm(async()=>false);await p.find('#webhook-form').listeners.submit({preventDefault(){}});assert.equal(p.calls.filter(r=>r.options).length,0);
 p.confirm(async()=>{p.find('#webhook-close').listeners.click();return true;});await p.find('#webhook-form').listeners.submit({preventDefault(){}});assert.equal(p.calls.filter(r=>r.options).length,0);
});
test('test and remove actions use the saved destination without sending its token back',async()=>{
 const p=page();await p.open();await p.find('#webhook-test').listeners.click();await p.find('#webhook-remove').listeners.click();
 const actions=p.calls.filter(r=>r.options).map(r=>r.options.body);assert.equal(actions[0].get('action'),'test');assert.equal(actions[0].has('url'),false);assert.equal(actions[1].get('clear'),'yes');assert.equal(actions[1].get('enabled'),'false');assert.equal(p.find('#webhook-enabled').checked,false);
});
test('a late initial response cannot repopulate a closed dialog',async()=>{
 const p=page(),resume=p.pause();p.find('#open-webhook').listeners.click();p.find('#webhook-close').listeners.click();p.find('#webhook-url').value='';resume();await p.flush();assert.equal(p.find('#webhook-url').value,'');assert.equal(p.find('#webhook-dialog').open,false);
});
