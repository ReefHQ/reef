'use strict';
// Per-tab navigation only: never persist editor contents, logs or terminal state.
(()=>{
 const key='reef.navigation.v1';
 const filterIds=['container-search','container-state','container-project','container-access','project-search','project-access'];
 function normalize(value){
  const data=value&&typeof value==='object'?value:{};
  const text=value=>typeof value==='string'&&value.length<=512?value:'';
  const filters={};for(const id of filterIds)filters[id]=text(data.filters?.[id]);
  return {view:data.view==='projects'?'projects':'containers',host:text(data.host)||'local',project:text(data.project),filters,scroll:Number.isFinite(data.scroll)&&data.scroll>=0?Math.min(data.scroll,10000000):0};
 }
 function read(){try{return normalize(JSON.parse(sessionStorage.getItem(key)));}catch{return normalize();}}
 function write(value){try{sessionStorage.setItem(key,JSON.stringify(normalize(value)));}catch{/* Navigation still works when storage is blocked. */}}
 window.ReefNavigation={read,write,filterIds};
})();
