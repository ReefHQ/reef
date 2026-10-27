'use strict';
(()=>{
 const find=s=>document.querySelector(s),button=find('#check-image-updates');
 const labels={current:'Up to date',available:'Update available',unknown:'Unable to check',pinned:'Digest pinned',local:'Local build',not_deployed:'Not deployed'};
 let checking='';
 function render(p){
  const report=p?.image_updates,remote=window.reefHost&&window.reefHost!=='local';
  button.disabled=!p||remote||p.busy||report?.checking||checking===p.id;
  find('#image-updates-summary').textContent=ReefUI.imageUpdateSummary(report);
  find('#image-updates-checked').textContent=report?.checked_at&&!report.checked_at.startsWith('0001-')?`Last checked ${new Date(report.checked_at).toLocaleString('en-GB')} · automatic checks every 6 hours`: 'Automatic checks every 6 hours. Checks use the saved Compose configuration.';
  const nodes=(report?.services||[]).map(service=>{
   const item=document.createElement('li'),name=document.createElement('strong'),state=document.createElement('span'),image=document.createElement('span');
   name.textContent=service.service;state.textContent=labels[service.status]||labels.unknown;state.className='tag '+(service.status==='available'?'update-available':'neutral-tag');image.textContent=service.image;image.className='muted image-update-reference';item.append(name,state,image);
   if(service.message){const note=document.createElement('small');note.textContent=service.message;note.className='muted';item.append(note);}
   return item;
  });find('#image-update-services').replaceChildren(...nodes);
 }
 async function check(id){
  if(checking||window.reefHost&&window.reefHost!=='local')return;
  checking=id;render(current());
  try{
   await request('/api/image-updates',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams({csrf,id})});
   const p=projects.find(p=>p.id===id);if(p)p.image_updates={...p.image_updates,checking:true};
  }catch(error){message(error.message,true);}finally{checking='';render(current());}
 }
 button.addEventListener('click',()=>{const p=current();if(p)check(p.id);});
 window.addEventListener('reef-state',()=>render(current()));
 window.addEventListener('reef-open-project',()=>render(current()));
 window.addEventListener('reef-host-changed',()=>render(current()));
 window.ReefImageUpdates={render,check};render(current());
})();
