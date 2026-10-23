'use strict';
// Shared, side-effect-free view models; the browser and Node tests use the same rules.
(function(root){
 function filterContainers(rows,query={}) {
  const search=(query.search||'').trim().toLowerCase();
  return rows.filter(c=>(!search||`${c.name} ${c.image} ${c.compose_project||''}`.toLowerCase().includes(search))&&(!query.state||c.state===query.state)&&(!query.project||(query.project==='__none__'?!c.compose_project:c.compose_project===query.project))&&(!query.access||(query.access==='managed'?!!c.managed_project_id:!c.managed_project_id)));
 }
 function projectRows(containers,managed) {
  const groups=new Map();
  for(const c of containers){if(!c.compose_project)continue;let g=groups.get(c.compose_project);if(!g){g={name:c.compose_project,containers:[],self:false,managed:null};groups.set(g.name,g);}g.containers.push(c);g.self=g.self||!!c.is_reef;}
  for(const p of managed){let g=groups.get(p.project_name);if(!g){g={name:p.project_name,containers:[],self:false,managed:null};groups.set(g.name,g);}g.managed=p;}
  return [...groups.values()].sort((a,b)=>Number(!!b.managed)-Number(!!a.managed)||a.name.localeCompare(b.name));
 }
 function operationSteps(op) {
  if(!op.id)return [];
  const labels=op.action==='apply'?['Validate & save','Pull images','Check files','Stop','Start']:op.action==='save'?['Validate','Save file']:['Validate',op.action==='stop'?'Stop':'Start'];
  const commands=[...(op.log||'').matchAll(/^> docker compose (.+)$/gm)].map(x=>x[1]);
  const last=commands.at(-1)||'';let active=0;
  if(op.action==='apply'){
   if(commands.includes('pull'))active=1;
   if(commands.includes('pull')&&last.startsWith('config '))active=2;
   if(commands.includes('down'))active=3;
   if(commands.some(c=>c.startsWith('up ')))active=4;
  }else if(op.action==='save')active=(op.log||'').includes('File saved;')?1:0;
  else if(commands.some(c=>c==='down'||c.startsWith('up ')))active=1;
  const success=op.phase==='completed'&&!op.running;
  const failed=['failed','interrupted'].includes(op.phase);
  return labels.map((label,i)=>({label,state:success||i<active?'done':i===active?(failed?'failed':'active'):'pending'}));
 }
 const api={filterContainers,projectRows,operationSteps};root.ReefUI=api;if(typeof module!=='undefined')module.exports=api;
})(typeof window!=='undefined'?window:globalThis);
