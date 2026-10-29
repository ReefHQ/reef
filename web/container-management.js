'use strict';
(()=>{
 const find=id=>document.querySelector('#'+id),dialog=find('container-management');
 let host='',id='',epoch=0,controller=null,timer=null,busy=false,review=null,operation={};
 const node=(tag,text)=>{const el=document.createElement(tag);el.textContent=text;return el;};
 function stop(){epoch++;controller?.abort();controller=null;clearTimeout(timer);timer=null;}
 function error(e){find('cm-error').textContent=e.message;find('cm-error').hidden=false;}
 function render(){
  const blocked=busy||operation.running||operation.unknown||!review?.allowed||window.reefAccess?.canWrite===false;
  for(const action of ['start','stop','restart','update','remove'])find('cm-'+action).disabled=blocked||(action==='start'?review?.state==='running':action==='stop'||action==='restart'?review?.state!=='running':false)||(action==='update'&&!review?.update_allowed)||(['update','remove'].includes(action)&&!find('cm-data').checked);
  find('cm-reason').hidden=!review||review.allowed&&review.update_allowed;find('cm-reason').textContent=review?.reason||review?.update_reason||'';
  find('cm-acknowledge').hidden=!operation.unknown;find('cm-acknowledge').disabled=busy||operation.persistence_error||window.reefAccess?.canWrite===false;
  find('cm-operation').hidden=!operation.id;find('cm-operation').replaceChildren();
  if(operation.id){find('cm-operation').append(node('strong',`${operation.action} · ${operation.phase?.replaceAll('_',' ')}`),node('p',operation.message||'Operation in progress…'),node('p',`Original: ${operation.container_id}${operation.new_id?' · Replacement: '+operation.new_id:''}`));if(operation.unknown)find('cm-operation').append(node('p',`Review required. Inspect both IDs on this host, their names, network connections and restart policies. Temporary name: ${operation.temporary_name||'none'}; previous name: ${operation.previous_name||'none'}. Acknowledge only after resolving host state; acknowledgement performs no Docker action.`));}
 }
 async function read(){
  clearTimeout(timer);if(!dialog.open||document.hidden)return;const ticket=epoch,chosenHost=host,chosenID=id;controller?.abort();controller=new AbortController();find('cm-loading').hidden=false;
  try{
   const op=await(await request('/api/container-management/operation',{signal:controller.signal},chosenHost)).json();if(ticket!==epoch||!dialog.open)return;operation=op;render();
   if(op.ok&&op.action==='remove'&&(!id||id===op.container_id)){review=null;find('cm-fields').replaceChildren(node('p','Container deleted. Its volumes and image were retained.'));find('cm-error').hidden=true;return;}
   if(op.ok&&op.action==='update'&&op.new_id&&id===op.container_id)id=op.new_id;
   if(!id&&op.container_id)id=op.new_id||op.container_id;
   const readID=id;if(!readID)return;
   if(!op.running){const data=await(await request('/api/container-management?'+new URLSearchParams({id:readID}),{signal:controller.signal},chosenHost)).json();if(ticket!==epoch||!dialog.open)return;if(data.container.id!==readID)throw Error('Container identity changed; reopen its details');review=data.container;const fields=[];for(const [name,value]of [['Host',find('cm-host').textContent],['Container',review.name],['ID',review.id],['Image',review.image],['Image ID',review.image_id],['State',review.state],['Mounts retained',review.mount_count],['Networks',review.networks.join(', ')||'None'],['Restart policy',review.restart_policy||'no']])fields.push(node('dt',name),node('dd',String(value)));find('cm-fields').replaceChildren(...fields);find('cm-error').hidden=true;}
  }catch(e){if(ticket!==epoch||e.name==='AbortError')return;review=null;error(e);}finally{if(ticket===epoch){find('cm-loading').hidden=true;render();if(dialog.open&&!document.hidden)timer=setTimeout(read,3000);}}
 }
 async function act(action){
  if(busy||!review||find('cm-'+action).disabled)return;const ticket=epoch,chosenHost=host,target={...review};busy=true;render();
  try{if(!await ask(`${action==='remove'?'Delete':action==='update'?'Update image for':action.charAt(0).toUpperCase()+action.slice(1)} ${target.name} on ${find('cm-host').textContent}? Container ${target.id}. ${action==='update'?'The image will be pulled, the container recreated and its current configuration/mounts reused. The old writable layer is lost on success. Recovery cannot undo application writes to shared volumes.':action==='remove'?'This stops and permanently removes this container and its writable layer. Volumes and images are retained.':'This changes the container running state and may interrupt its application.'}`)||ticket!==epoch||!dialog.open)return;
   const op=await(await request('/api/container-management/action',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams({csrf,confirmed:'yes',data_reviewed:find('cm-data').checked?'yes':'no',action,id:target.id,fingerprint:target.fingerprint})},chosenHost)).json();if(ticket!==epoch||!dialog.open)return;operation=op;review=null;find('cm-data').checked=false;find('cm-error').hidden=true;render();
  }catch(e){if(ticket===epoch){review=null;error(Error(e.message+' Refresh host state before retrying; an interrupted response does not prove that the action failed.'));}}finally{if(ticket===epoch){busy=false;render();read();}}
 }
 window.ReefContainerManagement={open(c){stop();host=c.host_id||window.reefHost||'local';id=c.id;review=null;operation={};busy=false;find('cm-title').textContent='Manage · '+c.name;find('cm-host').textContent=c.host_name||window.reefHostName||'Local';find('cm-data').checked=false;find('cm-fields').replaceChildren();find('cm-error').hidden=true;if(!dialog.open)dialog.showModal();render();if(host!=='local'&&!window.reefCapabilities?.includes('containers-v1')){error(Error('Upgrade the agent to 0.22.0 or later for standalone container management'));return;}read();}};
 find('container-operations').addEventListener('click',()=>window.ReefContainerManagement.open({id:'',name:'Latest operation',host_id:window.reefHost||'local',host_name:window.reefHostName||'Local'}));
 for(const action of ['start','stop','restart','update','remove'])find('cm-'+action).addEventListener('click',()=>act(action));
 find('cm-data').addEventListener('change',render);find('cm-refresh').addEventListener('click',()=>{if(!busy)read()});find('cm-close').addEventListener('click',()=>dialog.close());dialog.addEventListener('close',()=>{stop();review=null;busy=false;});
 find('cm-acknowledge').addEventListener('click',async()=>{if(busy||!operation.unknown)return;const ticket=epoch,op=operation.id,chosenHost=host;busy=true;render();try{if(!await ask('Have you resolved this interrupted operation on the selected host, including both container IDs, networks and restart policies? This only clears the review block; no Docker action is replayed.')||ticket!==epoch||!dialog.open)return;operation=await(await request('/api/container-management/acknowledge',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams({csrf,confirmed:'yes',operation:op})},chosenHost)).json();}catch(e){if(ticket===epoch)error(e);}finally{if(ticket===epoch){busy=false;render();read();}}});
 window.addEventListener('reef-host-changed',()=>{if(dialog.open)dialog.close();});document.addEventListener('visibilitychange',()=>{if(document.hidden){controller?.abort();clearTimeout(timer);}else if(dialog.open&&!busy)read();});
})();
