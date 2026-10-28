'use strict';
(()=>{
 const find=id=>document.getElementById(id),view=find('alerts-view');
 const fields={disconnect_seconds:'alerts-disconnect',unhealthy_seconds:'alerts-unhealthy',restart_count:'alerts-restarts',restart_window_seconds:'alerts-window',recovery_seconds:'alerts-recovery'};
 const kind={disconnected:'Agent disconnected',unhealthy:'Unhealthy container',restart_loop:'Restart loop'};
 const coverage={ready:'Collecting',waiting:'Waiting for first sample',disabled:'Disabled',maintenance:'Maintenance · observations suspended',disconnected:'Agent disconnected',upgrade_required:'Upgrade agent to v0.16.0 for container monitoring',collection_unavailable:'Collection unavailable · outcome unknown',partial:'Some containers could not be inspected'};
 let data=null,host=window.reefHost||'local',epoch=0,controller=null,timer,loading=false,saving=false,policyLoaded=false,offset=0,hostsReady=false,wasVisible=!view.hidden;
 const node=(tag,text,cls='')=>{const e=document.createElement(tag);e.textContent=text;e.className=cls;return e;};
 const date=value=>value&&!value.startsWith('0001')?new Date(value).toLocaleString('en-GB'):'Not collected';
 const selected=row=>find('alerts-scope').value==='all'||row.host_id===host;
 function error(message){find('alerts-error').textContent=message;find('alerts-error').hidden=!message;}
 function ready(){window.reefAlertsReady=true;window.dispatchEvent(new CustomEvent('reef-alerts-ready'));}
 function filters(){const saved=window.ReefNavigation?.read();if(saved?.view==='alerts'&&saved.host===host){for(const id of ['alerts-search','alerts-scope','alerts-result']){const f=find(id),v=saved.filters[id]||'';if(f.tagName!=='SELECT'||[...f.options].some(o=>o.value===v))f.value=v;}}}
 filters();
 function controls(){
  find('alerts-refresh').disabled=loading||saving;find('alerts-save').disabled=!policyLoaded||saving;
  for(const id of ['alerts-enabled',...Object.values(fields)])find(id).disabled=!policyLoaded||saving;
  find('alerts-loading').hidden=!loading;find('alerts-loading-text').textContent=data?'Refreshing monitoring state…':'Loading monitoring state…';find('alerts-rows').setAttribute('aria-busy',String(loading));
 }
 function policy(c){find('alerts-enabled').checked=c.enabled;for(const [key,id]of Object.entries(fields))find(id).value=String(c[key]);policyLoaded=true;}
 function render(){
  controls();if(!data)return;
  find('alerts-policy-summary').textContent=`Monitoring policy · ${data.config.enabled?'enabled':'disabled'}`;
  find('alerts-status').textContent=`${data.config.enabled?'Monitoring enabled':'Monitoring disabled'} · ${data.pending} event(s) awaiting central acceptance · Last refreshed ${date(data.checked_at)}`;
  find('alerts-warning').hidden=!data.persistence_error&&!data.capacity_limited;find('alerts-warning').textContent=data.capacity_limited?'Monitoring capacity reached. Further transitions are delayed until the queue or incident capacity becomes available.':'Monitoring state could not be persisted. Incident transitions may be delayed; inspect private state before retrying.';
  const hosts=data.hosts.filter(selected),hostRows=hosts.map(h=>{const tr=document.createElement('tr'),state=node('td',coverage[h.status]||h.status);if(h.hold_until&&!h.hold_until.startsWith('0001')&&new Date(h.hold_until)>new Date())state.append(node('small',`Until ${date(h.hold_until)}`,'muted'));tr.append(node('td',h.host),state,node('td',date(h.checked_at)),node('td',`${h.containers} observed${h.failed?` · ${h.failed} unavailable`:''}`));return tr;});
  replaceKeepingFocus(find('alerts-host-rows'),hostRows);find('alerts-host-empty').hidden=!!hosts.length;find('alerts-host-empty').textContent=data.config.enabled?'Waiting for observations on this host. Containers require a compatible agent.':'Enable monitoring in the policy above to begin collecting.';
  const search=find('alerts-search').value.trim().toLowerCase(),state=find('alerts-result').value;
  const rows=data.incidents.filter(r=>selected(r)&&(!state||r.status===state)&&(!search||`${r.host} ${r.target} ${kind[r.kind]||r.kind}`.toLowerCase().includes(search)));
  if(offset>=rows.length)offset=0;
  const nodes=rows.slice(offset,offset+50).map(r=>{const tr=document.createElement('tr'),target=node('td',r.host),status=document.createElement('td'),times=node('td',date(r.opened));target.append(node('small',r.target,'muted'));status.append(node('span',r.status==='firing'?'Firing':r.status==='resolved'?'Resolved':'Retired','tag '+(r.status==='firing'?'failed':'neutral-tag')));if(r.note)status.append(node('small',r.note,'muted'));times.append(node('small',`Last change: ${date(r.updated)}`,'muted'));tr.append(target,node('td',kind[r.kind]||r.kind),status,times);return tr;});
  replaceKeepingFocus(find('alerts-rows'),nodes);find('alerts-summary').textContent=`${rows.length} incidents matching · ${rows.length?offset+1:0}–${Math.min(offset+50,rows.length)} shown`;
  find('alerts-empty').hidden=!!rows.length;find('alerts-empty').textContent='No recorded incidents match these filters.';find('alerts-previous').disabled=!offset;find('alerts-next').disabled=offset+50>=rows.length;
 }
 function stop(){epoch++;controller?.abort();controller=null;clearTimeout(timer);loading=false;}
 async function load(){
  if(!hostsReady||view.hidden||document.hidden||loading||saving)return;clearTimeout(timer);loading=true;controls();const ticket=epoch;controller=new AbortController();
  try{const value=await(await request('/api/monitoring',{signal:controller.signal},'local')).json();if(ticket!==epoch)return;data=value;if(!policyLoaded)policy(value.config);error('');render();}
  catch(e){if(ticket===epoch&&e.name!=='AbortError')error(`${e.message}${data?' Previous results may be stale.':''}`);}
  finally{if(ticket===epoch){loading=false;controller=null;controls();ready();if(!view.hidden&&!document.hidden)timer=setTimeout(load,5000);}}
 }
 find('alerts-refresh').addEventListener('click',()=>{stop();load();});
 for(const [id,event] of [['alerts-search','input'],['alerts-scope','change'],['alerts-result','change']])find(id).addEventListener(event,()=>{offset=0;window.dispatchEvent(new CustomEvent('reef-alerts-interaction'));render();});
 find('alerts-previous').addEventListener('click',()=>{offset=Math.max(0,offset-50);render();});find('alerts-next').addEventListener('click',()=>{offset+=50;render();});
 find('alerts-form').addEventListener('submit',async event=>{
  event.preventDefault();if(saving||!policyLoaded)return;const ticket=epoch,body=new URLSearchParams({csrf,confirmed:'yes',enabled:String(find('alerts-enabled').checked)});for(const [key,id]of Object.entries(fields))body.set(key,find(id).value);
  if(!await ask('Save this monitoring policy for every host? Current incidents will be retired and observation timers reset. Docker resources will not be changed.'))return;
  if(ticket!==epoch||view.hidden||saving)return;stop();const saveEpoch=epoch;saving=true;controls();error('');
  try{const value=await(await request('/api/monitoring',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body},'local')).json();if(saveEpoch===epoch){data=value;policy(value.config);render();}}
  catch(e){if(saveEpoch===epoch)error(e.message);}
  finally{saving=false;controls();if(!view.hidden&&!document.hidden)load();}
 });
 window.addEventListener('reef-view-changed',()=>{const visible=!view.hidden,entered=visible&&!wasVisible;wasVisible=visible;if(!visible){stop();controls();}else if(entered)load();});
 window.addEventListener('reef-host-changed',event=>{stop();host=window.reefHost||'local';offset=0;for(const id of ['alerts-search','alerts-scope','alerts-result'])find(id).value='';if(event.detail?.restoring)filters();render();load();});
 document.addEventListener('visibilitychange',()=>{if(document.hidden){stop();controls();}else load();});
 controls();(window.reefHostsReady||Promise.resolve()).then(()=>{hostsReady=true;load();});
})();
