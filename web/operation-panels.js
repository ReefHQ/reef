'use strict';
// Only dismissal IDs are stored; operation output stays in memory.
(()=>{
 const fallback=new Map();
 const key=(kind,host)=>`reef.operation-dismissal.v1.${kind}.${host}`;
 function dismissed(kind,host,id){
  const name=key(kind,host);
  if(fallback.has(name))return fallback.get(name)===id;
  try{return localStorage.getItem(name)===id;}catch{return false;}
 }
 function dismiss(kind,host,id){
  const name=key(kind,host);
  try{localStorage.setItem(name,id);fallback.delete(name);}catch{fallback.set(name,id);}
 }
 function node(tag,text,className=''){const value=document.createElement(tag);value.textContent=text;value.className=className;return value;}
 function render(panel,op,{kind,host,summary,result}){
  const id=op?.id||'',running=!!op?.running,results=op?.results||[];
  const issues=!!op?.skipped||results.some(row=>!row.ok||row.unprocessed)||(!running&&op?.total>0&&results.length<op.total);
  const scrollTop=panel.dataset.operationId===id&&panel.dataset.operationHost===host?(panel.scrollTop||0):0;
  panel.dataset.operationId=id;panel.dataset.operationHost=host;panel.dataset.operationKind=kind;panel.dataset.operationRunning=String(running);
  panel.hidden=!id||(!running&&(!issues||dismissed(kind,host,id)));
  panel.replaceChildren();if(panel.hidden)return;
  const heading=node('div','','catalog-operation-heading');heading.append(node('strong',summary));
  if(!running){
   const close=node('button','Dismiss','quiet');close.type='button';close.setAttribute('aria-label','Dismiss completed operation result');
   close.addEventListener('click',()=>{
    if(panel.dataset.operationId!==id||panel.dataset.operationHost!==host||panel.dataset.operationKind!==kind||panel.dataset.operationRunning!=='false')return;
    dismiss(kind,host,id);panel.hidden=true;panel.replaceChildren();
   });
   heading.append(close);
  }
  panel.append(heading);
  for(const row of results)panel.append(node('p',result(row),row.ok?'small':'small error-text'));
  panel.scrollTop=scrollTop;
 }
 window.ReefOperationPanels={render};
})();
