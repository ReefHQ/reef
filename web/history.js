'use strict';
(()=>{
 const find=id=>document.getElementById(id),view=find('history-view');
 let host=window.reefHost||'local',epoch=0,controller=null,timer,hostsReady=false,loading=false,paused=false,loaded=false,failed=false,wasVisible=!view.hidden,rows=[],offset=0,total=0,cursor='',retained=0,dropped=0,persistenceError=false,checked='';
 function restoreFilters(){const saved=window.ReefNavigation?.read();if(saved?.view==='history'&&saved.host===host){offset=saved.historyOffset||0;cursor=saved.historyCursor||'';if(offset&&!cursor)offset=0;for(const id of ['history-search','history-scope','history-status-filter','history-hosts']){const field=find(id),value=saved.filters[id]||'';if(field.tagName!=='SELECT'||[...field.options].some(o=>o.value===value))field.value=value;}}}
 restoreFilters();
 const label={started:'Started',completed:'Completed',failed:'Failed',partial:'Partial',closed:'Closed',unknown:'Unknown'};
 const node=(tag,text,className='')=>{const el=document.createElement(tag);el.textContent=text;el.className=className;return el;};
 const date=value=>value?new Date(value).toLocaleString('en-GB'):'Not recorded';
 function ready(){window.reefHistoryReady=true;window.dispatchEvent(new CustomEvent('reef-history-ready'));}
 function persist(user=false){window.reefHistoryOffset=offset;window.reefHistoryCursor=cursor;window.dispatchEvent(new CustomEvent(user?'reef-history-interaction':'reef-history-location'));}
 function notice(text){find('history-error').textContent=text;find('history-error').hidden=!text;}
 function controls(){
  const pending=loading||(!loaded&&!failed&&!paused);
  window.reefHistoryOffset=offset;window.reefHistoryCursor=cursor;
  find('history-loading').hidden=!pending;find('history-loading-text').textContent=loaded?'Refreshing action history…':'Loading action history…';find('history-rows').setAttribute('aria-busy',String(pending));
  find('history-refresh').disabled=pending;find('history-pause').disabled=offset>0;find('history-pause').textContent=paused?'Resume':'Pause';find('history-pause').setAttribute('aria-pressed',String(paused));
  find('history-previous').disabled=loading||!offset;find('history-next').disabled=loading||offset+50>=total;
  find('history-summary').textContent=loaded?`${total} actions matching · ${rows.length?offset+1:0}–${offset+rows.length} shown · ${retained} retained events`:'';
  find('history-status').textContent=failed?'History unavailable; previous results may be stale':pending?'Reading action history…':offset>0?'Older snapshot page · use Refresh for latest actions':paused?'Automatic refresh paused':checked?`Last refreshed ${new Date(checked).toLocaleTimeString('en-GB')} · latest page refreshes every 5 seconds`:'';
  find('history-warning').hidden=!persistenceError&&!dropped;find('history-warning').textContent=[persistenceError?'History persistence is unavailable. Actions still work; inspect private history storage and restart Reef if needed.':'',dropped?`${dropped} local metadata event(s) could not be retained. History may be incomplete.`:''].filter(Boolean).join(' ');
  find('history-empty').hidden=!!rows.length;find('history-empty').textContent=pending?'Loading action history…':failed?'History unavailable. Use Refresh to try again.':'No recorded actions match these filters.';
 }
 function details(row){
  const fields=find('history-detail-fields');fields.replaceChildren();
  for(const [name,value] of [['Host',row.host],['Category',row.scope],['Action',row.action],['Target',row.target||(row.count?`${row.count} items`:'None')],['Last recorded status',label[row.status]||row.status],['First event time',date(row.started)],['Last event time (host clock)',date(row.time)],['Recorded centrally',date(row.recorded)],['Operation ID',row.operation_id||'Not provided'],['Result counts',row.count?`${row.succeeded||0} succeeded · ${row.failed||0} failed · ${row.skipped||0} skipped`:'Not provided'],['Note',row.note||'This is action metadata, not command output.']])fields.append(node('dt',name),node('dd',value));
  find('history-details').showModal();
 }
 function render(){
  const nodes=rows.map(row=>{
   const tr=document.createElement('tr'),time=document.createElement('td'),action=document.createElement('td'),result=document.createElement('td'),open=document.createElement('td');
   time.append(node('span',date(row.recorded)),node('small',`Host time: ${date(row.time)}`,'muted'));
   action.append(node('strong',row.action),node('small',row.scope,'muted'));
   result.append(node('span',label[row.status]||row.status,'tag '+(['failed','unknown','partial'].includes(row.status)?'failed':'neutral-tag')));if(row.note)result.append(node('small',row.note,'muted'));
   const button=node('button','Details','compact-button');button.type='button';button.dataset.focusKey='history-'+row.id;button.setAttribute('aria-label',`Action details for ${row.target||row.action}`);button.addEventListener('click',()=>details(row));open.append(button);
   tr.append(time,node('td',row.host),action,node('td',row.target||(row.count?`${row.count} items`:'—')),result,open);return tr;
  });replaceKeepingFocus(find('history-rows'),nodes);controls();
 }
 function stop(){epoch++;controller?.abort();controller=null;loading=false;clearTimeout(timer);}
 async function load(){
  if(!hostsReady||view.hidden||document.hidden||loading)return;
  clearTimeout(timer);const current=epoch,chosenHost=host;controller=new AbortController();loading=true;controls();
  const query=new URLSearchParams({host:find('history-hosts').value==='all'?'all':chosenHost,offset:String(offset),search:find('history-search').value,scope:find('history-scope').value,status:find('history-status-filter').value});
  if(offset>0&&cursor)query.set('cursor',cursor);
  try{
   const data=await(await request('/api/history?'+query,{signal:controller.signal},'local')).json();if(current!==epoch)return;
   rows=data.rows;offset=data.offset;total=data.total;cursor=data.cursor;retained=data.retained_events;dropped=data.dropped;persistenceError=data.persistence_error;checked=data.checked_at;loaded=true;failed=false;notice('');render();persist();
  }catch(error){if(current===epoch&&error.name!=='AbortError'){failed=true;notice(`${error.message}${loaded?' Showing previous results; use Refresh to return to the latest page.':''}`);render();}}
  finally{if(current===epoch){loading=false;controller=null;controls();ready();if(!loading&&!paused&&!offset&&!view.hidden&&!document.hidden){clearTimeout(timer);timer=setTimeout(load,5000);}}}
 }
 function changeFilters(){stop();offset=0;cursor='';controls();persist(true);timer=setTimeout(load,250);}
 find('history-refresh').addEventListener('click',()=>{stop();offset=0;cursor='';controls();persist(true);load();});
 find('history-pause').addEventListener('click',()=>{persist(true);paused=!paused;stop();controls();if(!paused)load();else if(!window.reefHistoryReady)ready();});
 find('history-search').addEventListener('input',changeFilters);for(const id of ['history-scope','history-status-filter','history-hosts'])find(id).addEventListener('change',changeFilters);
 find('history-previous').addEventListener('click',()=>{if(loading||!offset)return;persist(true);stop();offset=Math.max(0,offset-50);load();});
 find('history-next').addEventListener('click',()=>{if(loading||offset+50>=total)return;persist(true);stop();offset+=50;load();});
 find('history-detail-close').addEventListener('click',()=>find('history-details').close());
 window.addEventListener('reef-view-changed',()=>{const visible=!view.hidden,entered=visible&&!wasVisible;wasVisible=visible;if(!visible){stop();controls();}else if(entered&&!paused)load();});
 window.addEventListener('reef-host-changed',event=>{stop();host=window.reefHost||'local';window.reefHistoryReady=false;rows=[];offset=0;total=0;cursor='';checked='';retained=0;loaded=false;failed=false;persistenceError=false;dropped=0;if(find('history-details').open)find('history-details').close();for(const id of ['history-search','history-scope','history-status-filter','history-hosts'])find(id).value='';if(event.detail?.restoring)restoreFilters();notice('');render();persist();load();});
 document.addEventListener('visibilitychange',()=>{if(document.hidden){stop();controls();}else if(!paused&&!offset)load();});
 render();(window.reefHostsReady||Promise.resolve()).then(()=>{hostsReady=true;load();});
})();
