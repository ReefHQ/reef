'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const source=fs.readFileSync(path.join(__dirname,'../web/console-dock.js'),'utf8');
function page(limit=8){
 const nodes=new Map(),events={},requests=[];
 function element(tag){const classes=new Set();return {tag,children:[],events:{},dataset:{},style:{},attributes:{},hidden:false,contentWindow:{messages:[],postMessage(message){this.messages.push(message);}},classList:{toggle(c,value){value?classes.add(c):classes.delete(c);},add(c){classes.add(c);},remove(c){classes.delete(c);},contains(c){return classes.has(c);}},append(...children){for(const child of children){child.parent=this;this.children.push(child);}},remove(){this.parent.children=this.parent.children.filter(c=>c!==this);},setAttribute(k,v){this.attributes[k]=v;},addEventListener(name,fn){this.events[name]=fn;},focus(){},scrollIntoView(){},setPointerCapture(){}};}
 const find=id=>{if(!nodes.has(id))nodes.set(id,element('div'));return nodes.get(id);};find('meta[name="csrf-token"]').content='csrf';
 const body=element('body');body.dataset.consoleSessionLimit=String(limit);const window={innerHeight:900,addEventListener(name,fn){events[name]=fn;}};
 vm.runInNewContext(source,{document:{querySelector:find,createElement:element,body},window,location:{origin:'http://reef.test'},navigator:{sendBeacon(){}},URLSearchParams,AbortController,TextDecoder,Promise,fetch(url,options){return new Promise((resolve,reject)=>requests.push({url,options,resolve,reject}));}});
 const frames=()=>find('#console-dock-panes').children.map(p=>p.children[0]);
 const message=(frame,action,fields={},request=1)=>events.message({origin:'http://reef.test',source:frame.contentWindow,data:{type:'reef-console-frame',channel:new URL('http://reef.test'+frame.src).searchParams.get('channel'),action,fields,request}});
 return {find,body,events,requests,frames,message,open:window.openContainerConsole};
}
const c={id:'a'.repeat(64),name:'web-1',compose_project:'app',state:'running'};
const settle=async()=>{for(let i=0;i<16;i++)await Promise.resolve();};
test('multiple shells on the same container keep their frames alive when switching, reducing and maximizing',()=>{
 const p=page();p.open(c);p.open(c);const frames=p.frames();assert.equal(frames.length,2);assert.notEqual(frames[0],frames[1]);
 const groups=p.find('#console-dock-tabs').children;groups[0].children[0].events.click();assert.equal(p.find('#console-dock-panes').children[0].hidden,false);
 p.find('#console-dock-minimize').events.click();assert.equal(p.find('#console-dock').classList.contains('is-collapsed'),true);assert.equal(p.frames()[0],frames[0]);
 p.find('#console-dock-maximize').events.click();assert.equal(p.body.classList.contains('console-maximized'),true);assert.equal(p.frames()[1],frames[1]);assert.equal(p.requests.length,0);
 groups[0].children[1].events.click();assert.equal(p.frames().length,1);assert.equal(p.frames()[0],frames[1]);
});
test('sessions share one output connection and route frames and input by console identity',async()=>{
 const p=page();p.open(c);p.open(c);const [one,two]=p.frames();p.message(one,'open',{shell:'/bin/sh',cols:80,rows:24},1);p.message(two,'open',{shell:'/bin/sh',cols:80,rows:24},2);
 assert.equal(p.requests.length,1);assert.equal(p.requests[0].url,'/api/console/stream');let writer;const body=new ReadableStream({start(controller){writer=controller;}});p.requests[0].resolve({ok:true,status:200,body});
 const frame=data=>writer.enqueue(new TextEncoder().encode(JSON.stringify(data)+'\n'));frame({type:'transport',id:'shared'});await settle();
 assert.equal(p.requests.filter(r=>r.url==='/api/console/open').length,2);for(const req of p.requests.slice(1))req.resolve({ok:true,status:200});await settle();
 frame({type:'ready',client:'console-1',console:'first'});frame({type:'ready',client:'console-2',console:'second'});frame({type:'output',client:'console-2',console:'second',data:btoa('worker output')});await settle();
 assert.equal(one.contentWindow.messages.some(m=>m.frame?.type==='output'),false);assert.equal(two.contentWindow.messages.find(m=>m.frame?.type==='output').frame.data,btoa('worker output'));
 p.message(one,'input',{data:btoa('echo hi\r')},3);await settle();assert.equal(p.requests.at(-1).options.body.get('console'),'first');p.requests.at(-1).resolve({ok:true,status:204});await settle();
 p.find('#console-dock-tabs').children[0].children[1].events.click();assert.equal(p.requests[0].options.signal.aborted,false);assert.equal(p.frames()[0],two);
 p.find('#console-dock-tabs').children[0].children[1].events.click();assert.equal(p.requests[0].options.signal.aborted,true);
});
test('console panel enforces the configured tab limit and ignores messages from another frame',()=>{
 const p=page(2);p.open(c);p.open(c);p.open(c);assert.equal(p.frames().length,2);assert.match(p.find('#console-dock-error').textContent,/2 console tabs/);
 const frame=p.frames()[0];p.events.message({origin:'http://evil.test',source:frame.contentWindow,data:{type:'reef-console-frame',channel:'console-1',action:'open'}});p.events.message({origin:'http://reef.test',source:{},data:{type:'reef-console-frame',channel:'console-1',action:'open'}});assert.equal(p.requests.length,0);
 assert.equal(p.events.beforeunload,undefined);assert.equal(typeof p.events.pagehide,'function');
});
test('remote console keeps its original host while sharing the local panel connection',async()=>{
 const p=page();p.open({...c,host_id:'remote-one',host_name:'Server one',inventory_only:false});const remote=p.frames()[0];p.open(c);const local=p.frames()[1];
 assert.equal(new URL('http://reef.test'+remote.src).searchParams.get('host'),'remote-one');
 p.message(remote,'open',{shell:'/bin/sh',cols:80,rows:24});p.message(local,'open',{shell:'/bin/sh',cols:80,rows:24});assert.equal(p.requests.length,1);assert.equal(p.requests[0].url,'/api/console/stream');
 let writer;const body=new ReadableStream({start(controller){writer=controller;}});p.requests[0].resolve({ok:true,status:200,body});writer.enqueue(new TextEncoder().encode(JSON.stringify({type:'transport',id:'shared'})+'\n'));await settle();
 assert.equal(p.requests.filter(r=>r.url==='/api/console/open?host=remote-one').length,1);assert.equal(p.requests.filter(r=>r.url==='/api/console/open').length,1);
 for(const request of p.requests.slice(1))request.resolve({ok:true,status:200});await settle();p.find('#console-dock-tabs').children[1].children[1].events.click();assert.equal(p.frames()[0],remote);assert.equal(p.requests[0].options.signal.aborted,false);
 p.find('#console-dock-tabs').children[0].children[1].events.click();
});
