'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs');
const alias='/share/Container/apps/gitlab',physical='/share/VOLUME/Container/apps/gitlab';
function page({initial=physical,resolve=async()=>({resolved_directory:physical}),aliasSupport=false,suggestions=[],savedAliases=[],postAlias,confirmRemove=async()=>true}={}){
 const nodes=new Map(),events={},requests=[];
 class Element{
  constructor(tag='DIV'){this.tagName=tag;this.dataset={};this.children=[];this.listeners={};this.attributes={};this.value='';this.hidden=false;this.open=false;this.options=[];this.textContent='';}
  addEventListener(type,fn){(this.listeners[type]??=[]).push(fn);}
  async fire(type){await Promise.all((this.listeners[type]||[]).map(fn=>fn({target:this,preventDefault(){}})));}
  append(...items){this.children.push(...items);} replaceChildren(...items){this.children=items;}
  setAttribute(k,v){this.attributes[k]=v;} removeAttribute(k){delete this.attributes[k];}
  querySelectorAll(){return [];} reportValidity(){return true;} focus(){} contains(){return false;}
  showModal(){this.open=true;} close(){this.open=false;} reset(){} click(){return this.fire('click');}
 }
 const find=id=>{if(!nodes.has(id))nodes.set(id,new Element());return nodes.get(id);};
 const window={reefHost:'local',reefHostName:'Local',reefProjects:[],reefHostsReady:Promise.resolve(),scrollY:0,scrollTo(){},addEventListener(type,fn){(events[type]??=[]).push(fn);},dispatchEvent(event){for(const fn of events[event.type]||[])fn(event);}};
 const document={body:new Element(),querySelector:find,querySelectorAll:()=>[],createElement:tag=>new Element(tag)};
 const context={window,document,history:{},URLSearchParams,csrf:'csrf',Option:function(label,value){this.value=value;},CustomEvent:function(type,o){this.type=type;this.detail=o?.detail;},setInterval(){},setTimeout(){},clearTimeout(){},requestAnimationFrame:fn=>fn(),current:()=>null,controls(){},selectProject(){},ask:confirmRemove,ReefNavigation:{filterIds:[],read:()=>({view:'projects',filters:{},host:'local',scroll:0}),write(){}},textNode:(tag,text)=>Object.assign(new Element(tag),{textContent:text}),replaceKeepingFocus:(n,children)=>n.replaceChildren(...children),ReefContainers:{update(){},name:()=>new Element(),state:()=>new Element(),actions:()=>new Element()},request:async(url,opts,host)=>{
  requests.push({url,opts,host});const parsed=new URL(url,'http://reef.test');let data;
  if(parsed.pathname==='/api/projects/proposal')data=parsed.searchParams.has('directory')?await resolve(parsed.searchParams.get('directory')):{project_name:'gitlab',directory:alias,resolved_directory:initial,alias_management:aliasSupport,alias_suggestions:suggestions,path_aliases:savedAliases,compose_files:['compose.yaml'],allowed_roots:['/share/VOLUME/Container/apps'],project_access:{roots:['/share/VOLUME/Container/apps']}};
  else if(parsed.pathname==='/api/projects/path-aliases')data=postAlias?await postAlias(opts.body):{resolved_directory:physical,path_aliases:[{alias:'/share/Container/apps',target:'/share/VOLUME/Container/apps',editable:true}]};
  else if(parsed.pathname==='/api/projects/register')data={id:'registered'};
  else data={containers:[{id:'fixture',name:'gitlab',compose_project:'gitlab',state:'running'}],project_access:{roots:['/share/VOLUME/Container/apps']},checked_at:new Date().toISOString()};
  return {json:async()=>data};
 }};
 vm.createContext(context);vm.runInContext(fs.readFileSync('web/model.js','utf8'),context);context.ReefUI=window.ReefUI;vm.runInContext(fs.readFileSync('web/inventory.js','utf8'),context);
 const settle=async()=>{for(let i=0;i<30;i++)await Promise.resolve();};
 const descendants=n=>[n,...n.children.flatMap(descendants)];
 return {nodes,window,requests,settle,async open(){await settle();const button=descendants(find('#discovered-projects')).find(n=>n.dataset.focusKey==='manage-gitlab');assert.ok(button);await button.fire('click');},find};
}
test('registration prefills the resolved folder, displays the original alias and posts only the reviewed physical path',async()=>{
 const p=page();await p.open();assert.equal(p.find('#manage-directory').value,physical);
 await p.find('#manage-next').fire('click');assert.equal(p.find('#manage-review-step').hidden,false);
 const summary=p.find('#manage-review').children.map(n=>n.textContent);assert.ok(summary.includes(physical));assert.ok(summary.includes('Resolved from'));assert.ok(summary.includes(alias));
 p.find('#manage-confirm').checked=true;await p.find('#manage-form').fire('submit');
 const post=p.requests.find(r=>r.url==='/api/projects/register');assert.ok(post);assert.equal(post.opts.body.get('directory'),physical);
});
test('edited aliases are resolved before review and failures stay on the file step without registration',async()=>{
 let fail=false;const p=page({initial:'',resolve:async input=>{assert.equal(input,alias);if(fail)throw Error('Destination is not authorized');return {resolved_directory:physical};}});
 await p.open();await p.find('#manage-next').fire('click');assert.equal(p.find('#manage-directory').value,physical);
 await p.find('#manage-back').fire('click');p.find('#manage-directory').value=alias;fail=true;
 await p.find('#manage-next').fire('click');assert.equal(p.find('#manage-review-step').hidden,true);assert.equal(p.find('#manage-directory-error').textContent,'Destination is not authorized');assert.equal(p.find('#manage-confirm').checked,false);assert.ok(!p.requests.some(r=>r.url==='/api/projects/register'));
});
test('late folder checks cannot overwrite changed input, a closed dialog or another host',async()=>{
 for(const action of ['edit','close','host']){
  let release;const p=page({initial:'',resolve:()=>new Promise(r=>release=r)});await p.open();const pending=p.find('#manage-next').fire('click');await p.settle();
  if(action==='edit')p.find('#manage-directory').value='/edited';
  if(action==='close')p.find('#manage-project').close();
  if(action==='host'){p.window.reefHost='agent';p.window.dispatchEvent({type:'reef-host-changed',detail:{id:'agent',name:'Agent'}});}
  release({resolved_directory:physical});await pending;assert.notEqual(p.find('#manage-directory').value,physical,action);assert.equal(p.find('#manage-review-step').hidden,true,action);assert.ok(!p.requests.some(r=>r.url==='/api/projects/register'));
 }
});
test('older agents retain manual path review without claiming automatic resolution',async()=>{
 const p=page({initial:'',resolve:async()=>({directory:alias})});await p.open();p.find('#manage-directory').value=physical;await p.find('#manage-next').fire('click');assert.equal(p.find('#manage-directory').value,physical);assert.ok(!p.find('#manage-review').children.some(n=>n.textContent==='Resolved from'));
});

