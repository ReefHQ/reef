'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm'),path=require('node:path');
const source=fs.readFileSync(path.join(__dirname,'../web/volume-backups.js'),'utf8');
const volume={name:'database',driver:'local',scope:'local',fingerprint:'a'.repeat(64)},record={id:'b'.repeat(48),volume:'database',status:'succeeded',created:'2026-10-29T10:00:00Z',bytes:1048576,message:'Verified archive',containers:[]};
function page({host='local',caps=[],write=true,confirm=async()=>true,records=[record]}={}){
 const nodes=new Map(),events={},requests=[],timers=[];let operation={},operationRenders=0,readGate;
 class Node{constructor(tag='div',id=''){this.tagName=tag;this.id=id;this.value='';this.hidden=false;this.disabled=false;this.children=[];this.listeners={};this.open=false;this.textContent='';this.scrollTop=0;}setAttribute(k,v){this[k]=v;}insertBefore(n,before){const old=this.children.indexOf(n);if(old>=0)this.children.splice(old,1);const at=before?this.children.indexOf(before):this.children.length;this.children.splice(at,0,n);this.onChange?.();}removeChild(n){this.children.splice(this.children.indexOf(n),1);this.onChange?.();}addEventListener(k,f){(this.listeners[k]??=[]).push(f);}async fire(k){for(const f of this.listeners[k]||[])await f({preventDefault(){},target:this});}append(...ns){this.children.push(...ns);if(this.id==='backups-volume'&&!this.value)this.value=ns[0]?.value||'';}replaceChildren(...ns){this.children=ns;if(this.id==='backups-volume')this.value='';}showModal(){this.open=true;}close(){this.open=false;this.fire('close');}scrollIntoView(){}focus(){}}
 const find=id=>{if(!nodes.has(id))nodes.set(id,new Node('div',id));return nodes.get(id);};
 const window={reefHost:host,reefHostName:host,reefCapabilities:caps,reefAccess:{canWrite:write},ReefOperationPanels:{render(){operationRenders++;}},addEventListener(k,f){(events[k]??=[]).push(f);}};
 const context={window,document:{getElementById:find,createElement:t=>new Node(t),addEventListener(){},hidden:false},URLSearchParams,csrf:'csrf-fixture',ask:confirm,setTimeout:f=>{timers.push(f);return timers.length;},clearTimeout(){},request:async(url,opts,selected)=>{requests.push({url,opts,host:selected});if(readGate&&!opts&&url.startsWith('/api/volume-backups'))await readGate;return{json:async()=>url==='/api/volumes'?{volumes:[volume]}:{backups:records,total:records.length,offset:0,retention:7,operation,preview:{name:volume.name,fingerprint:volume.fingerprint,usage:'c'.repeat(64),containers:[{id:'d'.repeat(64),name:'db-running',running:true},{id:'e'.repeat(64),name:'db-stopped',running:false}]}}};}};
 vm.runInNewContext(source,context);return{find,window,requests,timers,setRecords(rows){records=rows;},setOperation(op){operation=op;},operationRenders(){return operationRenders;},holdRead(){let release;readGate=new Promise(r=>{release=r;});return()=>{readGate=undefined;release();};},async settle(){for(let i=0;i<30;i++)await Promise.resolve();},async open(){await window.ReefVolumeBackups.open(volume);await this.settle();},switchHost(){window.reefHost='other';for(const f of events['reef-host-changed']||[])f();}};
}
test('backup reviews active containers and pins the host, volume identity, usage and CSRF',async()=>{
 let confirmation='';const p=page({host:'remote',caps:['volume-backups-v1'],confirm:async text=>{confirmation=text;return true;}});await p.open();await p.find('backups-create').fire('click');await p.settle();const call=p.requests.find(r=>r.opts?.method==='POST');assert.equal(call.host,'remote');for(const [key,value] of Object.entries({action:'backup',volume:'database',fingerprint:'a'.repeat(64),usage:'c'.repeat(64),csrf:'csrf-fixture',confirmed:'yes'}))assert.equal(call.opts.body.get(key),value);assert.match(confirmation,/db-running/);assert.doesNotMatch(confirmation,/db-stopped/);assert.match(confirmation,/downtime/);assert.match(confirmation,/not a database dump/);
});
test('viewer sees backup metadata with every mutation disabled; old agents get upgrade guidance',async()=>{
 const p=page({write:false,records:[{...record,message:'<script>private data</script>'}]});await p.open();assert.equal(p.find('backups-create').disabled,true);const card=p.find('backups-records').children[0];assert.equal(card.children[2].textContent,'<script>private data</script>');assert.ok(card.children.at(-1).children.every(n=>n.disabled));
 const old=page({host:'old',caps:['volumes-v1']});await old.open();assert.equal(old.requests.length,0);assert.equal(old.find('backups-create').disabled,true);assert.match(old.find('backups-error').textContent,/Upgrade/);
});
test('host change during backup confirmation cancels the POST and closes the modal',async()=>{
 let answer;const p=page({confirm:()=>new Promise(resolve=>{answer=resolve;})});await p.open();const pending=p.find('backups-create').fire('click');await p.settle();assert.equal(typeof answer,'function');p.switchHost();answer(true);await pending;await p.settle();assert.equal(p.find('volume-backups').open,false);assert.ok(p.requests.every(r=>!r.opts));
});
test('guided restoration requires a new name and never submits an overwrite option',async()=>{
 let confirmation='';const p=page({confirm:async text=>{confirmation=text;return true;}});await p.open();await p.find('backups-records').children[0].children.at(-1).children[0].fire('click');assert.equal(p.find('backups-restore-form').hidden,false);p.find('backups-restore-name').value='database-recovered';await p.find('backups-restore-form').fire('submit');await p.settle();const body=p.requests.find(r=>r.opts)?.opts.body;assert.equal(body.get('action'),'restore');assert.equal(body.get('backup'),record.id);assert.equal(body.get('name'),'database-recovered');assert.equal(body.has('overwrite'),false);assert.match(confirmation,/NEW volume/);assert.match(confirmation,/Existing volumes/);
});
test('failed and interrupted archives have no restore action and expose recovery identities',async()=>{
 const p=page({records:[{...record,status:'interrupted',containers:['d'.repeat(64)],message:'Check container state'}]});await p.open();const card=p.find('backups-records').children[0];assert.match(card.children[3].textContent,/dddd/);assert.equal(card.children.at(-1).children.length,1);assert.match(card.children.at(-1).children[0].textContent,/Delete/);
});

