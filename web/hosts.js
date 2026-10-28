'use strict';
(()=>{
 const find=s=>document.querySelector(s),dialog=find('#hosts-dialog'),picker=find('#host-select');
 window.reefHost='local';window.reefCapabilities=[];let hosts=[],busy=false,restoreHost=true;const updateStates=new Map();
 const updating=op=>['queued','pulling','recreating','reconnecting'].includes(op?.phase);
 function newer(a,b){if(!/^\d+\.\d+\.\d+$/.test(a||'')||!/^\d+\.\d+\.\d+$/.test(b||''))return false;const x=a.split('.'),y=b.split('.');for(let i=0;i<3;i++){if(x[i].length!==y[i].length)return x[i].length>y[i].length;if(x[i]!==y[i])return x[i]>y[i];}return false;}
 const node=(tag,text)=>{const el=document.createElement(tag);el.textContent=text;return el;};
 function showError(error){find('#hosts-error').textContent=error.message;find('#hosts-error').hidden=false;}
 async function action(fields){return request('/api/hosts',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams({csrf,...fields})});}
 async function refresh(){
  if(busy)return;busy=true;
  try{
   const result=await(await request('/api/hosts')).json();hosts=result.hosts;
   if(dialog.open)await Promise.all(hosts.filter(h=>h.status==='connected'&&h.capabilities?.includes('agent-update-v1')).map(async h=>{try{const state=await(await request('/api/agent-update?'+new URLSearchParams({host:h.id}))).json();updateStates.set(h.id,state);}catch(e){const state=updateStates.get(h.id);if(state)state.unreachable=true;}}));
   const active=restoreHost?ReefNavigation.read().host:picker.value||'local';picker.replaceChildren(new Option('Local','local'),...hosts.filter(h=>h.status!=='revoked').sort((a,b)=>a.name.localeCompare(b.name)).map(h=>new Option(`${h.name} · ${h.status}`,h.id)));picker.value=[...picker.options].some(o=>o.value===active)?active:'local';
   if(window.reefHost!==picker.value||JSON.stringify(window.reefCapabilities)!==JSON.stringify(hosts.find(h=>h.id===picker.value)?.capabilities||[]))change(restoreHost);
   find('#host-enroll-submit').disabled=!result.enabled;find('#hosts-setup').hidden=result.enabled;
   const rows=hosts.map(h=>{
    const row=node('article','');row.className='host-row';const title=node('strong',h.name),status=node('p',`${h.status}${h.stale?' · inventory stale':''}${h.error?' · '+h.error:''}${h.notice?' · '+h.notice:''}`);status.className='muted';
    const detail=node('p',`Last contact: ${h.last_contact&&!h.last_contact.startsWith('0001')?new Date(h.last_contact).toLocaleString():'never'} · Inventory: ${h.checked_at&&!h.checked_at.startsWith('0001')?new Date(h.checked_at).toLocaleString():'none'} · Agent: ${h.version||'unknown'}`);detail.className='muted small';
    const name=node('input','');name.value=h.name;name.maxLength=100;name.setAttribute('aria-label',`Name for ${h.name}`);name.disabled=h.status==='revoked';
    const rename=node('button','Rename');rename.type='button';rename.disabled=name.disabled;rename.addEventListener('click',async()=>{rename.disabled=true;try{await action({action:'rename',id:h.id,name:name.value});await refresh();}catch(e){showError(e);}finally{rename.disabled=name.disabled;}});
    const revoke=node('button','Revoke');revoke.type='button';revoke.className='danger';revoke.disabled=name.disabled;revoke.addEventListener('click',async()=>{if(!await ask(`Revoke agent ${h.name}? Its Docker containers will keep running.`))return;try{await action({action:'revoke',id:h.id});await refresh();}catch(e){showError(e);}});
    const controls=node('div','');controls.className='host-controls';controls.append(name,rename,revoke);row.append(title,status,detail,controls);
    const state=updateStates.get(h.id),op=state?.operation,updateLine=node('p','');updateLine.className='muted small';updateLine.setAttribute('role','status');
    if(op?.id)updateLine.textContent=`Update to ${op.version}: ${op.phase} · ${op.message}${state.unreachable?' · Status temporarily unavailable':''}`;
    else if(!h.capabilities?.includes('agent-update-v1'))updateLine.textContent='Updates from Reef require one-time setup on this host.';
    else if(state&&!state.available)updateLine.textContent='Agent updater unavailable. Check its setup on this host.';
    else if(!result.updates_available)updateLine.textContent='Agent updates require a published stable central Reef build.';
    else if(!state)updateLine.textContent='Checking agent update availability…';
    else if(h.version===result.version)updateLine.textContent='Agent is up to date.';
    else if(!newer(result.version,h.version))updateLine.textContent='No newer matching central version is available.';
    if(updating(op)){const text=node('span',updateLine.textContent),spinner=node('span','');spinner.className='loading-spinner';spinner.setAttribute('aria-hidden','true');updateLine.className='list-loading';updateLine.replaceChildren(spinner,text);}
    const update=node('button',`Update to ${result.version||'central version'}`);update.type='button';update.disabled=!result.updates_available||h.status!=='connected'||!h.capabilities?.includes('agent-update-v1')||!state?.available||updating(op)||!newer(result.version,h.version);
    update.addEventListener('click',async()=>{
     const target=result.version;
     if(!await ask(`Update agent ${h.name} from ${h.version} to ${target}? Its connection will briefly close. Disconnect its consoles and wait for host operations first. Managed containers will keep running.`))return;
     const current=hosts.find(item=>item.id===h.id);
     if(!current||current.status!=='connected'||!current.capabilities?.includes('agent-update-v1')||updating(updateStates.get(h.id)?.operation))return;
     update.disabled=true;
     try{await request('/api/agent-update?'+new URLSearchParams({host:h.id}),{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams({csrf,confirmed:'yes',version:target})});updateStates.set(h.id,{available:true,operation:{id:'pending',version:target,phase:'queued',message:'Update accepted. Waiting for host status.'}});await refresh();}catch(e){showError(e);await refresh();}
    });
    row.append(updateLine,update);return row;
   });if(!(document.activeElement?.tagName==='INPUT'&&find('#hosts-list').contains(document.activeElement)))find('#hosts-list').replaceChildren(...rows);find('#hosts-empty').hidden=rows.length>0;
  }catch(e){showError(e);}finally{busy=false;restoreHost=false;}
 }
 function change(restoring=false){window.reefHost=picker.value;window.reefCapabilities=hosts.find(h=>h.id===picker.value)?.capabilities||[];window.reefHostName=hosts.find(h=>h.id===picker.value)?.name||'Local';window.dispatchEvent(new CustomEvent('reef-host-changed',{detail:{id:picker.value,name:window.reefHostName,restoring}}));}
 picker.addEventListener('change',()=>change());
 find('#open-hosts').addEventListener('click',()=>{find('#hosts-error').hidden=true;dialog.showModal();refresh();});
 function clearToken(){find('#host-enrollment-token').textContent='';find('#host-token-panel').hidden=true;}
 find('#close-hosts').addEventListener('click',()=>dialog.close());dialog.addEventListener('close',clearToken);
 find('#host-enroll').addEventListener('submit',async event=>{event.preventDefault();find('#host-enroll-submit').disabled=true;clearToken();try{const result=await(await action({action:'enroll',name:find('#host-new-name').value})).json();find('#host-enrollment-token').textContent=result.token;find('#host-token-panel').hidden=false;}catch(e){showError(e);}finally{find('#host-enroll-submit').disabled=false;}});
 window.reefHostsReady=refresh();setInterval(refresh,5000);
})();
