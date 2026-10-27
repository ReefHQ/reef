'use strict';
(()=>{
 const find=s=>document.querySelector(s),dock=find('#console-dock'),tabs=find('#console-dock-tabs'),panes=find('#console-dock-panes');
 const error=find('#console-dock-error'),csrf=find('meta[name="csrf-token"]').content;
 const entries=new Map(),limit=Number(document.body.dataset.consoleSessionLimit)||8;
 let selected='',sequence=0,height=380,collapsed=false,maximized=false,transport=null,drag=null;
 function post(path,fields={},signal,host='local'){return fetch('/api/console/'+path+(host!=='local'?'?host='+encodeURIComponent(host):''),{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams({...fields,csrf}),signal}).then(async response=>{if(response.status===401)throw Error('Sign-in expired. Reload Reef to sign in again.');if(!response.ok)throw Error(await response.text());return response;});}
 function tell(entry,message){entry.frame.contentWindow?.postMessage({type:'reef-console-host',channel:entry.key,...message},location.origin);}
 function count(){const active=[...entries.values()].filter(e=>['connected','connecting'].includes(e.phase)).length;find('#console-dock-count').textContent=`${active} active / ${limit} · ${entries.size} tab${entries.size===1?'':'s'}`;}
 function phase(entry,value){entry.phase=value;entry.tab.dataset.state=value;entry.tab.setAttribute('aria-label',`${entry.label} — ${value}`);entry.state.textContent=value;count();}
 function size(value){height=Math.max(220,Math.min(value,Math.max(220,window.innerHeight-185)));dock.style.height=height+'px';find('#console-dock-resize').setAttribute('aria-valuenow',String(Math.round(height)));find('#console-dock-resize').setAttribute('aria-valuemax',String(Math.max(220,window.innerHeight-185)));}
 function layout(){dock.hidden=entries.size===0;dock.classList.toggle('is-collapsed',collapsed);document.body.classList.toggle('console-maximized',maximized&&entries.size>0&&!collapsed);find('#console-dock-minimize').textContent=collapsed?'Expand':'Reduce';find('#console-dock-minimize').setAttribute('aria-expanded',String(!collapsed));find('#console-dock-maximize').textContent=maximized?'Restore':'Maximize';find('#console-dock-maximize').setAttribute('aria-pressed',String(maximized));for(const entry of entries.values())tell(entry,{action:'activate',active:entry.key===selected&&!collapsed});}
 function select(key,focus=false){
  selected=key;for(const entry of entries.values()){const active=entry.key===key;entry.pane.hidden=!active;entry.tab.setAttribute('aria-selected',String(active));entry.tab.tabIndex=active?0:-1;}
  const entry=entries.get(key);find('#console-dock-active').textContent=entry?entry.label:'Console';find('#console-dock-active').title=entry?.label||'';layout();if(entry&&!collapsed)entry.tab.scrollIntoView({block:'nearest',inline:'nearest'});if(focus)entry?.tab.focus();
 }
 function rejectTransport(current,failure){
  if(transport!==current)return;transport=null;current.controller.abort();current.reject(failure);
  for(const entry of entries.values()){if(['connected','connecting'].includes(entry.phase)){tell(entry,{frame:{type:'closed',message:failure.message}});phase(entry,'disconnected');entry.console='';}}
 }
 async function ensureTransport(){
  if(transport)return transport.ready;
  const current={id:'',controller:new AbortController()};current.ready=new Promise((resolve,reject)=>{current.resolve=resolve;current.reject=reject;});transport=current;
  (async()=>{try{
   const response=await post('stream',{},current.controller.signal),reader=response.body.getReader(),decoder=new TextDecoder();let pending='';
   for(;;){const {value,done}=await reader.read();if(transport!==current)return;if(done)throw Error('Console connection ended. Reconnect explicitly to open new shells.');pending+=decoder.decode(value,{stream:true});let newline;
    while((newline=pending.indexOf('\n'))!==-1){const line=pending.slice(0,newline);pending=pending.slice(newline+1);if(!line)continue;const frame=JSON.parse(line);
     if(frame.type==='transport'){current.id=frame.id;current.resolve(current);continue;}
     const entry=entries.get(frame.client);if(!entry)continue;
     if(frame.type==='ready'){entry.console=frame.console;phase(entry,'connected');}else if(frame.type==='closed'){entry.console='';phase(entry,'disconnected');}
     tell(entry,{frame});
    }
    if(pending.length>65536)throw Error('Invalid console response.');
   }
  }catch(failure){rejectTransport(current,failure);}})();
  return current.ready;
 }
 function close(key){
  const entry=entries.get(key);if(!entry)return;entry.controller?.abort();
  if(transport?.id)post('close',{console:entry.console,transport:transport.id,client:key}).catch(()=>{});
  entries.delete(key);entry.tabGroup.remove();entry.pane.remove();
  if(selected===key)selected=[...entries.keys()].at(-1)||'';select(selected);count();
  if(!entries.size){const current=transport;transport=null;current?.controller.abort();current?.reject(Error('Console panel closed.'));maximized=false;collapsed=false;layout();}
 }
 window.openContainerConsole=c=>{
  if(c.state!=='running'||c.inventory_only)return;
  collapsed=false;layout();
  if(entries.size>=limit){error.textContent=`${limit} console tabs are open. Close a tab before opening another.`;error.hidden=false;return;}
  error.hidden=true;const key='console-'+(++sequence),label=`${sequence} · ${c.name}${c.compose_project?' · '+c.compose_project:''} · ${c.host_name||'Local'}`;
  const tabGroup=document.createElement('div');tabGroup.className='console-dock-tab';tabGroup.setAttribute('role','presentation');
  const tab=document.createElement('button');tab.type='button';tab.id=key+'-tab';tab.setAttribute('role','tab');tab.setAttribute('aria-controls',key+'-pane');tab.title=label;
  const name=document.createElement('span');name.textContent=`${sequence} · ${c.name}`;const state=document.createElement('span');state.className='console-tab-state';state.textContent='not connected';tab.append(name,state);tab.setAttribute('aria-label',`${label} — not connected`);
  const remove=document.createElement('button');remove.type='button';remove.textContent='×';remove.setAttribute('aria-label',`Close and disconnect ${label}`);tabGroup.append(tab,remove);
  const pane=document.createElement('div');pane.className='console-dock-pane';pane.id=key+'-pane';pane.setAttribute('role','tabpanel');pane.setAttribute('aria-labelledby',tab.id);
  const frame=document.createElement('iframe');frame.classList.add('is-loading');frame.title=`Terminal for ${label}`;frame.src='/console?'+new URLSearchParams({id:c.id,embedded:'1',channel:key,host:c.host_id||'local'});pane.append(frame);
  const entry={key,c,label,tabGroup,tab,state,pane,frame,phase:'idle',console:'',controller:null};entries.set(key,entry);tabs.append(tabGroup);panes.append(pane);size(height);select(key);count();
  tab.addEventListener('click',()=>{collapsed=false;select(key);});remove.addEventListener('click',()=>close(key));
  tab.addEventListener('keydown',event=>{const keys=[...entries.keys()],index=keys.indexOf(key);let target;if(event.key==='ArrowRight')target=keys[(index+1)%keys.length];if(event.key==='ArrowLeft')target=keys[(index+keys.length-1)%keys.length];if(event.key==='Home')target=keys[0];if(event.key==='End')target=keys.at(-1);if(target){event.preventDefault();select(target,true);}if(event.key==='Delete'){event.preventDefault();close(key);}});
  frame.addEventListener('load',()=>{frame.classList.remove('is-loading');tell(entry,{action:'activate',active:selected===key&&!collapsed});});
 };
 window.addEventListener('message',async event=>{
  if(event.origin!==location.origin||event.data?.type!=='reef-console-frame')return;
  const entry=entries.get(event.data.channel);if(!entry||event.source!==entry.frame.contentWindow)return;
  const {request,action,fields={}}=event.data;const reply=(ok,message='')=>tell(entry,{request,ok,message});
  try{
   if(action==='loaded'){tell(entry,{action:'activate',active:entry.key===selected&&!collapsed});return;}
   if(action==='open'){
    phase(entry,'connecting');entry.openRequest=request;const opening=new AbortController();entry.controller=opening;const current=await ensureTransport();if(!entries.has(entry.key)||entry.controller!==opening||opening.signal.aborted)return;
    await post('open',{id:entry.c.id,shell:fields.shell,cols:fields.cols,rows:fields.rows,confirmed:'yes',transport:current.id,client:entry.key},opening.signal,entry.c.host_id||'local');reply(true);return;
   }
   if(action==='close'){entry.openRequest=null;entry.controller?.abort();if(transport?.id)await post('close',{console:entry.console,transport:transport.id,client:entry.key});entry.console='';phase(entry,'disconnected');reply(true);return;}
   if(!entry.console)throw Error('Console is not connected.');
   if(action==='input')await post('input',{console:entry.console,data:fields.data});
   else if(action==='resize')await post('resize',{console:entry.console,cols:fields.cols,rows:fields.rows});
   else return;reply(true);
  }catch(failure){if(!entries.has(entry.key))return;if(action==='open'&&entry.openRequest===request){entry.console='';phase(entry,'disconnected');}reply(false,failure.message);}
 });
 find('#console-dock-new').addEventListener('click',()=>{const entry=entries.get(selected);if(entry)window.openContainerConsole(entry.c);});
 find('#console-dock-disconnect').addEventListener('click',()=>{for(const entry of entries.values())tell(entry,{action:'disconnect'});});
 find('#console-dock-minimize').addEventListener('click',()=>{collapsed=!collapsed;if(collapsed)maximized=false;layout();});
 find('#console-dock-maximize').addEventListener('click',()=>{collapsed=false;maximized=!maximized;layout();});
 const handle=find('#console-dock-resize');handle.addEventListener('pointerdown',event=>{if(collapsed||maximized)return;drag={y:event.clientY,height};handle.setPointerCapture(event.pointerId);dock.classList.add('is-resizing');event.preventDefault();});
 handle.addEventListener('pointermove',event=>{if(drag)size(drag.height+drag.y-event.clientY);});for(const name of ['pointerup','pointercancel'])handle.addEventListener(name,()=>{drag=null;dock.classList.remove('is-resizing');});
 handle.addEventListener('keydown',event=>{if(event.key==='ArrowUp'||event.key==='ArrowDown'){event.preventDefault();size(height+(event.key==='ArrowUp'?40:-40));}if(event.key==='Home'){event.preventDefault();size(220);}if(event.key==='End'){event.preventDefault();size(window.innerHeight);}});
 window.addEventListener('resize',()=>size(height));window.addEventListener('pagehide',()=>{for(const entry of entries.values())if(entry.console)navigator.sendBeacon('/api/console/close',new URLSearchParams({csrf,console:entry.console}));transport?.controller.abort();});
})();
