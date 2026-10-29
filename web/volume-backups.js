'use strict';
(()=>{
 const find=id=>document.getElementById(id),dialog=find('volume-backups');
 let epoch=0,host='local',busy=false,loading=false,volumes=[],offset=0,total=0,timer;
 const canWrite=()=>window.reefAccess?.canWrite!==false;
 const available=()=>host==='local'||window.reefCapabilities?.includes('volume-backups-v1');
 const node=(tag,text,cls='')=>{const n=document.createElement(tag);n.textContent=text;n.className=cls;return n;};
 function notice(text){find('backups-error').textContent=text;find('backups-error').hidden=!text;}
 function controls(){find('backups-create').disabled=!canWrite()||busy||loading||!available()||!volumes.length;find('backups-volume').disabled=busy||loading;find('backups-refresh').disabled=loading;find('backups-previous').disabled=loading||offset===0;find('backups-next').disabled=loading||offset+50>=total;find('backups-loading').hidden=!loading;}
 async function refresh(){
  if(!dialog.open||loading||document.hidden)return;const current=epoch;loading=true;controls();
  try{if(!available())throw new Error('Upgrade this host’s agent for volume backups.');const data=await(await request('/api/volume-backups?offset='+offset,undefined,host)).json();if(current!==epoch)return;
   offset=data.offset;total=data.total;busy=!!data.operation?.running;find('backups-summary').textContent=`${total} records · ${total?offset+1:0}–${Math.min(offset+50,total)} shown · keeps ${data.retention} successful backups per volume`;
   window.ReefOperationPanels.render(find('backups-operation'),data.operation,{kind:'volume-backups',host,summary:data.operation?.id?`${data.operation.action}: ${busy?'in progress…':'finished'}`:'',result:r=>`${r.id}: ${r.message}`});
   const records=data.backups.map(row=>{const card=node('article','','backup-record');card.append(node('h3',row.destination?`${row.volume} → ${row.destination}`:row.volume),node('p',`${new Date(row.created).toLocaleString('en-GB')} · ${row.status}${row.bytes?' · '+(row.bytes/1048576).toFixed(1)+' MiB':''}`,'muted small'),node('p',row.message));
    if(row.containers?.length&&(row.status==='interrupted'||row.status==='failed'))card.append(node('p','Containers to check: '+row.containers.join(', '),'small'));
    const actions=node('div','','dialog-actions');if(row.status==='succeeded'&&!row.destination){const restore=node('button','Restore to a new volume');restore.type='button';restore.disabled=!canWrite()||busy;restore.addEventListener('click',()=>restoreDialog(row));actions.append(restore);}
    const remove=node('button','Delete record'+(!row.destination?' and archive':''),'quiet');remove.type='button';remove.disabled=!canWrite()||busy||row.status==='pending';remove.addEventListener('click',()=>act({action:'delete',backup:row.id},`Permanently delete this ${row.destination?'restoration record':'backup record and archive'} for ${row.volume} on ${window.reefHostName||host}?${row.status==='interrupted'?' Confirm you have checked and recovered the listed containers first.':''} Docker volumes are kept.`));actions.append(remove);card.append(actions);return card;});find('backups-records').replaceChildren(...records);find('backups-empty').hidden=!!records.length;notice('');
  }catch(e){if(current===epoch)notice(e.message);}finally{if(current===epoch){loading=false;controls();clearTimeout(timer);if(dialog.open)timer=setTimeout(refresh,busy?1500:10000);}}
 }
 async function act(fields,confirmation){
  const current=epoch,chosenHost=host;if(!canWrite()||busy||!await ask(confirmation))return;if(current!==epoch||!dialog.open||busy)return;busy=true;controls();
  try{await request('/api/volume-backups',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams({csrf,confirmed:'yes',...fields})},chosenHost);if(current!==epoch)return;notice('');if(fields.action==='restore')find('backups-restore-form').hidden=true;}
  catch(e){if(current===epoch)notice(`${e.message} Refresh status before retrying; the request may have been accepted.`);}
  finally{if(current===epoch){busy=false;controls();clearTimeout(timer);timer=setTimeout(refresh,500);}}
 }
 async function open(row){epoch++;host=window.reefHost||'local';offset=0;total=0;loading=false;busy=false;volumes=[];find('backups-records').replaceChildren();find('backups-operation').hidden=true;find('backups-volume').replaceChildren();find('backups-restore-form').hidden=true;notice('');dialog.showModal();controls();const current=epoch;
  try{if(available()){const data=await(await request('/api/volumes',undefined,host)).json();if(current!==epoch)return;volumes=data.volumes.filter(v=>v.driver==='local'&&v.scope==='local');for(const v of volumes){const option=node('option',v.name);option.value=v.name;find('backups-volume').append(option);}if(row&&volumes.some(v=>v.name===row.name))find('backups-volume').value=row.name;}}
  catch(e){if(current===epoch)notice(e.message);}if(current===epoch)await refresh();
 }
 async function backup(){const row=volumes.find(v=>v.name===find('backups-volume').value);if(!row||busy||loading)return;const current=epoch;loading=true;controls();let data;
  try{data=await(await request('/api/volume-backups?'+new URLSearchParams({volume:row.name,fingerprint:row.fingerprint}),undefined,host)).json();}catch(e){if(current===epoch)notice(e.message);}finally{if(current===epoch){loading=false;controls();}}
  if(!data||current!==epoch)return;const p=data.preview,active=p.containers.filter(c=>c.running);await act({action:'backup',volume:p.name,fingerprint:p.fingerprint,usage:p.usage},`Back up ${p.name} on ${window.reefHostName||host}?\n\n${active.length?'Reef will temporarily stop and then restart these running containers:\n'+active.map(c=>c.name).join('\n'):'No running containers currently use this volume.'}\n\nExpect application downtime. Already stopped containers remain stopped. The private archive stays on this host. A clean shutdown is required for application consistency; this is not a database dump.`);
 }
 function restoreDialog(row){find('backups-restore-form').hidden=false;find('backups-restore-id').value=row.id;find('backups-restore-name').value='';find('backups-restore-source').textContent=`Restore ${row.volume} from ${new Date(row.created).toLocaleString('en-GB')}`;find('backups-restore-form').scrollIntoView({block:'nearest'});find('backups-restore-name').focus();}
 find('backups-restore-form').addEventListener('submit',event=>{event.preventDefault();const name=find('backups-restore-name').value.trim(),id=find('backups-restore-id').value;act({action:'restore',backup:id,name},`Restore into NEW volume ${name} on ${window.reefHostName||host}? Existing volumes and application configuration stay unchanged. Do not attach applications until Reef confirms success, then verify the restored data.`);});
 find('backups-restore-cancel').addEventListener('click',()=>find('backups-restore-form').hidden=true);
 find('volumes-backups').addEventListener('click',()=>open());find('backups-create').addEventListener('click',backup);find('backups-close').addEventListener('click',()=>dialog.close());find('backups-refresh').addEventListener('click',refresh);find('backups-previous').addEventListener('click',()=>{offset=Math.max(0,offset-50);refresh();});find('backups-next').addEventListener('click',()=>{offset+=50;refresh();});
 dialog.addEventListener('close',()=>{epoch++;clearTimeout(timer);loading=false;busy=false;find('backups-restore-name').value='';});window.addEventListener('reef-host-changed',()=>{if(dialog.open)dialog.close();});document.addEventListener('visibilitychange',()=>{if(!document.hidden&&dialog.open)refresh();});window.ReefVolumeBackups={open};
})();
