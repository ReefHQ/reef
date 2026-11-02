'use strict';
(()=>{
 const find=s=>document.querySelector(s),button=find('#check-image-updates');
 const labels={current:'Up to date for this tag',available:'New image for current tag',unknown:'Unable to check',pinned:'Digest pinned',local:'Local build',not_deployed:'Not deployed'};
 const checking=new Set(),openDetails=new Set();
 const key=id=>(window.reefHost||'local')+':'+id;
 function render(p){
  const report=p?.image_updates,remote=window.reefHost&&window.reefHost!=='local'&&!window.reefCapabilities?.includes('control-v1');
  button.disabled=window.reefAccess?.canWrite===false||!p||remote||p.busy||report?.checking||checking.has(key(p.id));
  const hasImageUpdate=!!report?.services?.some(s=>s.status==='available'),apply=find('#apply-image-update');
  find('#image-update-actions').hidden=!hasImageUpdate||window.reefAccess?.canWrite===false;
  apply.disabled=window.reefAccess?.canWrite===false||remote||!p||p.busy||report?.checking||!!report?.error||checking.has(key(p?.id))||window.ReefCanApplyImageUpdate?.()!==true;
  find('#image-update-action-help').textContent=apply.disabled?'Updates the whole project. Save or discard editor changes and wait for the file and current operations before updating.':'Updates the whole project using its saved Compose file. Services will be interrupted.';
  find('#image-updates-summary').textContent=ReefUI.imageUpdateSummary(report);
  find('#image-updates-checked').textContent=report?.checked_at&&!report.checked_at.startsWith('0001-')?`Last checked ${new Date(report.checked_at).toLocaleString('en-GB')} · automatic checks every 6 hours`: 'Automatic checks every 6 hours. Checks use the saved Compose configuration.';
  const nodes=(report?.services||[]).map(service=>{
   const item=document.createElement('li'),name=document.createElement('strong'),state=document.createElement('span'),image=document.createElement('span');
   name.textContent=service.service;state.textContent=labels[service.status]||labels.unknown;state.className='tag '+(service.status==='available'?'update-available':'neutral-tag');image.textContent=service.image;image.className='muted image-update-reference';item.append(name,state,image);

   if(service.candidate&&service.version_status==='available'){
    const versionState=document.createElement('span');versionState.textContent='New version available';versionState.className='tag update-available';item.append(versionState);
    const candidate=document.createElement('span');candidate.textContent='Suggested tag: '+service.candidate;candidate.className='image-update-reference';item.append(candidate);
    const prepare=document.createElement('button');prepare.type='button';prepare.textContent='Review tag change';prepare.disabled=window.reefAccess?.canWrite===false||p.busy||report.checking||window.ReefCanPrepareImageVersion?.()===false;prepare.addEventListener('click',()=>window.ReefPrepareImageVersion?.(service.service));item.append(prepare);
   }
   if(service.image&&!['local','pinned'].includes(service.status)){const browse=document.createElement('button');browse.type='button';browse.textContent='Browse tags';browse.disabled=p.busy||report.checking||!window.ReefImageTagContext?.()||remote||!!(window.reefHost&&window.reefHost!=='local'&&!window.reefCapabilities?.includes('image-tags-v1'));if(browse.disabled&&window.reefHost&&window.reefHost!=='local'&&!window.reefCapabilities?.includes('image-tags-v1'))browse.title='Upgrade the agent to 0.26.0 or later to browse tags';const implicit=service.image.lastIndexOf(':')<=service.image.lastIndexOf('/'),base=implicit?service.image:service.image.slice(0,service.image.lastIndexOf(':')),head=base.split('/')[0],extra=implicit||base.includes('/')&&(head.includes('.')||head.includes(':')||head==='localhost')&&!['docker.io','index.docker.io'].includes(head);if(extra&&window.reefHost&&window.reefHost!=='local'&&!window.reefCapabilities?.includes('image-tag-registries-v1')){browse.disabled=true;browse.title='Upgrade the agent to 0.27.0 or later for registry and implicit latest tags';}browse.addEventListener('click',()=>window.ReefImageTags?.open(p,service));item.append(browse);}
   if(service.message||service.version_message){const details=document.createElement('details'),summary=document.createElement('summary');summary.textContent='Check details';const detailsKey=key(p.id)+':'+service.service;details.open=openDetails.has(detailsKey);details.addEventListener('toggle',()=>{if(details.open){if(openDetails.size>=256)openDetails.delete(openDetails.values().next().value);openDetails.add(detailsKey);}else openDetails.delete(detailsKey);});details.append(summary);for(const text of [service.message,service.version_message].filter(Boolean)){const note=document.createElement('small');note.textContent=text;note.className='muted';details.append(note);}item.append(details);}
   return item;
  });find('#image-update-services').replaceChildren(...nodes);
 }
 async function check(id){
  if(window.reefAccess?.canWrite===false||checking.has(key(id))||window.reefHost&&window.reefHost!=='local'&&!window.reefCapabilities?.includes('control-v1'))return;
  const checkKey=key(id),host=window.reefHost||'local',project=projects.find(p=>p.id===id);checking.add(checkKey);render(current());
  try{
   await request('/api/image-updates',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams({csrf,id})});
   if(project)project.image_updates={...project.image_updates,checking:true};
  }catch(error){if((window.reefHost||'local')===host)message(error.message,true);}finally{checking.delete(checkKey);render(current());}
 }
 find('#apply-image-update').addEventListener('click',()=>{if(!find('#apply-image-update').disabled)return window.ReefApplyImageUpdate?.();});
 button.addEventListener('click',()=>{const p=current();if(p)check(p.id);});
 window.addEventListener('reef-state',()=>render(current()));
 window.addEventListener('reef-open-project',()=>render(current()));
 window.addEventListener('reef-host-changed',()=>render(current()));
 window.ReefImageUpdates={render,check};render(current());
})();
