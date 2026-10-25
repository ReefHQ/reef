'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const source=fs.readFileSync(path.join(__dirname,'../web/console.js'),'utf8');
function page(){
 const nodes=new Map(),requests=[],timers=new Map(),events={};let next=0,term;
 const node=id=>{if(!nodes.has(id))nodes.set(id,{value:id==='#console-shell'?'/bin/sh':'',content:'csrf-value',textContent:'',disabled:false,hidden:true,events:{},addEventListener(n,fn){this.events[n]=fn;}});return nodes.get(id);};
 class Terminal{constructor(){term=this;this.cols=80;this.rows=24;this.output=[];}loadAddon(){}open(){}onData(fn){this.data=fn;}onBinary(fn){this.binary=fn;}reset(){this.output=[];}write(b,fn){this.output.push(new TextDecoder().decode(b));fn();}focus(){}clear(){this.output=[];}}
 const window={addEventListener(n,fn){events[n]=fn;}};
 vm.runInNewContext(source,{document:{querySelector:node,body:{dataset:{containerId:'a'.repeat(64)}}},window,location:{},navigator:{sendBeacon(){}},Terminal,FitAddon:{FitAddon:class{fit(){}}},ResizeObserver:class{observe(){}},TextEncoder,TextDecoder,Uint8Array,URLSearchParams,AbortController,atob,btoa,Promise,
  setTimeout(fn){timers.set(++next,fn);return next;},clearTimeout(id){timers.delete(id);},fetch(url,options){return new Promise((resolve,reject)=>requests.push({url,options,resolve,reject}));}
 });
 return {node,requests,timers,term,events};
}
const settle=async()=>{for(let i=0;i<12;i++)await Promise.resolve();};
function ready(p,index=0){
 let stream;
 const body=new ReadableStream({start(controller){stream=controller;}});
 p.requests[index].resolve({ok:true,status:200,body});
 const frame=value=>stream.enqueue(new TextEncoder().encode(JSON.stringify(value)+'\n'));
 return {stream,frame};
}
test('console waits for explicit Connect and binds input to the returned session',async()=>{
 const p=page();assert.equal(p.requests.length,0);
 p.node('#console-connect-form').events.submit({preventDefault(){}});
 assert.equal(p.requests[0].url,'/api/console/open');assert.equal(p.requests[0].options.body.get('confirmed'),'yes');assert.equal(p.requests[0].options.body.get('csrf'),'csrf-value');
 const {frame}=ready(p);frame({type:'ready',id:'owned-session',name:'fixture'});frame({type:'output',data:btoa('hello\r\n')});await settle();
 assert.equal(p.term.output.join(''),'hello\r\n');assert.equal(p.node('#console-connect').disabled,true);
 p.term.data('echo hi\r');const [id,fn]=p.timers.entries().next().value;p.timers.delete(id);fn();await settle();
 assert.equal(p.requests[1].url,'/api/console/input');assert.equal(p.requests[1].options.body.get('console'),'owned-session');assert.equal(atob(p.requests[1].options.body.get('data')),'echo hi\r');
 p.requests[1].resolve({ok:true,status:204});await settle();p.node('#console-disconnect').events.click();
 assert.equal(p.requests[0].options.signal.aborted,true);assert.equal(p.requests[2].url,'/api/console/close');assert.equal(p.node('#console-connect').disabled,false);
});
test('reconnect resets the input timer and ignores late data from the previous connection',async()=>{
 const p=page();const open=()=>p.node('#console-connect-form').events.submit({preventDefault(){}});
 open();const old=ready(p);old.frame({type:'ready',id:'first',name:'fixture'});await settle();
 p.term.data('discard this');p.node('#console-disconnect').events.click();
 open();const second=ready(p,2);second.frame({type:'ready',id:'second',name:'fixture'});await settle();
 old.frame({type:'output',data:btoa('stale')});await settle();assert.equal(p.term.output.join(''),'');
 p.term.data('new input');const [id,fn]=p.timers.entries().next().value;p.timers.delete(id);fn();await settle();
 const input=p.requests.find(r=>r.url==='/api/console/input');assert.ok(input);assert.equal(input.options.body.get('console'),'second');assert.equal(atob(input.options.body.get('data')),'new input');
});
test('connection errors restore controls and oversized input queues disconnect',async()=>{
 const p=page();p.node('#console-connect-form').events.submit({preventDefault(){}});p.requests[0].resolve({ok:false,status:503,text:async()=> 'Selected shell is missing.'});await settle();
 assert.equal(p.node('#console-error').textContent,'Selected shell is missing.');assert.equal(p.node('#console-connect').disabled,false);
 p.node('#console-connect-form').events.submit({preventDefault(){}});const {frame}=ready(p,1);frame({type:'ready',id:'session',name:'fixture'});await settle();
 p.term.data('x'.repeat(32769));assert.match(p.node('#console-error').textContent,/queue is full/);assert.equal(p.requests[1].options.signal.aborted,true);
});
