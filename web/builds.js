'use strict';
(()=>{
 const find=id=>document.getElementById(id),view=find('builds-view');
 let host=window.reefHost||'local',epoch=0,projects=[],selected='',detail=null,history=[],cache=[],cacheSelection=new Set(),cacheBusy=false,cacheLoading=false,cacheLoaded=false,stateLoading=false,stateLoaded=false,stateFailed=false,detailLoading=false,submitting=false,logID='',logProject='',timer,hostsReady=false;
 const available=()=>host==='local'||window.reefCapabilities?.includes('builds-v1');
 const active=()=>projects.find(p=>p.id===selected);
 const bytes=n=>{const units=['B','KiB','MiB','GiB','TiB'];let i=0;while(n>=1024&&i<4){n/=1024;i++;}return `${n.toFixed(i?1:0)} ${units[i]}`;};
 function notice(text,error=false){find('builds-error').textContent=text;find('builds-error').hidden=!text;find('builds-error').className='notice'+(error?' error':'');}
 function controls(){
  const p=active();find('builds-refresh').disabled=!available()||stateLoading;find('build-submit').disabled=!available()||!p||p.busy||!detail?.services?.length||detailLoading||submitting||cacheBusy;
  find('builds-loading').hidden=!(stateLoading||detailLoading||(!stateLoaded&&!stateFailed&&available()));find('builds-rows').setAttribute('aria-busy',String(stateLoading));
  find('build-cache-refresh').disabled=!available()||cacheLoading;find('build-cache-delete').disabled=!available()||cacheBusy||!cacheLoaded||!cacheSelection.size||projects.some(p=>p.busy);
  find('build-cache-cleanup').disabled=!available()||cacheBusy||!cacheLoaded||!cache.some(row=>row.reclaimable&&!row.mutable)||projects.some(p=>p.busy);
  find('build-cache-loading').hidden=!cacheLoading;find('build-cache-rows').setAttribute('aria-busy',String(cacheLoading));
 }
 function renderProjects(){
  const picker=find('build-project'),value=selected,signature=JSON.stringify(projects.map(p=>[p.id,p.name]));if(picker.dataset.signature!==signature){picker.replaceChildren(new Option('Choose a managed project',''),...projects.map(p=>new Option(p.name,p.id)));picker.dataset.signature=signature;}picker.value=projects.some(p=>p.id===value)?value:'';
  find('build-nav-count').textContent=available()&&stateLoaded?projects.length:'—';
  find('builds-empty').hidden=!!projects.length;find('builds-empty').textContent=available()?'Manage a Compose project first, then select it here.':'Upgrade this host’s agent to use Builds.';
  const rows=history.map(op=>{const tr=document.createElement('tr'),actions=document.createElement('td'),open=textNode('button','View log','compact-button');open.type='button';open.dataset.focusKey='build-log-'+op.id;open.addEventListener('click',()=>showLog(op.id));actions.append(open);tr.append(textNode('td',op.build?.service||'All build services'),textNode('td',op.running?'Running':op.phase),textNode('td',new Date(op.started).toLocaleString()),textNode('td',op.result||op.phase),actions);return tr;});replaceKeepingFocus(find('builds-rows'),rows);
  find('build-history-empty').hidden=!!history.length;find('build-history-empty').textContent=selected?'No builds recorded for this project.':'Select a managed project to see its builds.';
  find('build-project-state').textContent=active()?.busy?'A project operation is running.':detailLoading?'Reading saved Compose build services…':detail?`${detail.services.length} buildable service(s) in the saved Compose files.`:'';controls();
 }
 async function loadState(){
  if(!hostsReady||stateLoading||document.hidden)return;if(!available()){notice('Upgrade this host’s agent to use Builds.',true);window.reefBuildsReady=true;window.dispatchEvent(new CustomEvent('reef-builds-ready'));return;}const current=epoch;stateLoading=true;stateFailed=false;controls();
  try{const data=await(await request('/api/builds',undefined,host)).json();if(current!==epoch)return;const wasBusy=active()?.busy;projects=data;stateLoaded=true;if(selected&&!active()){selected='';detail=null;history=[];}const op=active()?.operation;if(op?.id){const index=history.findIndex(h=>h.id===op.id);if(index<0)history.unshift(op);else history[index]=op;}renderProjects();if(wasBusy&&!active()?.busy){await loadHistory();await loadDetail();}if(active()?.persistence_error)notice(active().persistence_error,true);
  }catch(error){if(current===epoch){stateFailed=true;notice(error.message,true);}}finally{if(current===epoch){stateLoading=false;controls();window.reefBuildsReady=true;window.dispatchEvent(new CustomEvent('reef-builds-ready'));}}
 }
 async function loadDetail(){
  const current=epoch,id=selected;if(!id||active()?.busy||detailLoading)return;detailLoading=true;detail=null;controls();
  try{const data=await(await request('/api/builds/project?id='+encodeURIComponent(id),undefined,host)).json();if(current!==epoch||id!==selected)return;detail=data;find('build-service').replaceChildren(new Option('All build services',''),...data.services.map(s=>new Option(s,s)));notice('');}
  catch(error){if(current===epoch&&id===selected)notice(error.message,true);}finally{if(current===epoch&&id===selected){detailLoading=false;renderProjects();}}
 }
 async function loadHistory(){const current=epoch,id=selected;if(!id)return;try{const data=await(await request('/api/builds/history?id='+encodeURIComponent(id),undefined,host)).json();if(current!==epoch||id!==selected)return;history=data;renderProjects();}catch(error){if(current===epoch&&id===selected)notice(error.message,true);}}
 async function logRefresh(){
  const current=epoch,id=logProject,op=logID;if(!find('build-log-dialog').open||!op)return;
  try{const data=await(await request('/api/builds/log?id='+encodeURIComponent(id)+'&operation='+encodeURIComponent(op),undefined,host)).json();if(current!==epoch||op!==logID||!find('build-log-dialog').open)return;const output=find('build-log-output'),atBottom=output.scrollHeight-output.scrollTop-output.clientHeight<35;output.textContent=data.log||'Waiting for build output…';if(atBottom)output.scrollTop=output.scrollHeight;find('build-log-state').textContent=data.running?`Running · ${data.phase}`:data.result||data.phase;}
  catch(error){if(current===epoch&&op===logID)find('build-log-state').textContent=error.message;}
 }
 async function showLog(op){logID=op;logProject=selected;find('build-log-output').textContent='Loading build log…';find('build-log-dialog').showModal();await logRefresh();}
 function cacheStatus(op){cacheBusy=!!op?.running;const panel=find('build-cache-operation');panel.hidden=!op?.id;if(op?.id){panel.replaceChildren(textNode('strong',`${op.action}: ${op.running?'running':op.skipped?'stopped':'finished'} · ${op.completed||0} / ${op.total||0} reviewed`));for(const result of op.results||[])panel.append(textNode('p',`${result.id}: ${result.message}`,result.ok?'small':'small error-text'));}controls();}
 function renderCache(){
  const query=find('build-cache-search').value.toLowerCase().trim(),rows=cache.filter(row=>`${row.id} ${row.type}`.toLowerCase().includes(query));
  find('build-cache-summary').textContent=cacheLoaded?`${cache.length} cache records · ${bytes(cache.reduce((n,r)=>n+r.size,0))} cumulative size; shared layers are not an estimate of freed space.`:'';
  const nodes=rows.map(row=>{const tr=document.createElement('tr'),selection=document.createElement('td'),check=document.createElement('input');check.type='checkbox';check.checked=cacheSelection.has(row.id);check.disabled=cacheBusy||!row.reclaimable||row.mutable||!cacheLoaded;check.setAttribute('aria-label',`Select cache ${row.id}`);check.dataset.focusKey='cache-'+row.id;check.addEventListener('change',()=>{if(check.checked)cacheSelection.add(row.id);else cacheSelection.delete(row.id);controls();});selection.append(check);tr.append(selection,textNode('td',row.id),textNode('td',row.type),textNode('td',bytes(row.size)),textNode('td',row.last_used||row.created||'Unknown'),textNode('td',!row.reclaimable?'In use':row.mutable?'Mutable · retained':row.shared?'Reclaimable · shared':'Reclaimable'));return tr;});replaceKeepingFocus(find('build-cache-rows'),nodes);find('build-cache-empty').hidden=!!rows.length;find('build-cache-empty').textContent=cacheLoaded?'No cache records match this selection.':'Use Refresh cache to read the default Docker builder.';controls();
 }
 async function loadCache(){
  if(cacheLoading||!available())return;const current=epoch;cacheLoading=true;controls();
  try{const data=await(await request('/api/builds/cache',undefined,host)).json();if(current!==epoch)return;const previous=new Map(cache.map(row=>[row.id,row.fingerprint]));cache=data.records;cacheLoaded=true;cacheSelection=new Set([...cacheSelection].filter(id=>cache.some(row=>row.id===id&&row.fingerprint===previous.get(id)&&row.reclaimable&&!row.mutable)));find('build-cache-error').hidden=true;cacheStatus(data.operation);renderCache();}
  catch(error){if(current===epoch){find('build-cache-error').textContent=error.message;find('build-cache-error').hidden=false;cacheLoaded=false;controls();}}
  finally{if(current===epoch){cacheLoading=false;controls();}}
 }
 async function cleanup(targets){
  const current=epoch,chosenHost=host;if(!targets.length||cacheBusy||!cacheLoaded||!available())return;
  if(!await ask(`Remove ${targets.length} selected cache records from the default builder on ${window.reefHostName||'Local'}? Future builds may take longer. Images, containers and volumes are not deleted.\n\n${targets.map(r=>r.id).join('\n')}`))return;
  if(current!==epoch||!available()||cacheBusy||!cacheLoaded)return;cacheBusy=true;controls();
  try{const body=new URLSearchParams({csrf,confirmed:'yes',targets:JSON.stringify(targets.map(row=>({id:row.id,fingerprint:row.fingerprint})))});const op=await(await request('/api/builds/cache/action',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body},chosenHost)).json();if(current!==epoch)return;cacheStatus(op);cacheSelection.clear();renderCache();}
  catch(error){if(current===epoch){find('build-cache-error').textContent=`${error.message} Refresh cache before retrying; outcome may be unknown.`;find('build-cache-error').hidden=false;}}
 }
 find('build-project').addEventListener('change',async()=>{selected=find('build-project').value;detail=null;detailLoading=false;history=[];renderProjects();await Promise.all([loadDetail(),loadHistory()]);});
 find('build-form').addEventListener('submit',async event=>{
  event.preventDefault();const current=epoch,id=selected,chosenHost=host,info=detail,service=find('build-service').value,noCache=find('build-no-cache').checked,pull=find('build-pull').checked;if(!available()||!info||!active()||active().busy||submitting)return;
  if(!await ask(`Build ${service||'all build services'} for ${active().name} on ${window.reefHostName||'Local'} from the saved Compose files? ${noCache?'Cache reuse disabled. ':''}${pull?'Base images will be refreshed. ':''}Containers will keep running. Unsaved editor changes are not included.`))return;
  if(current!==epoch||id!==selected||!available()||active()?.busy)return;submitting=true;controls();
  try{const body=new URLSearchParams({csrf,confirmed:'yes',id,revision:info.revision,service,no_cache:noCache?'yes':'no',pull:pull?'yes':'no'});await request('/api/builds/action',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body},chosenHost);if(current!==epoch)return;active().busy=true;notice('Build accepted. It continues if you close this page.');await loadState();}
  catch(error){if(current===epoch)notice(`${error.message} Refresh status before retrying; outcome may be unknown.`,true);}finally{if(current===epoch){submitting=false;controls();}}
 });
 find('builds-refresh').addEventListener('click',async()=>{await loadState();await Promise.all([loadDetail(),loadHistory()]);});
 find('build-cache-refresh').addEventListener('click',loadCache);find('build-cache-search').addEventListener('input',renderCache);
 find('build-cache-delete').addEventListener('click',()=>cleanup(cache.filter(row=>cacheSelection.has(row.id))));find('build-cache-cleanup').addEventListener('click',()=>cleanup(cache.filter(row=>row.reclaimable&&!row.mutable)));
 find('build-log-close').addEventListener('click',()=>find('build-log-dialog').close());find('build-log-dialog').addEventListener('close',()=>{logID='';logProject='';});
 async function poll(){clearTimeout(timer);const current=epoch;try{if(available()&&!document.hidden&&!view.hidden){await loadState();await logRefresh();{const op=await(await request('/api/builds/cache/operation',undefined,host)).json();if(current!==epoch)return;const wasBusy=cacheBusy;cacheStatus(op);if(wasBusy&&!cacheBusy)await loadCache();}}}catch(error){if(current===epoch)notice(error.message,true);}finally{if(current===epoch)timer=setTimeout(poll,2000);}}
 window.addEventListener('reef-state',event=>{if(view.hidden)find('build-nav-count').textContent=available()?event.detail.length:'—';});
 window.addEventListener('reef-view-changed',()=>{if(!view.hidden){if(!available()){notice('Upgrade this host’s agent to use Builds.',true);window.reefBuildsReady=true;window.dispatchEvent(new CustomEvent('reef-builds-ready'));}else loadState();}});
 window.addEventListener('reef-host-changed',()=>{epoch++;host=window.reefHost||'local';projects=[];selected='';history=[];detail=null;cache=[];cacheSelection.clear();cacheBusy=false;cacheLoading=false;cacheLoaded=false;stateLoading=false;stateLoaded=false;stateFailed=false;detailLoading=false;submitting=false;logID='';window.reefBuildsReady=false;if(find('build-log-dialog').open)find('build-log-dialog').close();find('build-cache-operation').hidden=true;find('build-cache-error').hidden=true;find('build-no-cache').checked=false;find('build-pull').checked=false;find('build-cache-search').value='';notice('');renderProjects();renderCache();if(!view.hidden)loadState();});
 renderProjects();renderCache();(window.reefHostsReady||Promise.resolve()).then(()=>{hostsReady=true;if(!view.hidden)loadState();timer=setTimeout(poll,2000);});
})();