test('polling keeps unchanged backup cards and operation output instead of replacing focused controls',async()=>{
 const p=page();await p.open();const list=p.find('backups-records'),card=list.children[0],restore=card.children.at(-1).children[0],renders=p.operationRenders();
 await p.find('backups-refresh').fire('click');assert.equal(list.children[0],card);assert.equal(list.children[0].children.at(-1).children[0],restore);assert.equal(p.operationRenders(),renders);
});
test('refresh preserves reading position and restoration draft while records change and operation finishes',async()=>{
 const p=page();await p.open();await p.find('backups-records').children[0].children.at(-1).children[0].fire('click');
 const dialog=p.find('volume-backups'),list=p.find('backups-records'),card=list.children[0];dialog.scrollTop=260;p.find('backups-restore-name').value='recovery-draft';list.onChange=()=>{dialog.scrollTop=0;};
 const newRecord={...record,id:'e'.repeat(48),status:'pending'};p.setRecords([newRecord,record]);p.setOperation({id:'operation',action:'backup',running:true,total:1,results:[]});
 await p.find('backups-refresh').fire('click');assert.equal(dialog.scrollTop,260);assert.equal(list.children[1],card);assert.equal(card.children.at(-1).children[0].disabled,true);assert.equal(p.find('backups-restore-name').value,'recovery-draft');assert.equal(p.find('backups-restore-form').hidden,false);
 p.setRecords([{...newRecord,status:'succeeded'},record]);p.setOperation({id:'operation',action:'backup',running:false,total:1,results:[{id:'volume',ok:true,message:'done'}]});await p.find('backups-refresh').fire('click');
 assert.equal(dialog.scrollTop,260);assert.equal(list.children[1],card);assert.equal(card.children.at(-1).children[0].disabled,false);assert.equal(p.find('backups-restore-name').value,'recovery-draft');assert.equal(p.find('backups-records')['aria-busy'],'false');
});
test('delayed refresh marks the retained list busy and ignores a response after host change',async()=>{
 const p=page({host:'ego',caps:['volume-backups-v1']});await p.open();const card=p.find('backups-records').children[0],release=p.holdRead();const reading=p.find('backups-refresh').fire('click');await p.settle();
 assert.equal(p.find('backups-loading').hidden,false);assert.equal(p.find('backups-records')['aria-busy'],'true');assert.equal(p.find('backups-records').children[0],card);
 p.switchHost();p.setRecords([{...record,volume:'late-private-result'}]);release();await reading;assert.equal(p.find('volume-backups').open,false);assert.equal(p.find('backups-records').children[0],card);
});
test('record refresh removes stale actions and retains the requested order without duplicating cards',async()=>{
 const second={...record,id:'f'.repeat(48),volume:'other-volume'},p=page({records:[record,second]});await p.open();
 const list=p.find('backups-records'),firstCard=list.children[0],secondCard=list.children[1];p.setRecords([second,record]);await p.find('backups-refresh').fire('click');
 assert.deepEqual(list.children,[secondCard,firstCard]);p.setRecords([second]);await p.find('backups-refresh').fire('click');assert.deepEqual(list.children,[secondCard]);
 p.setRecords([]);await p.find('backups-refresh').fire('click');assert.equal(list.children.length,0);assert.equal(p.find('backups-empty').hidden,false);
});
