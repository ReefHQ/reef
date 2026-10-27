'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm'),path=require('node:path');
const {imageUpdateSummary}=require('../web/model.js');
const source=fs.readFileSync(path.join(__dirname,'../web/image-updates.js'),'utf8');
function page({host='local',report={},busy=false,fail=false}={}){
 const nodes=new Map(),events={},requests=[];let resolve;
 const pending=new Promise(r=>resolve=r);
 function node(){return {textContent:'',className:'',disabled:false,children:[],listeners:{},append(...items){this.children.push(...items);},replaceChildren(...items){this.children=items;},addEventListener(type,fn){this.listeners[type]=fn;}};}
 const find=selector=>{if(!nodes.has(selector))nodes.set(selector,node());return nodes.get(selector);};
 const project={id:'project-1',busy,image_updates:report};
 const window={reefHost:host,addEventListener(type,fn){events[type]=fn;}};
 const context={window,document:{querySelector:find,createElement:node},current:()=>project,projects:[project],csrf:'fixture-csrf',ReefUI:{imageUpdateSummary},URLSearchParams,request:async(url,options)=>{requests.push({url,options});await pending;if(fail)throw Error('Registry request rejected');},message:text=>find('#message').textContent=text};
 vm.runInNewContext(source,context);
 return {project,nodes,requests,window,events,resolve,check:()=>window.ReefImageUpdates.check(project.id)};
}
test('summary distinguishes partial errors, available updates and no comparison baseline',()=>{
 const report={checked_at:'2026-10-27T12:00:00Z',services:[{status:'available'},{status:'unknown'}]};
 assert.equal(imageUpdateSummary(report),'1 image update available · 1 unable to check');
 assert.equal(imageUpdateSummary({...report,checking:true}),'Checking image updates…');
 assert.equal(imageUpdateSummary({...report,services:[{status:'current'}]}),'Images are up to date');
 assert.equal(imageUpdateSummary({...report,services:[{status:'pinned'}]}),'No deployed tagged images to check');
 assert.equal(imageUpdateSummary({...report,error:'Check failed'}),'Check failed');
 assert.equal(imageUpdateSummary({checked_at:'0001-01-01T00:00:00Z'}),'Not checked yet');
});
test('manual check uses authenticated POST with CSRF and suppresses duplicate clicks',async()=>{
 const p=page(),first=p.check();await p.check();assert.equal(p.requests.length,1);
 assert.equal(p.requests[0].url,'/api/image-updates');assert.equal(p.requests[0].options.method,'POST');
 assert.equal(p.requests[0].options.body.get('csrf'),'fixture-csrf');assert.equal(p.requests[0].options.body.get('id'),'project-1');
 assert.equal(p.nodes.get('#check-image-updates').disabled,true);
 p.resolve();await first;assert.equal(p.project.image_updates.checking,true);
});
test('remote hosts do not check local images; busy and checking projects disable controls',async()=>{
 const remote=page({host:'remote'});await remote.check();assert.equal(remote.requests.length,0);assert.equal(remote.nodes.get('#check-image-updates').disabled,true);
 assert.equal(page({busy:true}).nodes.get('#check-image-updates').disabled,true);
 assert.equal(page({report:{checking:true}}).nodes.get('#check-image-updates').disabled,true);
});
test('service metadata renders as text and failed manual requests restore controls',async()=>{
 const p=page({fail:true,report:{services:[{service:'<img>',image:'<script>',status:'available',message:'<b>note</b>'}]}});
 const items=p.nodes.get('#image-update-services').children;assert.equal(items[0].children[0].textContent,'<img>');assert.equal(items[0].children[2].textContent,'<script>');
 const request=p.check();p.resolve();await request;assert.equal(p.nodes.get('#check-image-updates').disabled,false);assert.equal(p.nodes.get('#message').textContent,'Registry request rejected');
});
