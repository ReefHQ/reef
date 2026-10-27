'use strict';
(()=>{
 const find=id=>document.getElementById(id),view=find('images-view');
 let host=window.reefHost||'local',epoch=0,rows=[],selected=new Set(),busy=false,loading=false,loaded=false,hasSnapshot=false,loadError=false,operation='',timer,hostsReady=false;
 const available=()=>host==='local'||window.reefCapabilities?.includes('images-v1');
 const bytes=value=>{if(value<1024)return value+' B';const units=['KiB','MiB','GiB','TiB'];let n=value/1024,i=0;while(n>=1024&&i<units.length-1){n/=1024;i++;}return n.toFixed(1)+' '+units[i];};
 const name=row=>row.tags?.join(', ')||'<untagged>';
 const target=row=>({id:row.id,fingerprint:row.fingerprint});
 function notice(text,error=false){find('images-error').textContent=text;find('images-error').hidden=!text;find('images-error').className='notice'+(error?' error':'');}
 function controls(){
  const pending=available()&&(loading||(!hasSnapshot&&!loadError));
  find('images-loading').hidden=!pending;
  find('images-loading-text').textContent=hasSnapshot?'Refreshing images…':'Loading images…';
  find('images-rows').setAttribute('aria-busy',String(pending));
  if(!hasSnapshot){find('images-results').textContent='';find('images-empty').hidden=false;find('images-empty').textContent=pending?'Loading images…':available()?'Inventory unavailable. Use Refresh to try again.':'Upgrade the agent to manage images.';}

  const disabled=!available()||busy||loading||!loaded;
  find('images-refresh').disabled=!available()||loading;
  find('images-pull-submit').disabled=!available()||busy;
  find('images-delete').disabled=disabled||!selected.size;
  find('images-cleanup').disabled=disabled||!rows.some(row=>!row.containers.length&&(find('images-cleanup-mode').value==='all'||!row.tags?.length));
  find('images-select-all').disabled=disabled;
 }
 function status(op){
  busy=!!op?.running;operation=op?.id||'';
  const node=find('images-operation');node.hidden=!operation;
  if(operation){node.replaceChildren(textNode('strong',`${op.action}: ${busy?'in progress…':op.skipped?'stopped':'finished'}${op.total?` · ${op.completed||0} / ${op.total} images processed`:''}${op.skipped?` · ${op.skipped} not processed`:''}${busy&&op.batches>1?` · batch ${op.batch||1} / ${op.batches}`:''}`));for(const result of op.results||[]){node.append(textNode('p',`${result.id.slice(0,28)}: ${result.message}`,result.ok?'small':'small error-text'));}}
  controls();
 }
 function render(){
  find('image-nav-count').textContent=hasSnapshot?rows.length:'—';
  const query=find('images-search').value.toLowerCase().trim(),usage=find('images-usage').value;
  const filtered=rows.filter(row=>(!query||`${row.id} ${name(row)} ${(row.digests||[]).join(' ')}`.toLowerCase().includes(query))&&(!usage||(usage==='used'?row.containers.length:!row.containers.length)));
  find('images-summary').textContent=loaded?`${rows.length} images · ${rows.filter(r=>!r.containers.length).length} unused · ${bytes(rows.reduce((n,r)=>n+r.size,0))} cumulative size (layers may be shared)`:'';
  find('images-results').textContent=`${filtered.length} shown · ${selected.size} selected`;
  const nodes=filtered.map(row=>{
   const tr=document.createElement('tr'),check=document.createElement('input'),selection=document.createElement('td'),title=document.createElement('td'),actions=document.createElement('td');
   check.type='checkbox';check.checked=selected.has(row.id);check.disabled=busy||!!row.containers.length;check.setAttribute('aria-label',`Select ${name(row)} ${row.id.slice(7,19)}`);check.dataset.focusKey='image-check-'+row.id;check.addEventListener('change',()=>{if(check.checked)selected.add(row.id);else selected.delete(row.id);render();});selection.append(check);
   const open=textNode('button',name(row),'text-button');open.type='button';open.dataset.focusKey='image-open-'+row.id;open.addEventListener('click',()=>details(row));title.append(open,textNode('small',row.id.slice(7,19),'muted'));
   const tag=textNode('button','Add tag','compact-button');tag.type='button';tag.disabled=busy;tag.addEventListener('click',()=>tagDialog(row));actions.append(tag);
   tr.append(selection,title,textNode('td',bytes(row.size)),textNode('td',`${row.os}/${row.architecture}${row.variant?'/'+row.variant:''}`),textNode('td',row.containers.length?row.containers.join(', '):'Unused'),actions);return tr;
  });
  replaceKeepingFocus(find('images-rows'),nodes);find('images-empty').hidden=!!filtered.length;find('images-empty').textContent=hasSnapshot?'No images match this selection.':'Loading images…';
  find('images-select-all').checked=filtered.some(r=>!r.containers.length)&&filtered.filter(r=>!r.containers.length).every(r=>selected.has(r.id));
  controls();
 }
 async function refresh(){
  if(!hostsReady||loading||document.hidden)return;
  if(!available()){notice('Upgrade this host’s agent to manage images.',true);controls();window.reefImagesReady=true;window.dispatchEvent(new CustomEvent('reef-images-ready'));return;}
  const current=epoch;loading=true;loadError=false;controls();
  try{const data=await(await request('/api/images',undefined,host)).json();if(current!==epoch)return;
   const prior=new Map(rows.map(row=>[row.id,row.fingerprint]));rows=data.images;loaded=true;hasSnapshot=true;
   selected=new Set([...selected].filter(id=>rows.some(row=>row.id===id&&!row.containers.length&&row.fingerprint===prior.get(id))));
   notice('');status(data.operation);render();
  }catch(error){if(current===epoch){loadError=true;notice(`${error.message}${hasSnapshot?' Showing the last successful inventory; refresh before acting.':''}`,true);loaded=false;controls();}}
  finally{if(current===epoch){loading=false;controls();window.reefImagesReady=true;window.dispatchEvent(new CustomEvent('reef-images-ready'));}}
 }
 async function poll(){
  clearTimeout(timer);const current=epoch;
  try{if(available()&&!document.hidden){if(busy){const op=await(await request('/api/images/operation',undefined,host)).json();if(current!==epoch)return;const wasBusy=busy;status(op);if(wasBusy!==busy)render();if(wasBusy&&!busy)await refresh();}else await refresh();}}
  catch(error){if(current===epoch)notice(`Operation status unavailable. ${error.message} Refresh before retrying.`,true);}
  finally{if(current===epoch)timer=setTimeout(poll,busy?1500:10000);}
 }
 async function action(action,targets=[],reference='',tag='',confirmation=''){
  const current=epoch,chosenHost=host;
  if(!available()||busy||(!loaded&&action!=='pull'))return;
  if(confirmation&&!await ask(confirmation))return;
  if(current!==epoch||!available()||busy||(!loaded&&action!=='pull'))return;
  busy=true;controls();render();
  try{const body=new URLSearchParams({csrf,action,reference,tag,targets:JSON.stringify(targets.map(target))});const op=await(await request('/api/images/action',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body},chosenHost)).json();if(current!==epoch)return;status(op);notice('');selected.clear();render();clearTimeout(timer);timer=setTimeout(poll,500);}
  catch(error){if(current===epoch){notice(`${error.message} Refresh status before retrying; a disconnected request may have been accepted.`,true);/* Keep mutations disabled until a successful status refresh. */clearTimeout(timer);timer=setTimeout(poll,500);}}
 }
 function details(row){
  const fields=find('image-detail-fields');fields.replaceChildren();
  for(const [label,value] of [['Image ID',row.id],['Tags',(row.tags||[]).join('\n')||'None'],['Repository digests',(row.digests||[]).join('\n')||'None'],['Platform',`${row.os}/${row.architecture}${row.variant?'/'+row.variant:''}`],['Created',row.created||'Not recorded'],['Size',bytes(row.size)],['Containers (including stopped)',row.containers.join(', ')||'Unused']])fields.append(textNode('dt',label),textNode('dd',value));
  const actions=find('image-detail-actions');actions.replaceChildren();
  for(const tag of row.tags||[]){const button=textNode('button',`Remove tag ${tag}`,'compact-button');button.type='button';button.disabled=busy||!!row.containers.length;button.addEventListener('click',async()=>{find('image-details').close();await action('untag',[row],'',tag,`Remove ${tag} from ${hostName()}? Removing its last reference can also delete the unused image.`);});actions.append(button);}
  find('image-details').showModal();
 }
 function hostName(){return window.reefHostName||'Local';}
 let tagging=null;
 function tagDialog(row){tagging=row;find('image-tag-source').textContent=row.id;find('image-tag-reference').value='';find('image-tag-dialog').showModal();find('image-tag-reference').focus();}
 find('image-tag-form').addEventListener('submit',event=>{event.preventDefault();const row=tagging,tag=find('image-tag-reference').value.trim();find('image-tag-dialog').close();action('tag',[row],'',tag,`Add ${tag} on ${hostName()}? Existing destination tags are refused.`);});
 find('image-tag-cancel').addEventListener('click',()=>find('image-tag-dialog').close());
 find('image-detail-close').addEventListener('click',()=>find('image-details').close());
 find('images-pull').addEventListener('submit',event=>{event.preventDefault();const ref=find('images-reference').value.trim();action('pull',[],ref,'',`Download ${ref} on ${hostName()}? A moved tag will be updated; existing containers keep their current image.`);});
 find('images-refresh').addEventListener('click',refresh);
 for(const id of ['images-search','images-usage'])find(id).addEventListener(id==='images-search'?'input':'change',render);
 find('images-cleanup-mode').addEventListener('change',controls);
 find('images-select-all').addEventListener('change',event=>{const checked=event.target.checked,query=find('images-search').value.toLowerCase().trim(),usage=find('images-usage').value;for(const row of rows){if(row.containers.length||usage==='used'||(query&&!`${row.id} ${name(row)} ${(row.digests||[]).join(' ')}`.toLowerCase().includes(query)))continue;if(checked)selected.add(row.id);else selected.delete(row.id);}render();});
 async function remove(targets,kind){if(!targets.length)return;if(host!=='local'&&targets.length>100&&!window.reefCapabilities?.includes('images-batch-v1')){notice('Upgrade this host’s agent to clean this entire selection in one operation.',true);return;}await action(kind,targets,'','',`Delete ${targets.length} unused image(s) and all their tags from ${hostName()}? Images used by running or stopped containers will be retained. This cannot be undone without downloading or rebuilding them. Large selections are processed automatically in batches.\n\n${targets.map(row=>name(row)+' ['+row.id.slice(7,19)+']').join('\n')}`);}
 find('images-delete').addEventListener('click',()=>remove(rows.filter(row=>selected.has(row.id)),'remove'));
 // Batch deletion uses the exact reviewed IDs/fingerprints; never Docker's global prune.
 find('images-cleanup').addEventListener('click',()=>remove(rows.filter(row=>!row.containers.length&&(find('images-cleanup-mode').value==='all'||!row.tags?.length)),'cleanup'));
 window.addEventListener('reef-view-changed',()=>{if(!view.hidden){refresh();clearTimeout(timer);timer=setTimeout(poll,1500);}});
 window.addEventListener('reef-host-changed',()=>{epoch++;window.reefImagesReady=false;host=window.reefHost||'local';rows=[];selected.clear();busy=false;loading=false;loaded=false;hasSnapshot=false;loadError=false;operation='';for(const id of ['image-details','image-tag-dialog'])if(find(id).open)find(id).close();find('images-search').value='';find('images-usage').value='';find('images-operation').hidden=true;notice('');render();clearTimeout(timer);timer=setTimeout(poll,100);});
 document.addEventListener('visibilitychange',()=>{if(!document.hidden)refresh();});
 render();(window.reefHostsReady||Promise.resolve()).then(()=>{hostsReady=true;refresh();clearTimeout(timer);timer=setTimeout(poll,1500);});
})();
