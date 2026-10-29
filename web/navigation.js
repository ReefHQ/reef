'use strict';
// Per-tab navigation only: never persist editor contents, logs or terminal state.
(()=>{
 const userId=typeof document==='object'?document.body?.dataset?.userId:'';
 const key='reef.navigation.v1'+(userId?'.'+userId:'');
 const filterIds=['container-search','container-state','container-project','container-access','project-search','project-access','images-search','images-usage','volumes-search','volumes-usage','networks-search','networks-usage','stats-search','stats-state','alerts-search','alerts-scope','alerts-result','history-search','history-scope','history-status-filter','history-hosts'];
 function normalize(value){
  const data=value&&typeof value==='object'?value:{};
  const text=value=>typeof value==='string'&&value.length<=512?value:'';
  const filters={};for(const id of filterIds)filters[id]=text(data.filters?.[id]);
  return {view:['alerts','history','stats','projects','images','volumes','networks','builds'].includes(data.view)?data.view:'containers',host:text(data.host)||'local',project:text(data.project),filters,historyOffset:Number.isInteger(data.historyOffset)&&data.historyOffset>=0&&data.historyOffset<=2000&&data.historyOffset%50===0?data.historyOffset:0,historyCursor:typeof data.historyCursor==='string'&&/^[A-Za-z0-9_-]{1,128}$/.test(data.historyCursor)?data.historyCursor:'',statsOffset:Number.isInteger(data.statsOffset)&&data.statsOffset>=0&&data.statsOffset<=9984&&data.statsOffset%32===0?data.statsOffset:0,scroll:Number.isFinite(data.scroll)&&data.scroll>=0?Math.min(data.scroll,10000000):0};
 }
 function read(){try{return normalize(JSON.parse(sessionStorage.getItem(key)));}catch{return normalize();}}
 function write(value){try{sessionStorage.setItem(key,JSON.stringify(normalize(value)));}catch{/* Navigation still works when storage is blocked. */}}
 window.ReefNavigation={read,write,filterIds};
})();
