'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm'),path=require('node:path');
const {imageUpdateSummary}=require('../web/model.js');
const source=fs.readFileSync(path.join(__dirname,'../web/image-updates.js'),'utf8');
function page({host='local',capabilities=[],report={},busy=false,fail=false}={}){
 const nodes=new Map(),events={},requests=[];let resolve;
 const pending=new Promise(r=>resolve=r);
 function node(tag='div'){return {tagName:tag,hidden:false,open:false,textContent:'',className:'',disabled:false,children:[],listeners:{},append(...items){this.children.push(...items);},replaceChildren(...items){this.children=items;},addEventListener(type,fn){this.listeners[type]=fn;}};}
 const find=selector=>{if(!nodes.has(selector))nodes.set(selector,node());return nodes.get(selector);};
 const project={id:'project-1',busy,image_updates:report};
 const window={reefHost:host,reefCapabilities:capabilities,addEventListener(type,fn){events[type]=fn;}};
 const context={window,document:{querySelector:find,createElement:node},current:()=>project,projects:[project],csrf:'fixture-csrf',ReefUI:{imageUpdateSummary},URLSearchParams,request:async(url,options)=>{requests.push({url,options});await pending;if(fail)throw Error('Registry request rejected');},message:text=>find('#message').textContent=text};
 vm.runInNewContext(source,context);
 return {project,nodes,requests,window,events,resolve,check:()=>window.ReefImageUpdates.check(project.id)};
}
test('summary distinguishes partial errors, available updates and no comparison baseline',()=>{
 const report={checked_at:'2026-10-27T12:00:00Z',services:[{status:'available'},{status:'unknown'}]};
 assert.equal(imageUpdateSummary(report),'1 service has image updates · 1 unable to check');
 assert.equal(imageUpdateSummary({...report,checking:true}),'Checking image updates…');
 assert.equal(imageUpdateSummary({...report,services:[{status:'current'}]}),'Images match the configured tags');
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

test('upgraded remote hosts can run manual image checks',async()=>{
 const p=page({host:'remote',capabilities:['control-v1']});const pending=p.check();assert.equal(p.requests.length,1);p.resolve();await pending;assert.equal(p.project.image_updates.checking,true);
});

test('viewer can read update status but cannot start a manual registry check',async()=>{const p=page();p.window.reefAccess={canWrite:false};p.window.ReefImageUpdates.render(p.project);assert.equal(p.nodes.get('#check-image-updates').disabled,true);await p.check();assert.equal(p.requests.length,0)});

test('a newer patch counts as an update even when the configured tag matches, and tag lookup failures remain visible',()=>{
 const report={checked_at:'2026-10-30T00:00:00Z',services:[{status:'current',version_status:'available'}]};assert.equal(imageUpdateSummary(report),'1 service has image updates');assert.equal(imageUpdateSummary({...report,services:[{status:'current',version_status:'unknown'}]}),'1 image unable to check');
 const p=page({report:{...report,services:[{service:'web',image:'old',status:'current',version_status:'available',candidate:'new',version_message:'Review this patch'}]}});const children=p.nodes.get('#image-update-services').children[0].children;assert.equal(children[1].textContent,'Up to date for this tag');assert.ok(children.some(n=>n.textContent==='Suggested tag: new'));assert.ok(children.some(n=>n.textContent==='Review tag change'));
});
test('same-tag updates expose one project action and keep new-tag diagnostics in expandable details',async()=>{
 const p=page({report:{services:[{service:'mariadb',image:'mariadb:latest',status:'available',version_message:'Only configured tag compared'},{service:'web',image:'dolibarr/dolibarr:22.0.5',status:'available',version_message:'No newer patch found'}]}});
 let applied=0;p.window.ReefCanApplyImageUpdate=()=>true;p.window.ReefApplyImageUpdate=()=>applied++;p.window.ReefImageUpdates.render(p.project);
 assert.equal(p.nodes.get('#image-update-actions').hidden,false);assert.equal(p.nodes.get('#apply-image-update').disabled,false);
 for(const row of p.nodes.get('#image-update-services').children){assert.equal(row.children[1].textContent,'New image for current tag');const details=row.children.find(n=>n.tagName==='details');assert.equal(details.open,false);assert.equal(details.children[0].textContent,'Check details');assert.equal(row.children.some(n=>n.textContent==='Review tag change'),false);}
 await p.nodes.get('#apply-image-update').listeners.click();assert.equal(applied,1);
 const details=p.nodes.get('#image-update-services').children[0].children.find(n=>n.tagName==='details');details.open=true;details.listeners.toggle();p.window.ReefImageUpdates.render(p.project);assert.equal(p.nodes.get('#image-update-services').children[0].children.find(n=>n.tagName==='details').open,true);
});
test('project shortcut is unavailable for drafts, pending checks, viewers and unsupported agents',async()=>{
 for(const setup of [p=>{p.window.ReefCanApplyImageUpdate=()=>false},p=>{p.project.busy=true},p=>{p.project.image_updates.checking=true},p=>{p.project.image_updates.error='Unknown result'},p=>{p.window.reefAccess={canWrite:false}},p=>{p.window.reefHost='old-agent'}]){
  const p=page({report:{services:[{status:'available'}]}});p.window.ReefCanApplyImageUpdate=()=>true;let calls=0;p.window.ReefApplyImageUpdate=()=>calls++;setup(p);p.window.ReefImageUpdates.render(p.project);assert.equal(p.nodes.get('#apply-image-update').disabled,true);await p.nodes.get('#apply-image-update').listeners.click();assert.equal(calls,0);
 }
 const tagsOnly=page({report:{services:[{status:'current',version_status:'available',candidate:'example:1.2.4'}]}});assert.equal(tagsOnly.nodes.get('#image-update-actions').hidden,true);const row=tagsOnly.nodes.get('#image-update-services').children[0];assert.ok(row.children.some(n=>n.textContent==='New version available'));assert.ok(row.children.some(n=>n.textContent==='Review tag change'));
});
