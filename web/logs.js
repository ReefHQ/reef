'use strict';
(()=>{
 const dialog=document.querySelector('#container-logs'),picker=document.querySelector('#log-container');
 const output=document.querySelector('#container-log-output'),status=document.querySelector('#container-logs-status');
 const error=document.querySelector('#container-logs-error'),limit=document.querySelector('#container-logs-limit');
 const refresh=document.querySelector('#refresh-container-logs'),toggle=document.querySelector('#toggle-container-logs');
 let timer=null,controller=null,generation=0,automatic=true,lastStatus='',logHost='local';
 function cancel(){clearTimeout(timer);timer=null;controller?.abort();controller=null;generation++;refresh.disabled=false;}
 function setAutomatic(value){automatic=value;toggle.textContent=value?'Pause updates':'Resume updates';toggle.setAttribute('aria-pressed',String(value));if(!value)clearTimeout(timer);}
 async function load(reset=false,background=false){
  cancel();if(!dialog.open||!picker.value)return;
  const token=generation;controller=new AbortController();if(!background)refresh.disabled=true;error.hidden=true;
  if(reset){output.textContent='Loading logs…';limit.hidden=true;status.textContent=automatic?'Loading · updates every 3 seconds':'Loading · automatic updates paused';}
  try{
   const query=new URLSearchParams({id:picker.value,stream:document.querySelector('#log-stream').value,tail:document.querySelector('#log-tail').value});
   const response=await request('/api/container-logs?'+query,{signal:controller.signal},logHost);
   const result=await response.json();if(token!==generation||!dialog.open)return;
   const follow=reset||output.scrollTop+output.clientHeight>=output.scrollHeight-30,scroll=output.scrollTop;
   const text=result.text||'No output in this selection. The application may log to files or the selected stream may be empty.';
   if(output.textContent!==text){output.textContent=text;output.scrollTop=follow?output.scrollHeight:scroll;}
   limit.hidden=!result.truncated;
   lastStatus=`${result.name} · updated ${new Date(result.checked_at).toLocaleTimeString('en-GB')}`;
   status.textContent=`${lastStatus} · ${automatic?'updates every 3 seconds':'automatic updates paused'}`;
  }catch(failure){
   if(token!==generation||failure.name==='AbortError')return;
   error.textContent=failure.message;error.hidden=false;setAutomatic(false);
   status.textContent='Unable to refresh · automatic updates paused';
   if(reset)output.textContent='No logs loaded.';
  }finally{
   if(token===generation){refresh.disabled=false;controller=null;if(dialog.open&&automatic)timer=setTimeout(()=>load(false,true),3000);}
  }
 }
 window.openContainerLogs=({containers,project='',id=''})=>{
  if(containers.some(c=>c.inventory_only))return;
  logHost=containers[0]?.host_id||window.reefHost||'local';
  cancel();picker.replaceChildren(...containers.map(c=>new Option(`${c.compose_service?c.compose_service+' · ':''}${c.name} (${c.state})`,c.id)));
  if(id)picker.value=id;
  document.querySelector('#container-logs-title').textContent=project?`Application logs · ${project} · ${containers[0]?.host_name||window.reefHostName||'Local'}`:`Container logs · ${containers[0]?.host_name||window.reefHostName||'Local'}`;
  setAutomatic(true);lastStatus='';toggle.disabled=!containers.length;error.hidden=true;limit.hidden=true;output.textContent='';
  if(!dialog.open)dialog.showModal();
  if(containers.length)load(true);else{status.textContent='No containers currently exist for this project.';output.textContent='Start the project, then reopen its application logs.';refresh.disabled=true;setAutomatic(false);}
 };
 document.querySelector('#close-container-logs').addEventListener('click',()=>dialog.close());
 dialog.addEventListener('close',cancel);
 for(const selector of ['#log-container','#log-stream','#log-tail'])document.querySelector(selector).addEventListener('change',()=>load(true));
 refresh.addEventListener('click',()=>load());
 toggle.addEventListener('click',()=>{setAutomatic(!automatic);if(automatic)load();else status.textContent=`${lastStatus||'Loading'} · automatic updates paused`;});
 document.addEventListener('visibilitychange',()=>{if(document.hidden)cancel();else if(dialog.open&&automatic)load(false,true);});
})();
