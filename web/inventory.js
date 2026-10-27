'use strict';
(()=>{
 const find=selector=>document.querySelector(selector);
 let snapshot=[],managed=window.reefProjects||[],hasSnapshot=false,busy=false,managing=false,currentView='containers',access=null;
 const dialog=find('#manage-project'),form=find('#manage-form');
 const saved=ReefNavigation.read();let restoring=true,restoringProject=false,hostsReady=false,managedReady=Array.isArray(window.reefProjects),restoreInFlight=false,scrollTimer;
 if('scrollRestoration' in history)history.scrollRestoration='manual';
 function remember(){
  if(restoring)return;
  const filters={};for(const id of ReefNavigation.filterIds)filters[id]=find('#'+id).value;
  ReefNavigation.write({view:currentView,host,project:currentView==='projects'&&document.body.dataset.detail==='true'?current()?.id||'':'',filters,scroll:window.scrollY});
 }
 function userNavigation(){restoring=false;}
 async function restore(){
  if(!restoring||restoreInFlight||!hostsReady||!hasSnapshot||(host==='local'&&!managedReady))return;
  restoreInFlight=true;
  try{
   setView(saved.view);
   if(saved.view==='projects'&&saved.host===host&&host==='local'&&managed.some(p=>p.id===saved.project)){
    restoringProject=true;await selectProject(saved.project);restoringProject=false;
    if(current()?.id!==saved.project){restoring=false;remember();return;}
   }
   if(!restoring)return;
   if(saved.host===host){
    for(const id of ReefNavigation.filterIds){const field=find('#'+id),value=saved.filters[id];if(field.tagName!=='SELECT'||[...field.options].some(o=>o.value===value))field.value=value;}
   }
   renderContainers();renderProjects();
   requestAnimationFrame(()=>{if(!restoring)return;window.scrollTo(0,saved.host===host?saved.scroll:0);restoring=false;remember();});
  }finally{restoringProject=false;restoreInFlight=false;}
 }
 window.addEventListener('pagehide',remember);
 window.addEventListener('scroll',()=>{clearTimeout(scrollTimer);scrollTimer=setTimeout(remember,150);},{passive:true});
 let host=window.reefHost||'local',hostName=window.reefHostName||'Local',hostEpoch=0;const visibleManaged=()=>host==='local'?managed:[];
 function updateProjectConsole(){const p=current();find('#project-console-open').disabled=!p||!hasSnapshot||!snapshot.some(c=>c.compose_project===p.project_name&&c.state==='running');}
 function setView(view,detail=false){currentView=view;find('#containers-view').hidden=view!=='containers';find('#projects-view').hidden=view!=='projects';find('#project-overview').hidden=detail;find('#project-detail').hidden=!detail;find('#managed-navigation').hidden=view!=='projects'||!visibleManaged().length;document.body.dataset.detail=String(detail);document.querySelectorAll('[data-view]').forEach(button=>{if(button.dataset.view===view)button.setAttribute('aria-current','page');else button.removeAttribute('aria-current');});}
 document.querySelectorAll('[data-view]').forEach(button=>button.addEventListener('click',()=>{userNavigation();setView(button.dataset.view);remember();}));
 find('#back-projects').addEventListener('click',()=>{userNavigation();setView('projects');find('#projects-title').focus({preventScroll:true});window.scrollTo(0,0);remember();});
 window.addEventListener('reef-open-project',()=>{if(!restoringProject)userNavigation();setView('projects',true);updateProjectConsole();renderProjectContainers();find('#project-name').focus({preventScroll:true});window.scrollTo(0,0);remember();});
 window.addEventListener('reef-project-overview',()=>{userNavigation();setView('projects');remember();});
 window.addEventListener('reef-state',event=>{managed=event.detail;managedReady=true;find('#managed-navigation').hidden=currentView!=='projects'||!visibleManaged().length;renderProjects();renderContainers();restore();});
 function rootDescription(policy){if(!policy)return 'Loading allowed folders…';if(policy.source==='explicit')return 'Allowed folders are explicitly configured.';if(policy.source==='installation-parent')return `Default: parent of Reef installation (${policy.installation_directory}).`;return 'Installation folder unknown. Set installation_directory or REEF_INSTALLATION_DIR, or configure allowed_project_roots.';}
 function options(selector,values,first){const select=find(selector),signature=JSON.stringify(values);if(select.dataset.options===signature)return;const value=select.value;select.replaceChildren(new Option(first,''),...values.map(([value,label])=>new Option(label,value)));select.value=[...select.options].some(o=>o.value===value)?value:'';select.dataset.options=signature;}
 function renderProjects(){
  find('#project-access-details').hidden=host!=='local';find('#no-managed-projects').hidden=host!=='local'||managed.length>0;
  find('#projects-description').textContent=host==='local'?'Connect a project to edit its Compose files and manage updates.':'Remote projects are available for inventory only.';
  find('#host-note').textContent=host==='local'?'Visible containers stay read-only until you connect their project.':'Remote inventory only. Logs, consoles and project management are not available yet.';
  updateProjectConsole();renderProjectContainers();
  const all=ReefUI.projectRows(snapshot,visibleManaged()),query=find('#project-search').value.trim().toLowerCase(),mode=find('#project-access').value;
  const rows=all.filter(g=>(!query||`${g.name} ${g.managed?.name||''}`.toLowerCase().includes(query))&&(!mode||(mode==='managed'?!!g.managed:!g.managed)));
  find('#compose-nav-count').textContent=hasSnapshot?all.length:'—';find('#project-results').textContent=`${rows.length} of ${all.length} projects · ${visibleManaged().length} managed`;
  const signature=JSON.stringify(rows.map(g=>[g.name,g.self,g.managed?.id,g.managed?.name,g.managed?.busy,g.managed?.image_updates,g.containers.map(c=>[c.state,c.managed_project_id])]))+query+mode+hostEpoch+hasSnapshot;
  if(find('#discovered-projects').dataset.signature===signature)return;
  const nodes=rows.map(group=>{
   const row=document.createElement('article');row.className='project-row';
   const title=document.createElement('div');title.className='project-row-name';
   if(group.managed){const open=textNode('button',group.managed.name,'text-button');open.type='button';open.dataset.focusKey='open-'+group.managed.id;open.addEventListener('click',()=>selectProject(group.managed.id));title.append(open);if(group.managed.name!==group.name)title.append(textNode('small',group.name,'muted'));}
   else title.append(textNode('strong',group.name));
   if(group.managed)title.append(textNode('small',ReefUI.imageUpdateSummary(group.managed.image_updates),'muted'));
   const running=group.containers.filter(c=>c.state==='running').length;
   const count=textNode('span',group.containers.length?`${running} running · ${group.containers.length} total`:'No containers','muted project-container-count');
   const badge=textNode('span',group.self?'Reef itself':group.managed?'Managed':'View only','tag'+(group.managed?' managed-tag':group.self?' neutral-tag':' neutral-tag'));
   const action=document.createElement('div');action.className='project-row-action';
   if(group.managed){const button=textNode('button','Open project','project-primary-action');button.type='button';button.setAttribute('aria-label',`Open ${group.managed.name}`);button.dataset.focusKey='open-action-'+group.managed.id;button.addEventListener('click',()=>selectProject(group.managed.id));action.append(button);}
   else if(!group.self&&host==='local'){const button=textNode('button','Manage this project','project-primary-action');button.type='button';button.setAttribute('aria-label',`Manage ${group.name}`);button.dataset.focusKey='manage-'+group.name;button.disabled=!hasSnapshot;button.addEventListener('click',()=>openManage(group.name));action.append(button);}
   if(group.containers.length&&host==='local'){const logs=textNode('button','Logs','project-logs-action');logs.type='button';logs.dataset.focusKey='project-logs-'+group.name;logs.setAttribute('aria-label',`Application logs for ${group.name}`);logs.addEventListener('click',()=>window.openContainerLogs({containers:snapshot.filter(c=>c.compose_project===group.name),project:group.name}));action.append(logs);}
   const consoleButton=textNode('button','Console','project-console-action');consoleButton.type='button';consoleButton.dataset.focusKey='project-console-'+group.name;consoleButton.setAttribute('aria-label',`Console for project ${group.name}`);consoleButton.disabled=host!=='local'||!hasSnapshot||!running;if(consoleButton.disabled)consoleButton.title=host!=='local'?'Remote consoles are not available yet':'Console access requires a running container';consoleButton.addEventListener('click',()=>window.openProjectConsole({containers:snapshot.filter(c=>c.compose_project===group.name),project:group.managed?.name||group.name}));action.append(consoleButton);
   row.append(title,count,badge,action);return row;
  });
  if(!rows.length)nodes.push(textNode('div',!hasSnapshot?'Waiting for Docker inventory…':all.length?'No projects match your search. Clear the search or change Access.':'No Compose projects found. Projects appear here when their containers have Compose labels.','empty-state'));
  replaceKeepingFocus(find('#discovered-projects'),nodes);find('#discovered-projects').dataset.signature=signature;
 }
 function renderProjectContainers(){
  const p=current(),rows=p?snapshot.filter(c=>c.compose_project===p.project_name):[],table=find('#project-container-rows');
  const signature=JSON.stringify([p?.id,hasSnapshot,rows]);if(table.dataset.signature===signature)return;
  const nodes=rows.map(c=>{const row=document.createElement('tr'),name=document.createElement('td'),actions=document.createElement('td');name.append(ReefContainers.name(c));actions.append(ReefContainers.actions(c));row.append(name,textNode('td',c.compose_service||'—'),textNode('td',c.image,'image-name'),ReefContainers.state(c),actions);return row;});
  replaceKeepingFocus(table,nodes);table.dataset.signature=signature;
  find('#project-containers-empty').hidden=rows.length>0;find('#project-containers-empty').textContent=hasSnapshot?'No containers currently exist for this project.':'Waiting for Docker inventory…';
 }
 function renderContainers(){
  ReefContainers.update(snapshot);
  options('#container-state',[...new Set(snapshot.map(c=>c.state))].sort().map(s=>[s,s]),'All states');
  const groups=[...new Set(snapshot.map(c=>c.compose_project).filter(Boolean))].sort().map(s=>[s,s]);if(snapshot.some(c=>!c.compose_project))groups.push(['__none__','No Compose project']);options('#container-project',groups,'All projects');
  const query={search:find('#container-search').value,state:find('#container-state').value,project:find('#container-project').value,access:find('#container-access').value};const rows=ReefUI.filterContainers(snapshot,query);
  find('#container-nav-count').textContent=hasSnapshot?snapshot.length:'—';find('#total-count').textContent=hasSnapshot?snapshot.length:'—';find('#running-count').textContent=hasSnapshot?snapshot.filter(c=>c.state==='running').length:'—';find('#other-count').textContent=hasSnapshot?snapshot.filter(c=>c.state!=='running').length:'—';find('#container-results').textContent=hasSnapshot?`${rows.length} of ${snapshot.length} containers`:'';find('#clear-container-filters').hidden=!Object.values(query).some(Boolean);
  const signature=JSON.stringify(rows);if(find('#container-rows').dataset.signature!==signature){
   const nodes=rows.map(c=>{
    const row=document.createElement('tr'),name=document.createElement('td');name.append(ReefContainers.name(c),textNode('small',c.id.slice(0,12),'muted'));name.title=c.id;
    const image=textNode('td',c.image,'image-name');image.title=c.image;
    const project=textNode('td',c.compose_project||'—','container-project-name'),permission=document.createElement('td');
    if(c.managed_project_id){const button=textNode('button','Open project','compact-button');button.type='button';button.dataset.focusKey='container-open-'+c.id;button.setAttribute('aria-label',`Open project for ${c.name}`);button.addEventListener('click',()=>selectProject(c.managed_project_id));permission.append(textNode('span','Managed','managed-label'),button);}else permission.append(textNode('span',c.is_reef?'Reef itself':'View only','muted'));
    const actions=document.createElement('td');actions.append(ReefContainers.actions(c));row.append(name,image,ReefContainers.state(c),project,permission,actions);return row;
   });
   replaceKeepingFocus(find('#container-rows'),nodes);find('#container-rows').dataset.signature=signature;
  }
  const empty=find('#inventory-empty');empty.hidden=rows.length>0;empty.textContent=!hasSnapshot?'Waiting for Docker inventory…':snapshot.length?'No containers match these filters. Clear filters to see all containers.':'No containers on this engine.';
 }
 async function refresh(){
  const requestedHost=hostEpoch;
  if(busy)return;busy=true;find('#refresh-containers').disabled=true;find('#refresh-projects').disabled=true;
  try{const result=await(await request('/api/containers')).json();if(requestedHost!==hostEpoch)return;snapshot=result.containers.map(c=>host==='local'?c:{...c,host_id:host,host_name:hostName,inventory_only:true});hasSnapshot=true;access=result.project_access;find('#inventory-error').hidden=true;find('#connection-status').textContent=host==='local'?'Docker connected':`${hostName} · ${result.host.status}${result.host.stale?' · stale':''}`;find('#connection-status').dataset.connected=String(!result.host?.stale);if(result.host?.stale){find('#inventory-error').textContent=`${hostName}: ${result.host.error||result.host.status}. Showing the last inventory; it may be outdated.`;find('#inventory-error').hidden=false;}find('#updated-at').textContent=`Updated ${new Date(result.checked_at).toLocaleTimeString('en-GB')}`;find('#project-root-summary').textContent=rootDescription(access)+(access.roots.length?'\n'+access.roots.join('\n'):'');}
  catch(error){if(requestedHost!==hostEpoch)return;find('#inventory-error').textContent=`Docker inventory unavailable. ${error.message}${hasSnapshot?' Showing the last successful snapshot.':''}`;find('#inventory-error').hidden=false;find('#connection-status').textContent='Docker unavailable';find('#connection-status').dataset.connected='false';}
  finally{if(requestedHost!==hostEpoch)return;busy=false;find('#refresh-containers').disabled=false;find('#refresh-projects').disabled=false;renderContainers();renderProjects();restore();}
 }
 for(const id of ['#container-search','#container-state','#container-project','#container-access'])find(id).addEventListener(id==='#container-search'?'input':'change',()=>{userNavigation();renderContainers();remember();});
 find('#clear-container-filters').addEventListener('click',()=>{userNavigation();for(const id of ['#container-search','#container-state','#container-project','#container-access'])find(id).value='';renderContainers();find('#container-search').focus();remember();});
 window.addEventListener('reef-projects-changed',refresh);
 find('#project-console-open').addEventListener('click',()=>{const p=current();if(p)window.openProjectConsole({containers:snapshot.filter(c=>c.compose_project===p.project_name),project:p.name});});
 find('#project-container-logs').addEventListener('click',()=>{const p=current();if(p)window.openContainerLogs({containers:snapshot.filter(c=>c.compose_project===p.project_name),project:p.name});});
 for(const [id,event] of [['#project-search','input'],['#project-access','change']])find(id).addEventListener(event,()=>{userNavigation();renderProjects();remember();});find('#refresh-containers').addEventListener('click',refresh);find('#refresh-projects').addEventListener('click',refresh);
 function setupStep(review){find('#manage-confirm').disabled=!review;find('#manage-confirm').required=review;find('#manage-files-step').hidden=review;find('#manage-review-step').hidden=!review;find('#manage-back').hidden=!review;find('#manage-next').hidden=review;find('#manage-submit').hidden=!review;find('#manage-step-label').textContent=review?'2 of 2 · Review connection':'1 of 2 · Project files';if(!review)find('#manage-confirm').checked=false;dialog.scrollTop=0;}
 async function openManage(name){
  if(managing||dialog.open)return;managing=true;
  try{const result=await(await request('/api/projects/proposal?name='+encodeURIComponent(name))).json();form.reset();form.querySelectorAll('[aria-invalid]').forEach(n=>n.removeAttribute('aria-invalid'));form.querySelectorAll('.field-error').forEach(n=>n.hidden=true);find('#manage-error').hidden=true;find('#manage-identity').value=result.project_name;find('#manage-project-name').textContent=result.project_name;find('#manage-name').value=result.project_name;find('#manage-directory').value=result.directory;find('#manage-files').value=result.compose_files.join('\n');find('#manage-notice').textContent=result.directory&&result.compose_files.length?'Detected from Docker labels. Check the original folder and file order.':'Some details are missing from Docker labels. Enter the original folder and Compose files.';find('#manage-root-source').textContent=rootDescription(result.project_access);find('#manage-roots').textContent=result.allowed_roots.length?result.allowed_roots.join('\n'):'No allowed folder is configured. Set the installation directory or explicit roots, with the matching mount.';find('#manage-access-details').open=!result.allowed_roots.length;find('#manage-env-options').open=false;find('#manage-next').disabled=!result.allowed_roots.length;setupStep(false);dialog.showModal();}
  catch(error){find('#inventory-error').textContent=error.message;find('#inventory-error').hidden=false;}finally{managing=false;}
 }
 find('#manage-next').addEventListener('click',()=>{
  for(const id of ['#manage-name','#manage-directory','#manage-files'])if(!find(id).reportValidity())return;
  const summary=find('#manage-review');summary.replaceChildren();for(const [label,value] of [['Project',find('#manage-identity').value],['Display name',find('#manage-name').value],['Folder',find('#manage-directory').value],['Compose files',find('#manage-files').value],['Environment',find('#manage-env').value.trim()||'Default .env behavior']])summary.append(textNode('dt',label),textNode('dd',value));setupStep(true);find('#manage-confirm').focus();
 });
 find('#manage-back').addEventListener('click',()=>setupStep(false));find('#manage-cancel').addEventListener('click',()=>{if(!managing)dialog.close();});dialog.addEventListener('cancel',event=>{if(managing)event.preventDefault();});
 form.addEventListener('input',event=>{if(event.target.id!=='manage-confirm')find('#manage-confirm').checked=false;event.target.removeAttribute('aria-invalid');const errorNode=find('#'+event.target.id+'-error');if(errorNode)errorNode.hidden=true;});
 form.addEventListener('submit',async event=>{
  event.preventDefault();if(managing)return;if(find('#manage-review-step').hidden){find('#manage-next').click();return;}if(!form.reportValidity())return;managing=true;find('#manage-submit').disabled=true;find('#manage-submit').textContent='Connecting…';find('#manage-cancel').disabled=true;find('#manage-back').disabled=true;find('#manage-error').hidden=true;
  try{const body=new URLSearchParams({csrf,confirmed:'yes',project_name:find('#manage-identity').value,name:find('#manage-name').value,directory:find('#manage-directory').value,compose_files:find('#manage-files').value,env_files:find('#manage-env').value});const project=await(await request('/api/projects/register',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body})).json();dialog.close();window.dispatchEvent(new CustomEvent('reef-projects-changed',{detail:{id:project.id}}));await refresh();}
  catch(error){const fields={directory:'manage-directory',compose_files:'manage-files',env_files:'manage-env',name:'manage-name'};const id=fields[error.field];if(id){setupStep(false);if(id==='manage-env')find('#manage-env-options').open=true;if(id==='manage-directory')find('#manage-access-details').open=true;find('#'+id).setAttribute('aria-invalid','true');find('#'+id+'-error').textContent=error.message;find('#'+id+'-error').hidden=false;find('#'+id).focus();}else{find('#manage-error').textContent=error.message;find('#manage-error').hidden=false;}}
  finally{managing=false;find('#manage-submit').disabled=false;find('#manage-submit').textContent='Confirm and manage';find('#manage-cancel').disabled=false;find('#manage-back').disabled=false;}
 });
 window.addEventListener('reef-host-changed',event=>{
  if(!event.detail.restoring)userNavigation();hostEpoch++;host=event.detail.id;controls();hostName=event.detail.name;busy=false;snapshot=[];hasSnapshot=false;access=null;
  for(const id of ['#container-details','#project-console','#container-logs','#manage-project']){const d=find(id);if(d?.open)d.close();}
  find('#connection-status').textContent=`${hostName} · connecting…`;find('#updated-at').textContent='';find('#inventory-error').hidden=true;
  for(const id of ['#container-search','#container-state','#container-project','#container-access','#project-search','#project-access'])find(id).value='';
  setView(event.detail.restoring?saved.view:'containers');renderContainers();renderProjects();remember();if(hostsReady)refresh();
 });
 setView(saved.view);
 window.reefHostsReady.then(()=>{hostsReady=true;refresh();setInterval(refresh,5000);});
})();
