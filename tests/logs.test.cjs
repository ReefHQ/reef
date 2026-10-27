'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const source=fs.readFileSync(require('node:path').join(__dirname,'../web/logs.js'),'utf8');
function page(){
 const nodes=new Map(),events={},pending=[],timers=new Map();let next=0;
 function node(id){if(!nodes.has(id))nodes.set(id,{value:'',textContent:'',hidden:false,disabled:false,open:false,scrollTop:0,clientHeight:100,scrollHeight:200,events:{},attributes:{},addEventListener(name,fn){this.events[name]=fn;},setAttribute(name,value){this.attributes[name]=value;},replaceChildren(...children){this.value=children[0]?.value||'';},showModal(){this.open=true;},close(){this.open=false;this.events.close();}});return nodes.get(id);}
 node('#log-stream').value='all';node('#log-tail').value='200';
 const document={hidden:false,querySelector:node,addEventListener(name,fn){events[name]=fn;}};
 const window={};
 vm.runInNewContext(source,{document,window,Option:function(label,value){return {label,value};},URLSearchParams,AbortController,Date,
  setTimeout(fn){timers.set(++next,fn);return next;},clearTimeout(id){timers.delete(id);},
  request(url,options,host){return new Promise((resolve,reject)=>pending.push({url,host,signal:options.signal,resolve(value){resolve({json:async()=>value});},reject}));}
 });
 return {node,document,events,pending,timers,open:window.openContainerLogs};
}
const containers=[{id:'a'.repeat(64),name:'web-1',compose_service:'web',state:'running'},{id:'b'.repeat(64),name:'worker-1',compose_service:'worker',state:'exited'}];
const settle=async()=>{for(let i=0;i<5;i++)await Promise.resolve();};
test('log viewer rejects stale responses after selecting another service and renders output as text',async()=>{
 const p=page();p.open({containers,project:'App'});
 assert.match(p.pending[0].url,/stream=all&tail=200/);
 p.node('#log-container').value=containers[1].id;p.node('#log-container').events.change();
 assert.equal(p.pending[0].signal.aborted,true);
 p.pending[1].resolve({text:'<script>output</script>',name:'worker-1',checked_at:'2026-10-23T10:00:00Z',truncated:true});await settle();
 p.pending[0].resolve({text:'stale output',name:'web-1',checked_at:'2026-10-23T10:00:00Z'});await settle();
 assert.equal(p.node('#container-log-output').textContent,'<script>output</script>');
 assert.equal(p.node('#container-logs-limit').hidden,false);
 assert.equal(p.timers.size,1);
 p.node('#container-logs').close();assert.equal(p.timers.size,0);
});
test('pause cancels polling, resume loads again, and close aborts outstanding reads',async()=>{
 const p=page();p.open({containers});
 p.pending[0].resolve({text:'hello',name:'web-1',checked_at:'2026-10-23T10:00:00Z'});await settle();
 p.node('#toggle-container-logs').events.click();assert.equal(p.timers.size,0);assert.equal(p.node('#toggle-container-logs').attributes['aria-pressed'],'false');
 p.node('#toggle-container-logs').events.click();assert.equal(p.pending.length,2);
 p.node('#container-logs').close();assert.equal(p.pending[1].signal.aborted,true);
 p.pending[1].resolve({text:'late',name:'web-1',checked_at:'2026-10-23T10:00:00Z'});await settle();
 assert.equal(p.node('#container-log-output').textContent,'hello');assert.equal(p.timers.size,0);
});
test('errors keep the last output and pause polling; empty projects do not request logs',async()=>{
 const p=page();p.open({containers});
 p.pending[0].resolve({text:'last successful snapshot',name:'web-1',checked_at:'2026-10-23T10:00:00Z'});await settle();
 p.node('#refresh-container-logs').events.click();p.pending[1].reject(Error('Docker unavailable'));await settle();
 assert.equal(p.node('#container-log-output').textContent,'last successful snapshot');assert.equal(p.node('#container-logs-error').hidden,false);assert.equal(p.timers.size,0);
 p.open({containers:[],project:'Stopped'});assert.equal(p.pending.length,2);assert.equal(p.node('#refresh-container-logs').disabled,true);assert.equal(p.node('#toggle-container-logs').disabled,true);
});
test('hidden tabs abort reads and resume when visible',()=>{
 const p=page();p.open({containers});p.document.hidden=true;p.events.visibilitychange();
 assert.equal(p.pending[0].signal.aborted,true);assert.equal(p.timers.size,0);
 p.document.hidden=false;p.events.visibilitychange();assert.equal(p.pending.length,2);
});
test('automatic refresh keeps dialog controls and status stable while preserving the reading position',async()=>{
 const p=page();p.open({containers,project:'App'});
 p.pending[0].resolve({text:'first output',name:'web-1',checked_at:'2026-10-23T10:00:00Z'});await settle();
 const title=p.node('#container-logs-title').textContent,status=p.node('#container-logs-status').textContent,selection=p.node('#log-container').value;
 p.node('#container-log-output').scrollTop=10;
 const [id,poll]=p.timers.entries().next().value;p.timers.delete(id);poll();
 assert.equal(p.node('#container-logs-status').textContent,status);
 assert.equal(p.node('#refresh-container-logs').disabled,false);
 assert.equal(p.node('#container-log-output').textContent,'first output');
 p.pending[1].resolve({text:'updated output',name:'web-1',checked_at:'2026-10-23T10:00:03Z'});await settle();
 assert.equal(p.node('#container-log-output').textContent,'updated output');
 assert.equal(p.node('#container-log-output').scrollTop,10);
 assert.equal(p.node('#container-logs-title').textContent,title);
 assert.equal(p.node('#log-container').value,selection);
});

test('remote log snapshots keep the selected host pinned',()=>{
 const p=page();p.open({containers:[{...containers[0],host_id:'remote-host',host_name:'Remote',inventory_only:false}]});assert.equal(p.pending[0].host,'remote-host');assert.match(p.node('#container-logs-title').textContent,/Remote/);
});
