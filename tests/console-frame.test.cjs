'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const source=fs.readFileSync(path.join(__dirname,'../web/console-frame.js'),'utf8');
function page(){
 const nodes=new Map(),events={},messages=[],timers=new Map();let term,next=0;
 const find=id=>{if(!nodes.has(id))nodes.set(id,{value:id==='#console-shell'?'/bin/sh':'',events:{},addEventListener(n,fn){this.events[n]=fn;}});return nodes.get(id);};
 class Terminal{constructor(){term=this;this.cols=80;this.rows=24;this.output='';}loadAddon(){}open(){}onData(fn){this.data=fn;}onBinary(){}focus(){}clear(){}reset(){this.output='';}write(b){this.output+=new TextDecoder().decode(b);}}
 const parent={postMessage(message){messages.push(message);}};
 vm.runInNewContext(source,{document:{querySelector:find,body:{dataset:{consoleChannel:'console-1'}}},window:{addEventListener(n,fn){events[n]=fn;}},location:{origin:'http://reef.test'},parent,Terminal,FitAddon:{FitAddon:class{fit(){}}},ResizeObserver:class{observe(){}},Uint8Array,TextEncoder,TextDecoder,atob,btoa,Promise,setTimeout(fn){timers.set(++next,fn);return next;},clearTimeout(id){timers.delete(id);}});
 const incoming=message=>events.message({origin:'http://reef.test',source:parent,data:{type:'reef-console-host',channel:'console-1',...message}});
 return {find,events,messages,timers,term,incoming};
}
const settle=async()=>{for(let i=0;i<10;i++)await Promise.resolve();};
test('embedded consoles keep processing output while inactive and send ordered input through the parent',async()=>{
 const p=page();p.find('#console-connect-form').events.submit({preventDefault(){}});const open=p.messages.find(m=>m.action==='open');assert.ok(open);p.incoming({frame:{type:'ready'}});p.incoming({request:open.request,ok:true});await settle();
 p.incoming({action:'activate',active:false});p.incoming({frame:{type:'output',data:btoa('background output')}});assert.equal(p.term.output,'background output');assert.equal(p.messages.some(m=>m.action==='close'),false);
 p.term.data('echo hi\r');const inputTimer=[...p.timers].find(([id])=>id!==open.request);assert.ok(inputTimer);p.timers.delete(inputTimer[0]);inputTimer[1]();await settle();
 const input=p.messages.find(m=>m.action==='input');assert.ok(input);assert.equal(atob(input.fields.data),'echo hi\r');p.incoming({request:input.request,ok:true});await settle();
 p.find('#console-disconnect').events.click();assert.ok(p.messages.some(m=>m.action==='close'));assert.equal(p.find('#console-connect').disabled,false);
});
test('embedded console rejects controls from foreign origins and unknown sources',()=>{
 const p=page();p.events.message({origin:'http://evil.test',source:{},data:{type:'reef-console-host',channel:'console-1',frame:{type:'output',data:btoa('bad')}}});assert.equal(p.term.output,'');
});