test('a unique alias hypothesis prefills fields but requires a separate confirmation before saving',async()=>{
 const source='/share/Container/apps',target='/share/VOLUME/Container/apps';
 const p=page({initial:'',aliasSupport:true,suggestions:[{alias:source,target,directory:physical}]});await p.open();
 assert.equal(p.find('#manage-access-details').open,true);assert.equal(p.find('#manage-alias-source').value,source);assert.equal(p.find('#manage-alias-target').value,target);assert.equal(p.find('#manage-alias-confirm').checked,false);
 await p.find('#manage-alias-save').fire('click');assert.ok(!p.requests.some(r=>r.url==='/api/projects/path-aliases'));
 p.find('#manage-alias-confirm').checked=true;await p.find('#manage-alias-save').fire('click');
 const saved=p.requests.find(r=>r.url==='/api/projects/path-aliases');assert.equal(saved.opts.body.get('confirmed'),'yes');assert.equal(saved.opts.body.get('csrf'),'csrf');assert.equal(saved.opts.body.get('alias'),source);assert.equal(saved.opts.body.get('target'),target);assert.equal(saved.opts.body.get('directory'),alias);assert.equal(saved.host,'local');
 assert.equal(p.find('#manage-directory').value,physical);assert.equal(p.find('#manage-alias-confirm').checked,false);assert.equal(p.find('#manage-confirm').checked,false);assert.ok(!p.requests.some(r=>r.url==='/api/projects/register'));
});
test('multiple candidates stay unselected and editing an alias revokes its confirmation',async()=>{
 const p=page({initial:'',aliasSupport:true,suggestions:[{alias:'/one',target:'/first'},{alias:'/two',target:'/second'}]});await p.open();
 assert.equal(p.find('#manage-alias-editor').hidden,true);assert.match(p.find('#manage-alias-status').textContent,/Several/);
 await p.find('#manage-alias-add').fire('click');p.find('#manage-alias-confirm').checked=true;p.find('#manage-alias-source').value='/edited';await p.find('#manage-alias-source').fire('input');assert.equal(p.find('#manage-alias-confirm').checked,false);
});
test('saved alias removal is explicit and late alias saves cannot repaint a different host',async()=>{
 const row={alias:'/share/Container/apps',target:'/share/VOLUME/Container/apps',editable:true};
 const cancelled=page({aliasSupport:true,savedAliases:[row],confirmRemove:async()=>false});await cancelled.open();
 await cancelled.find('#manage-alias-list').children[0].children.find(n=>n.textContent==='Remove').fire('click');assert.ok(!cancelled.requests.some(r=>r.url==='/api/projects/path-aliases'));
 let release;const p=page({initial:'',aliasSupport:true,suggestions:[row],postAlias:()=>new Promise(r=>release=r)});await p.open();p.find('#manage-alias-confirm').checked=true;const pending=p.find('#manage-alias-save').fire('click');await p.settle();
 p.window.reefHost='remote';p.window.dispatchEvent({type:'reef-host-changed',detail:{id:'remote',name:'Remote'}});release({resolved_directory:physical,path_aliases:[row]});await pending;
 assert.notEqual(p.find('#manage-directory').value,physical);assert.equal(p.requests.find(r=>r.url==='/api/projects/path-aliases').host,'local');assert.equal(p.find('#manage-project').open,false);
});
test('older agents do not offer persistent alias controls',async()=>{const p=page();await p.open();assert.equal(p.find('#manage-alias-settings').hidden,true);});
