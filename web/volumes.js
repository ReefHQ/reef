'use strict';
(()=>{
 const find=id=>document.getElementById(id),view=find('volumes-view');
 let host=window.reefHost||'local',epoch=0,rows=[],selected=new Set(),busy=false,loading=false,loaded=false,hasSnapshot=false,loadError=false,timer,hostsReady=false;
 const available=()=>host==='local'||window.reefCapabilities?.includes('volumes-v1');
 const candidates=()=>rows.filter(row=>row.removable);
 const target=row=>({name:row.name,fingerprint:row.fingerprint});
 function notice(text,error=false){find('volumes-error').textContent=text;find('volumes-error').hidden=!text;find('volumes-error').className='notice'+(error?' error':'');}
 function controls(){
  const pending=available()&&(loading||(!hasSnapshot&&!loadError));
  find('volumes-loading').hidden=!pending;
  find('volumes-loading-text').textContent=hasSnapshot?'Refreshing volumes…':'Loading volumes…';
  find('volumes-rows').setAttribute('aria-busy',String(pending));
  if(!hasSnapshot){find('volumes-results').textContent='';find('volumes-empty').hidden=false;find('volumes-empty').textContent=pending?'Loading volumes…':available()?'Inventory unavailable. Use Refresh to try again.':'Upgrade the agent to manage volumes.';}

  const disabled=!available()||busy||loading||!loaded;
  find('volumes-refresh').disabled=!available()||loading;
  find('volumes-create-submit').disabled=!available()||busy;
  find('volumes-delete').disabled=disabled||!selected.size;
  find('volumes-cleanup').disabled=disabled||!candidates().length;
  find('volumes-select-all').disabled=disabled;
 }
 function status(op){
  busy=!!op?.running;
  window.ReefOperationPanels.render(find('volumes-operation'),op,{
   kind:'volumes',host,summary:op?.id?`${op.action}: ${busy?'in progress…':'finished'}`:'',
   result:row=>`${row.id}: ${row.message}`
  });
  controls();
 }
 function filtered(){const query=find('volumes-search').value.trim().toLowerCase(),usage=find('volumes-usage').value;return rows.filter(row=>(!query||`${row.name} ${row.driver} ${row.project}`.toLowerCase().includes(query))&&(!usage||(usage==='used'?row.containers.length:!row.containers.length)));}
 function render(){
  find('volume-nav-count').textContent=hasSnapshot?rows.length:'—';
  const visible=filtered();find('volumes-summary').textContent=loaded?`${rows.length} volumes · ${rows.filter(row=>!row.containers.length).length} not referenced by containers`:'';
  find('volumes-results').textContent=`${visible.length} shown · ${selected.size} selected`;
  const nodes=visible.map(row=>{
   const tr=document.createElement('tr'),selection=document.createElement('td'),title=document.createElement('td'),actions=document.createElement('td'),check=document.createElement('input');
   check.type='checkbox';check.checked=selected.has(row.name);check.disabled=busy||loading||!loaded||!row.removable;check.title=row.retain_reason||'';check.setAttribute('aria-label',`Select ${row.name}`);check.dataset.focusKey='volume-check-'+row.name;check.addEventListener('change',()=>{if(check.checked)selected.add(row.name);else selected.delete(row.name);render();});selection.append(check);
   const open=textNode('button',row.name,'text-button');open.type='button';open.dataset.focusKey='volume-open-'+row.name;open.addEventListener('click',()=>details(row));title.append(open);
   const remove=textNode('button','Delete','compact-button');remove.type='button';remove.disabled=busy||loading||!loaded||!row.removable;remove.title=row.retain_reason||'';remove.dataset.focusKey='volume-delete-'+row.name;remove.setAttribute('aria-label',`Delete volume ${row.name}`);remove.addEventListener('click',()=>removeVolumes([row],'remove'));actions.append(remove);
   tr.append(selection,title,textNode('td',row.driver),textNode('td',row.project||'—'),textNode('td',row.containers.length?row.containers.join(', '):'Unused'),actions);return tr;
  });replaceKeepingFocus(find('volumes-rows'),nodes);find('volumes-empty').hidden=!!visible.length;find('volumes-empty').textContent=hasSnapshot?'No volumes match this selection.':'Loading volumes…';
  const eligible=visible.filter(row=>row.removable);find('volumes-select-all').checked=!!eligible.length&&eligible.every(row=>selected.has(row.name));controls();
 }
 async function refresh(){
  if(!hostsReady||loading||document.hidden)return;
  if(!available()){notice('Upgrade this host’s agent to manage volumes.',true);controls();window.reefVolumesReady=true;window.dispatchEvent(new CustomEvent('reef-volumes-ready'));return;}
  const current=epoch;loading=true;loadError=false;controls();
  try{const data=await(await request('/api/volumes',undefined,host)).json();if(current!==epoch)return;
   const prior=new Map(rows.map(row=>[row.name,row.fingerprint]));rows=data.volumes;loaded=true;hasSnapshot=true;selected=new Set([...selected].filter(name=>rows.some(row=>row.name===name&&row.removable&&row.fingerprint===prior.get(name))));notice('');status(data.operation);render();
  }catch(error){if(current===epoch){loadError=true;notice(`${error.message}${hasSnapshot?' Showing the last successful inventory; refresh before acting.':''}`,true);loaded=false;render();}}
  finally{if(current===epoch){loading=false;render();window.reefVolumesReady=true;window.dispatchEvent(new CustomEvent('reef-volumes-ready'));}}
 }
 async function poll(){
  clearTimeout(timer);const current=epoch;
  try{if(available()&&!document.hidden){if(busy){const op=await(await request('/api/volumes/operation',undefined,host)).json();if(current!==epoch)return;const wasBusy=busy;status(op);render();if(wasBusy&&!busy)await refresh();}else await refresh();}}
  catch(error){if(current===epoch)notice(`Operation status unavailable. ${error.message} Refresh before retrying.`,true);}
  finally{if(current===epoch)timer=setTimeout(poll,busy?1500:10000);}
 }
 async function action(kind,targets,name,confirmation){
  const current=epoch,chosenHost=host;if(!available()||busy||(!loaded&&kind!=='create'))return;
  if(!await ask(confirmation))return;
  if(current!==epoch||!available()||busy||(!loaded&&kind!=='create'))return;
  busy=true;render();
  try{const body=new URLSearchParams({csrf,confirmed:'yes',action:kind,name,targets:JSON.stringify(targets.map(target))});const op=await(await request('/api/volumes/action',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body},chosenHost)).json();if(current!==epoch)return;status(op);notice('');selected.clear();render();clearTimeout(timer);timer=setTimeout(poll,500);}
  catch(error){if(current===epoch){notice(`${error.message} Refresh status before retrying; a disconnected request may have been accepted.`,true);clearTimeout(timer);timer=setTimeout(poll,500);}}
 }
 async function removeVolumes(targets,kind){
  if(!targets.length)return;if(targets.length>100){notice('Select at most 100 volumes per operation.',true);return;}
  await action(kind,targets,'',`Permanently delete ${targets.length} volume(s) and ALL DATA stored in them on ${window.reefHostName||'Local'}? No container reference does not mean the data is disposable. There is no undo. Verify your backups before confirming.\n\n${targets.map(row=>row.name).join('\n')}\n\nVolumes referenced by running or stopped containers will be retained.`);
 }
 function details(row){
  const fields=find('volume-detail-fields');fields.replaceChildren();for(const [label,value] of [['Name',row.name],['Driver',row.driver],['Scope',row.scope||'Not recorded'],['Created',row.created||'Not recorded'],['Docker mountpoint',row.mountpoint||'Not provided by driver'],['Compose project',row.project||'None'],['Containers (including stopped)',row.containers.join(', ')||'None'],['Removal',row.removable?'Eligible for explicit deletion; may contain valuable data':row.retain_reason]])fields.append(textNode('dt',label),textNode('dd',value));
  const remove=find('volume-detail-delete');remove.disabled=busy||!loaded||!row.removable;remove.onclick=()=>{find('volume-details').close();removeVolumes([row],'remove');};find('volume-details').showModal();
 }
 find('volume-detail-close').addEventListener('click',()=>find('volume-details').close());
 find('volumes-create').addEventListener('submit',event=>{event.preventDefault();const name=find('volumes-name').value.trim();action('create',[],name,`Create the Docker volume ${name} on ${window.reefHostName||'Local'} using the default local driver?`);});
 find('volumes-refresh').addEventListener('click',refresh);
 for(const id of ['volumes-search','volumes-usage'])find(id).addEventListener(id==='volumes-search'?'input':'change',render);
 find('volumes-select-all').addEventListener('change',event=>{for(const row of filtered()){if(!row.removable)continue;if(event.target.checked)selected.add(row.name);else selected.delete(row.name);}render();});
 find('volumes-delete').addEventListener('click',()=>removeVolumes(rows.filter(row=>selected.has(row.name)),'remove'));
 find('volumes-cleanup').addEventListener('click',()=>removeVolumes(candidates(),'cleanup'));
 window.addEventListener('reef-view-changed',()=>{if(!view.hidden){refresh();clearTimeout(timer);timer=setTimeout(poll,1500);}});
 window.addEventListener('reef-host-changed',()=>{epoch++;window.reefVolumesReady=false;host=window.reefHost||'local';rows=[];selected.clear();busy=false;loading=false;loaded=false;hasSnapshot=false;loadError=false;find('volumes-operation').hidden=true;if(find('volume-details').open)find('volume-details').close();for(const id of ['volumes-search','volumes-usage','volumes-name'])find(id).value='';notice('');render();clearTimeout(timer);timer=setTimeout(poll,100);});
 document.addEventListener('visibilitychange',()=>{if(!document.hidden)refresh();});
 render();(window.reefHostsReady||Promise.resolve()).then(()=>{hostsReady=true;refresh();clearTimeout(timer);timer=setTimeout(poll,1500);});
})();
