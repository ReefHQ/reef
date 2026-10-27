'use strict';
const $=selector=>document.querySelector(selector);
const csrf=$('meta[name="csrf-token"]').content;
let projects=[],selected='',revision='',dirty=false,loading=false,loaded=false,inFlight=false,lastOp='',openedLog='';
let pendingEdits=new Map();
let editorHost='local',editorEpoch=0,pollTimer=null,pollController=null;
const hostEditors=new Map();
function canManageHost(){return !window.reefHost||window.reefHost==='local'||window.reefCapabilities?.includes('control-v1');}
function saveHostEditor(){hostEditors.set(editorHost,{projects,selected,revision,dirty,loading:false,loaded,inFlight:false,lastOp,openedLog,pendingEdits,content:$('#editor').value});}
window.addEventListener('reef-host-changed',event=>{
 saveHostEditor();editorEpoch++;editorHost=event.detail.id;pollController?.abort();clearTimeout(pollTimer);
 const cached=hostEditors.get(editorHost)||{};projects=cached.projects||[];selected=cached.selected||'';revision=cached.revision||'';dirty=!!cached.dirty;loading=false;loaded=!!cached.loaded;inFlight=false;lastOp=cached.lastOp||'';openedLog=cached.openedLog||'';pendingEdits=cached.pendingEdits||new Map();$('#editor').value=cached.content||'';
 window.reefProjects=projects;message('');render();poll();
});
function message(text,error=false){$('#message').textContent=text;$('#message').className=text?'notice'+(error?' error':''):'';$('#message').setAttribute('role',error?'alert':'status');}
async function request(path,opts,host=window.reefHost||'local'){
 if(host!=='local'&&path.startsWith('/api/')&&!path.startsWith('/api/hosts')&&!/[?&]host=/.test(path))path+=(path.includes('?')?'&':'?')+'host='+encodeURIComponent(host);
 const response=await fetch(path,opts);if(response.status===401){location.href='/login';throw Error('Sign-in required');}if(!response.ok){const error=Error(await response.text());error.field=response.headers.get('X-Reef-Field');throw error;}return response;
}
function ask(text){const dialog=$('#confirmation');if(dialog.open)return Promise.resolve(false);$('#confirm-message').textContent=text;dialog.returnValue='cancel';return new Promise(resolve=>{dialog.addEventListener('close',()=>resolve(dialog.returnValue==='confirm'),{once:true});dialog.showModal();});}
function current(){return projects.find(p=>p.id===selected);}
function textNode(tag,value,className){const node=document.createElement(tag);node.textContent=value;if(className)node.className=className;return node;}
function replaceKeepingFocus(parent,nodes){const key=parent.contains(document.activeElement)?document.activeElement.dataset.focusKey:null;parent.replaceChildren(...nodes);if(key)[...parent.querySelectorAll('[data-focus-key]')].find(n=>n.dataset.focusKey===key)?.focus({preventScroll:true});}
function controls(){
 const p=current(),busy=!canManageHost()||!p||p.busy||pendingEdits.has(selected)||loading||inFlight||!loaded;
 $('#editor').disabled=busy;
 document.querySelectorAll('[data-action]').forEach(button=>button.disabled=busy||p?.image_updates?.checking||(dirty&&['start','stop'].includes(button.dataset.action)));
 $('#reload').disabled=!p||!!p.busy||p.image_updates?.checking||pendingEdits.has(selected)||loading||inFlight;
 $('#unmanage-current').disabled=!p||!!p.busy||p.image_updates?.checking||pendingEdits.has(selected)||loading||inFlight;
 $('#edit-state').textContent=p?.busy||inFlight||pendingEdits.has(selected)?'Operation in progress':loading?'Loading file…':!loaded?'File unavailable':dirty?'Unsaved changes':'Saved';
 $('#edit-state').className='save-status'+(dirty?' unsaved':'');
 $('#chars').textContent=`${$('#editor').value.split('\n').length} lines · ${$('#editor').value.length.toLocaleString('en-GB')} characters`;
}
async function loadDocument(){
 if(!selected)return;const epoch=editorEpoch,id=selected;loading=true;loaded=false;controls();
 try{const doc=await(await request('/api/document?id='+encodeURIComponent(id))).json();if(selected!==id||epoch!==editorEpoch)return;$('#editor').value=doc.content;revision=doc.revision;dirty=false;loaded=true;lastOp=current()?.operation.id||'';}
 catch(error){if(selected===id&&epoch===editorEpoch)message(error.message,true);}finally{if(epoch===editorEpoch){loading=false;controls();}}
}
async function selectProject(id){
 const epoch=editorEpoch;
 if(!canManageHost())return;
 if(id===selected&&loaded){window.dispatchEvent(new CustomEvent('reef-open-project'));return;}
 if(dirty&&!await ask('Discard unsaved changes and open another project?'))return;
 if(epoch!==editorEpoch)return;
 document.querySelector('.project-tools').open=false;$('#operation-log').open=false;
 selected=id;dirty=false;loaded=false;revision='';$('#editor').value='';lastOp='';openedLog='';message('');render();window.dispatchEvent(new CustomEvent('reef-open-project'));
 const pending=pendingEdits.get(id);if(pending){$('#editor').value=pending.content;revision=pending.revision;loaded=true;dirty=pending.dirty;controls();}else if(!current()?.busy)await loadDocument();
}
function render(){
 $('#project-count').textContent=projects.length;
 const picker=$('#managed-project-select'),pickerSignature=JSON.stringify(projects.map(p=>[p.id,p.name]));if(picker.dataset.signature!==pickerSignature){picker.replaceChildren(new Option('Choose a project',''),...projects.map(p=>new Option(p.name,p.id)));picker.dataset.signature=pickerSignature;}picker.value=selected;
 const buttons=projects.map(p=>{const button=document.createElement('button');button.type='button';button.dataset.focusKey='managed-'+p.id;button.className='project'+(p.id===selected?' active':'');button.append(textNode('strong',p.name),textNode('small',p.busy?'Operation in progress':p.operation.needs_start?(['failed','interrupted'].includes(p.operation.phase)?'Needs attention':'Start available'):p.operation.phase==='failed'?'Last action failed':'Managed'));button.addEventListener('click',()=>selectProject(p.id));return button;});
 const signature=JSON.stringify(projects.map(p=>[p.id,p.name,p.busy,p.operation.phase,p.operation.needs_start]))+selected;
 if($('#projects').dataset.signature!==signature){replaceKeepingFocus($('#projects'),buttons);$('#projects').dataset.signature=signature;}
 $('#no-managed-projects').hidden=projects.length>0;
 const p=current();if(!p){controls();return;}
 $('#project-name').textContent=p.name;$('#project-path').textContent=p.directory;$('#project-identity').textContent=`${p.project_name} · ${p.files.length} Compose file(s)`;$('#file-name').textContent=p.files[0];$('#file-context').textContent=p.files.length>1?`${p.files.length-1} additional file(s) preserved in order`:'Main Compose file';$('#unmanage-current').hidden=!p.registered_in_ui;
 const op=p.operation;const status=op.phase==='completed'?'Completed':op.phase==='failed'?'Failed':op.phase==='interrupted'?'Interrupted':op.running?'In progress':'None';
 $('#phase').textContent=status;$('#phase').className='tag'+(['Failed','Interrupted'].includes(status)?' failed':'');
 $('#operation-result').textContent=op.result||(op.running?'Running on the server. You can close this tab.':'No operations yet.');
 const steps=ReefUI.operationSteps(op);$('#steps').replaceChildren(...steps.map(step=>{const item=textNode('li',step.label,step.state);item.setAttribute('aria-label',`${step.label}: ${step.state}`);if(step.state==='active')item.setAttribute('aria-current','step');return item;}));
 $('#operation-time').textContent=op.id?`${op.action==='apply'?'Update':op.action==='save'?'Save':op.action==='start'?'Start':'Stop'} · ${new Date(op.started).toLocaleString('en-GB')}${op.running?' · continues on the server':''}`:'';
 $('#needs-start').hidden=!op.needs_start;$('#operation-next').hidden=!['failed','interrupted'].includes(op.phase)||op.needs_start;$('#operation-next').textContent='Review the error and command log, then retry. Reload the file first if it changed outside Reef.';
 const log=op.log||'Operation logs will appear here.';if($('#logs').textContent!==log){const pre=$('#logs'),follow=pre.scrollTop+pre.clientHeight>=pre.scrollHeight-30;pre.textContent=log;if(follow)pre.scrollTop=pre.scrollHeight;}
 if(['failed','interrupted'].includes(op.phase)&&openedLog!==op.id){$('#operation-log').open=true;openedLog=op.id;}else if(op.id&&openedLog!==op.id){$('#operation-log').open=false;openedLog=op.id;}
 if(p.persistence_error)message(p.persistence_error,true);controls();window.ReefImageUpdates?.render(p);
}
async function poll(){
 const epoch=editorEpoch,controller=new AbortController();pollController=controller;
 try{
  if(!canManageHost()){projects=[];window.reefProjects=projects;render();window.dispatchEvent(new CustomEvent('reef-state',{detail:projects}));return;}
  const state=await(await request('/api/state',{signal:controller.signal})).json();if(epoch!==editorEpoch)return;projects=state;window.reefProjects=projects;$('#app-notice').hidden=true;render();window.dispatchEvent(new CustomEvent('reef-state',{detail:projects}));
  const p=current();if(p&&!p.busy&&$('#message').textContent.startsWith('Operation started.'))message('');
  if(p&&!p.busy&&!inFlight&&pendingEdits.has(p.id)&&p.operation.id!==pendingEdits.get(p.id).previousOpID)await settleEdit(p);
  else if(p&&!p.busy&&!dirty&&!inFlight&&!loading&&!pendingEdits.has(p.id)&&(!loaded||lastOp!==p.operation.id))await loadDocument();
 }catch(error){if(epoch!==editorEpoch||error.name==='AbortError')return;$('#app-notice').textContent='Project status unavailable. '+error.message;$('#app-notice').hidden=false;}
 finally{if(epoch===editorEpoch)pollTimer=setTimeout(poll,1800);}
}
$('#managed-project-select').addEventListener('change',event=>{if(event.target.value)selectProject(event.target.value);});
document.addEventListener('click',event=>{if(!event.target.closest('.project-tools'))document.querySelector('.project-tools').open=false;});
$('#editor').addEventListener('input',()=>{dirty=true;controls();});
$('#reload').addEventListener('click',async()=>{const epoch=editorEpoch;if(!dirty||await ask('Reload the file and discard your unsaved changes?')){if(epoch!==editorEpoch)return;message('');await loadDocument();}});
async function runAction(action){
 const epoch=editorEpoch,host=editorHost,p=current();if(!p||p.busy||p.image_updates?.checking||pendingEdits.has(p.id)||inFlight||loading||!loaded)return;
 if(['start','stop'].includes(action)&&dirty)return;
 if(['apply','stop'].includes(action)&&!await ask(action==='apply'?`Apply update to ${p.name} on ${window.reefHostName||'Local'}? Reef will validate and save, pull images, then stop and recreate this project. Downtime is expected. Replaced images will be removed only if untagged, unused and the updated containers are running (and healthy when a healthcheck exists).`:`Stop ${p.name} on ${window.reefHostName||'Local'}? Reef will run Compose down without deleting named volumes.`))return;
 if(epoch!==editorEpoch)return;
 inFlight=true;controls();message('');
 const submitted={content:$('#editor').value,revision,dirty,previousOpID:p.operation.id};
 try{await request('/api/action',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams({csrf,id:p.id,action,revision,content:$('#editor').value})},host);if(epoch!==editorEpoch){const cached=hostEditors.get(host);if(cached){cached.loaded=false;cached.inFlight=false;if(['save','apply'].includes(action))cached.pendingEdits.set(p.id,submitted);}return;}if(['save','apply'].includes(action))pendingEdits.set(p.id,submitted);else loaded=false;p.busy=true;message('Operation started. It will continue if you close this tab.');document.querySelector('.project-tools').open=false;}
 catch(error){if(epoch===editorEpoch)message(error.message,true);}finally{if(epoch===editorEpoch){inFlight=false;controls();}}
}
document.querySelectorAll('[data-action]').forEach(button=>button.addEventListener('click',()=>runAction(button.dataset.action)));
$('#unmanage-current').addEventListener('click',async()=>{
 const epoch=editorEpoch,host=editorHost,p=current();if(!p||p.busy||!p.registered_in_ui||inFlight)return;
 if(!await ask(`Stop managing ${p.name} on ${window.reefHostName||'Local'}? Its containers and files stay in place; operation history is retained.${dirty?' Unsaved editor changes will be discarded.':''}`))return;
 if(epoch!==editorEpoch)return;
 inFlight=true;controls();
 try{await request('/api/projects/unmanage',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams({csrf,id:p.id,confirmed:'yes'})},host);if(epoch!==editorEpoch){const cached=hostEditors.get(host);if(cached){cached.selected='';cached.loaded=false;cached.dirty=false;}return;}selected='';dirty=false;loaded=false;window.dispatchEvent(new CustomEvent('reef-projects-changed',{detail:{removed:p.id}}));window.dispatchEvent(new CustomEvent('reef-project-overview'));}
 catch(error){if(epoch===editorEpoch)message(error.message,true);}finally{if(epoch===editorEpoch){inFlight=false;controls();}}
});
async function settleEdit(p){
 const epoch=editorEpoch,id=p.id,pending=pendingEdits.get(id);loading=true;controls();
 try{const disk=await(await request('/api/document?id='+encodeURIComponent(id))).json();if(selected!==id||epoch!==editorEpoch)return;pendingEdits.delete(id);lastOp=p.operation.id;loaded=true;
 if(disk.content===pending.content){$('#editor').value=disk.content;revision=disk.revision;dirty=false;message(p.operation.phase==='failed'?'Your file is saved. Review the failed operation below.':'');}
 else{$('#editor').value=pending.content;revision=pending.revision;dirty=true;message('Your edits are kept in the editor. Resolve the error, then save again. Reload first if the file changed elsewhere.',true);}
 }catch(error){if(selected===id&&epoch===editorEpoch){pendingEdits.delete(id);dirty=true;loaded=true;lastOp=p.operation.id;message('Your edits are kept here. '+error.message,true);}}finally{if(epoch===editorEpoch){loading=false;controls();}}
}
window.addEventListener('reef-projects-changed',async event=>{const epoch=editorEpoch;try{const state=await(await request('/api/state')).json();if(epoch!==editorEpoch)return;projects=state;window.reefProjects=projects;render();window.dispatchEvent(new CustomEvent('reef-state',{detail:projects}));if(event.detail.id)await selectProject(event.detail.id);}catch(error){if(epoch===editorEpoch)message(error.message,true);}});
window.addEventListener('keydown',event=>{if(!document.querySelector('dialog[open]')&&(event.metaKey||event.ctrlKey)&&event.key.toLowerCase()==='s'&&!$('#project-detail').hidden&&!$('#projects-view').hidden){event.preventDefault();if(!$('#save').disabled)runAction('save');}});
window.addEventListener('beforeunload',event=>{if(dirty||[...pendingEdits.values()].some(p=>p.dirty)||[...hostEditors.entries()].some(([host,state])=>host!==editorHost&&(state.dirty||[...state.pendingEdits.values()].some(p=>p.dirty)))){event.preventDefault();event.returnValue='';}});
poll();
