'use strict';
(()=>{
 const find=s=>document.querySelector(s),dialog=find('#container-details');
 let selected='',rows=[],signature='';
 const node=(tag,text,className)=>{const el=document.createElement(tag);el.textContent=text;if(className)el.className=className;return el;};
 function actions(c){
  const group=document.createElement('div');group.className='container-log-actions';
  const logs=node('button','Logs','compact-button');logs.type='button';logs.dataset.focusKey='container-logs-'+c.id;logs.setAttribute('aria-label',`Logs for ${c.name}`);
  logs.addEventListener('click',()=>{if(dialog.open)dialog.close();window.openContainerLogs({containers:[c],id:c.id});});group.append(logs);
  if(c.state==='running'){
   const console=node('button','Console','compact-button');console.type='button';console.dataset.focusKey='container-console-'+c.id;console.setAttribute('aria-label',`Console for ${c.name}`);console.addEventListener('click',()=>{if(dialog.open)dialog.close();window.openContainerConsole(c);});group.append(console);
  }else{const console=node('button','Console','compact-button');console.type='button';console.disabled=true;console.title='Console access requires a running container';group.append(console);}
  return group;
 }
 function render(c){
  find('#container-detail-error').hidden=!!c;
  if(!c){find('#container-detail-actions').replaceChildren();signature='';return;}
  const next=JSON.stringify(c);if(signature===next)return;signature=next;
  find('#container-detail-title').textContent=c.name;
  const fields=[];
  for(const [label,value] of [['Container ID',c.id],['Image',c.image],['State',c.state],['Status',c.status],['Compose project',c.compose_project||'—'],['Service',c.compose_service||'—']])fields.push(node('dt',label),node('dd',value));
  find('#container-detail-fields').replaceChildren(...fields);find('#container-detail-actions').replaceChildren(actions(c));
 }
 function open(c){selected=c.id;signature='';render(c);if(!dialog.open)dialog.showModal();}
 function name(c){const button=node('button',c.name,'text-button container-name-button');button.type='button';button.dataset.focusKey='container-detail-'+c.id;button.setAttribute('aria-label',`Details for ${c.name}`);button.addEventListener('click',()=>open(rows.find(row=>row.id===c.id)||c));return button;}
 function state(c){const field=document.createElement('td');field.append(node('span',c.state,'tag '+(c.state==='running'?'running-state':'neutral-tag')),node('small',c.status,'muted'));return field;}
 window.ReefContainers={actions,name,state,open,update(snapshot){rows=snapshot;if(dialog.open&&selected)render(rows.find(c=>c.id===selected));}};
 find('#close-container-details').addEventListener('click',()=>dialog.close());dialog.addEventListener('close',()=>{selected='';signature='';});
})();
