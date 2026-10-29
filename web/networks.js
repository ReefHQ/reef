'use strict';
(()=>{
 const find=id=>document.getElementById(id),view=find('networks-view');
 let host=window.reefHost||'local',epoch=0,rows=[],containers=[],selected=new Set(),busy=false,loading=false,loaded=false,hasSnapshot=false,loadError=false,timer,hostsReady=false;
 const canWrite=()=>window.reefAccess?.canWrite!==false;
 const available=()=>host==='local'||window.reefCapabilities?.includes('networks-v1');
 const candidates=()=>rows.filter(row=>row.removable);
 const target=row=>({id:row.id,fingerprint:row.fingerprint});
 function notice(text,error=false){find('networks-error').textContent=text;find('networks-error').hidden=!text;find('networks-error').className='notice'+(error?' error':'');}
 function controls(){
  const pending=available()&&(loading||(!hasSnapshot&&!loadError));
  find('networks-loading').hidden=!pending;
  find('networks-loading-text').textContent=hasSnapshot?'Refreshing networks…':'Loading networks…';
  find('networks-rows').setAttribute('aria-busy',String(pending));
  if(!hasSnapshot){find('networks-results').textContent='';find('networks-empty').hidden=false;find('networks-empty').textContent=pending?'Loading networks…':available()?'Inventory unavailable. Use Refresh to try again.':'Upgrade the agent to manage networks.';}

  const disabled=!canWrite()||!available()||busy||loading||!loaded;
  find('networks-refresh').disabled=!available()||loading;
  find('networks-create-submit').disabled=!canWrite()||!available()||busy;
  find('networks-delete').disabled=disabled||!selected.size;
  find('networks-cleanup').disabled=disabled||!candidates().length;
  find('networks-select-all').disabled=disabled;
 }
 function status(op){
  busy=!!op?.running;
  window.ReefOperationPanels.render(find('networks-operation'),op,{
   kind:'networks',host,summary:op?.id?`${op.action}: ${busy?'in progress…':'finished'}`:'',
   result:row=>`${row.id}: ${row.message}`
  });
  controls();
 }
 function eligibleContainer(c){return !c.protected&&c.state==='running'&&!['host','none'].includes(c.mode)&&!c.mode?.startsWith('container:');}
 function filtered(){const query=find('networks-search').value.trim().toLowerCase(),usage=find('networks-usage').value;return rows.filter(row=>(!query||`${row.name} ${row.driver} ${row.project}`.toLowerCase().includes(query))&&(!usage||(usage==='used'?row.containers.length:!row.containers.length)));}
 function render(){
  find('network-nav-count').textContent=hasSnapshot?rows.length:'—';
  const visible=filtered();find('networks-summary').textContent=loaded?`${rows.length} networks · ${rows.filter(row=>!row.containers.length).length} not referenced by containers`:'';
  find('networks-results').textContent=`${visible.length} shown · ${selected.size} selected`;
  const nodes=visible.map(row=>{
   const tr=document.createElement('tr'),selection=document.createElement('td'),title=document.createElement('td'),actions=document.createElement('td'),check=document.createElement('input');
   check.type='checkbox';check.checked=selected.has(row.id);check.disabled= !canWrite()||busy||loading||!loaded||!row.removable;check.title=row.retain_reason||'';check.setAttribute('aria-label',`Select ${row.name}`);check.dataset.focusKey='network-check-'+row.name;check.addEventListener('change',()=>{if(check.checked)selected.add(row.id);else selected.delete(row.id);render();});selection.append(check);
   const open=textNode('button',row.name,'text-button');open.type='button';open.dataset.focusKey='network-open-'+row.name;open.addEventListener('click',()=>details(row));title.append(open);
   const remove=textNode('button','Delete','compact-button');remove.type='button';remove.disabled= !canWrite()||busy||loading||!loaded||!row.removable;remove.title=row.retain_reason||'';remove.dataset.focusKey='network-delete-'+row.name;remove.setAttribute('aria-label',`Delete network ${row.name}`);remove.addEventListener('click',()=>removeNetworks([row],'remove'));actions.append(remove);
   tr.append(selection,title,textNode('td',row.driver),textNode('td',row.project||'—'),textNode('td',row.containers.length?row.containers.join(', '):'Unused'),actions);return tr;
  });replaceKeepingFocus(find('networks-rows'),nodes);find('networks-empty').hidden=!!visible.length;find('networks-empty').textContent=hasSnapshot?'No networks match this selection.':'Loading networks…';
  const eligible=visible.filter(row=>row.removable);find('networks-select-all').checked=!!eligible.length&&eligible.every(row=>selected.has(row.id));controls();
 }
 async function refresh(){
  if(!hostsReady||loading||document.hidden)return;
  if(!available()){notice('Upgrade this host’s agent to manage networks.',true);controls();window.reefNetworksReady=true;window.dispatchEvent(new CustomEvent('reef-networks-ready'));return;}
  const current=epoch;loading=true;loadError=false;controls();
  try{const data=await(await request('/api/networks',undefined,host)).json();if(current!==epoch)return;
   const prior=new Map(rows.map(row=>[row.id,row.fingerprint]));rows=data.networks;containers=data.containers||[];loaded=true;hasSnapshot=true;selected=new Set([...selected].filter(name=>rows.some(row=>row.id===name&&row.removable&&row.fingerprint===prior.get(name))));notice('');status(data.operation);render();
  }catch(error){if(current===epoch){loadError=true;notice(`${error.message}${hasSnapshot?' Showing the last successful inventory; refresh before acting.':''}`,true);loaded=false;render();}}
  finally{if(current===epoch){loading=false;render();window.reefNetworksReady=true;window.dispatchEvent(new CustomEvent('reef-networks-ready'));}}
 }
 async function poll(){
  clearTimeout(timer);const current=epoch;
  try{if(available()&&!document.hidden){if(busy){const op=await(await request('/api/networks/operation',undefined,host)).json();if(current!==epoch)return;const wasBusy=busy;status(op);render();if(wasBusy&&!busy)await refresh();}else await refresh();}}
  catch(error){if(current===epoch)notice(`Operation status unavailable. ${error.message} Refresh before retrying.`,true);}
  finally{if(current===epoch)timer=setTimeout(poll,busy?1500:10000);}
 }
 async function action(kind,targets,name,confirmation,container=''){
  const current=epoch,chosenHost=host;if(!available()||busy||(!loaded&&kind!=='create'))return;
  if(!await ask(confirmation))return;
  if(current!==epoch||!available()||busy||(!loaded&&kind!=='create'))return;
  busy=true;render();
  try{const body=new URLSearchParams({csrf,confirmed:'yes',action:kind,name,container,targets:JSON.stringify(targets.map(target))});const op=await(await request('/api/networks/action',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body},chosenHost)).json();if(current!==epoch)return;status(op);notice('');selected.clear();render();clearTimeout(timer);timer=setTimeout(poll,500);}
  catch(error){if(current===epoch){notice(`${error.message} Refresh status before retrying; a disconnected request may have been accepted.`,true);clearTimeout(timer);timer=setTimeout(poll,500);}}
 }
 async function removeNetworks(targets,kind){
  if(!targets.length)return;if(targets.length>2000){notice('Select at most 2000 networks per operation.',true);return;}
  await action(kind,targets,'',`Delete ${targets.length} unused network(s) on ${window.reefHostName||'Local'}?\n\n${targets.map(row=>row.name).join('\n')}\n\nNetworks referenced by running or stopped containers and Reef administration networks will be retained. Compose may recreate its networks later.`);
 }
 function details(row){
  const chosenEpoch=epoch,fields=find('network-detail-fields');fields.replaceChildren();
  for(const [label,value] of [['Name',row.name],['ID',row.id],['Driver',row.driver],['Scope',row.scope],['Created',row.created||'Not recorded'],['Internal',row.internal?'Yes':'No'],['IPv6',row.ipv6?'Yes':'No'],['Attachable',row.attachable?'Yes':'No'],['Subnets',(row.subnets||[]).join(', ')||'Automatic'],['Gateways',(row.gateways||[]).join(', ')||'None'],['Compose project',row.project||'None'],['Containers (including stopped)',row.containers.join(', ')||'None'],['Removal',row.removable?'Unused; eligible for explicit deletion':row.retain_reason]])fields.append(textNode('dt',label),textNode('dd',value));
  const remove=find('network-detail-delete');remove.disabled= !canWrite()||busy||loading||!loaded||!row.removable;remove.onclick=()=>{if(chosenEpoch!==epoch)return;find('network-details').close();removeNetworks([row],'remove');};
  const select=find('network-container');select.replaceChildren(new Option('Choose a running container',''),...containers.filter(eligibleContainer).map(c=>new Option(c.name,c.id)));
  const connect=find('network-connect'),disconnect=find('network-disconnect');
  function connectionControls(){const c=containers.find(c=>c.id===select.value),connected=c&&Object.values(c.networks||{}).includes(row.id);const disabled=!canWrite()||chosenEpoch!==epoch||!loaded||loading||busy||!row.mutable||!c||!eligibleContainer(c);connect.disabled=disabled||connected;disconnect.disabled=disabled||!connected;}
  select.onchange=connectionControls;connectionControls();
  for(const [button,kind] of [[connect,'connect'],[disconnect,'disconnect']])button.onclick=()=>{const c=containers.find(c=>c.id===select.value);if(chosenEpoch!==epoch||!c||!eligibleContainer(c)||!row.mutable||busy||loading||!loaded)return;find('network-details').close();action(kind,[row],'',`${kind==='connect'?'Connect':'Disconnect'} ${c.name} ${kind==='connect'?'to':'from'} ${row.name} on ${window.reefHostName||'Local'}? This changes live connectivity and does not edit Compose files. A later Compose recreation may overwrite this change.`,c.id);};
  find('network-details').showModal();
 }
 find('network-detail-close').addEventListener('click',()=>find('network-details').close());
 find('networks-create').addEventListener('submit',event=>{event.preventDefault();const name=find('networks-name').value.trim();action('create',[],name,`Create the Docker network ${name} on ${window.reefHostName||'Local'} using the default bridge driver and automatic IP addressing?`);});
 find('networks-refresh').addEventListener('click',refresh);
 for(const id of ['networks-search','networks-usage'])find(id).addEventListener(id==='networks-search'?'input':'change',render);
 find('networks-select-all').addEventListener('change',event=>{for(const row of filtered()){if(!row.removable)continue;if(event.target.checked)selected.add(row.id);else selected.delete(row.id);}render();});
 find('networks-delete').addEventListener('click',()=>removeNetworks(rows.filter(row=>selected.has(row.id)),'remove'));
 find('networks-cleanup').addEventListener('click',()=>removeNetworks(candidates(),'cleanup'));
 window.addEventListener('reef-view-changed',()=>{if(!view.hidden){refresh();clearTimeout(timer);timer=setTimeout(poll,1500);}});
 window.addEventListener('reef-host-changed',()=>{epoch++;window.reefNetworksReady=false;host=window.reefHost||'local';rows=[];containers=[];selected.clear();busy=false;loading=false;loaded=false;hasSnapshot=false;loadError=false;find('networks-operation').hidden=true;if(find('network-details').open)find('network-details').close();for(const id of ['networks-search','networks-usage','networks-name'])find(id).value='';notice('');render();clearTimeout(timer);timer=setTimeout(poll,100);});
 document.addEventListener('visibilitychange',()=>{if(!document.hidden)refresh();});
 render();(window.reefHostsReady||Promise.resolve()).then(()=>{hostsReady=true;refresh();clearTimeout(timer);timer=setTimeout(poll,1500);});
})();
