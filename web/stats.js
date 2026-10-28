'use strict';
(()=>{
 const find=id=>document.getElementById(id),view=find('stats-view');
 let host=window.reefHost||'local',epoch=0,controller=null,timer,hostsReady=false,loading=false,paused=false,loaded=false,failed=false,rows=[],previous=new Map(),offset=0,total=0,hostTotal=0,checked='';
 function restoreFilters(){const saved=window.ReefNavigation?.read();if(saved?.view==='stats'&&saved.host===host){offset=saved.statsOffset||0;find('stats-search').value=saved.filters['stats-search']||'';find('stats-state').value=['running','stopped'].includes(saved.filters['stats-state'])?saved.filters['stats-state']:'';}}
 restoreFilters();
 let wasVisible=!view.hidden;
 const available=()=>host==='local'||window.reefCapabilities?.includes('stats-v1');
 const node=(tag,text,className='')=>{const el=document.createElement(tag);el.textContent=text;el.className=className;return el;};
 const bytes=value=>{if(!Number.isFinite(value)||value<0)return '—';let unit=0;while(value>=1024&&unit<4){value/=1024;unit++;}return `${value.toLocaleString('en-GB',{maximumFractionDigits:1})} ${['B','KiB','MiB','GiB','TiB'][unit]}`;};
 const rate=value=>Number.isFinite(value)?bytes(value)+'/s':'—';
 const percent=value=>Number.isFinite(value)?value.toLocaleString('en-GB',{maximumFractionDigits:1})+'%':'—';
 function notice(text){find('stats-error').textContent=text;find('stats-error').hidden=!text;}
 function ready(){window.reefStatsReady=true;window.dispatchEvent(new CustomEvent('reef-stats-ready'));}
 function persist(user=false){window.reefStatsOffset=offset;window.dispatchEvent(new CustomEvent(user?'reef-stats-interaction':'reef-stats-location'));}
 function controls(){
  window.reefStatsOffset=offset;
  find('stats-loading').hidden=!loading;find('stats-loading-text').textContent=loaded?'Refreshing statistics…':'Loading statistics…';find('stats-rows').setAttribute('aria-busy',String(loading));
  find('stats-refresh').disabled=!available()||loading;
  find('stats-pause').disabled=!available();find('stats-pause').textContent=paused?'Resume':'Pause';find('stats-pause').setAttribute('aria-pressed',String(paused));
  find('stats-previous').disabled=loading||!offset;find('stats-next').disabled=loading||offset+32>=total;
  find('stats-summary').textContent=loaded?`${hostTotal} containers · ${total} matching · ${rows.length?offset+1:0}–${offset+rows.length} shown`:'';
  find('stats-status').textContent=!available()?'Agent upgrade required':failed?'Statistics unavailable; previous samples may be stale':loading?'Collecting a bounded snapshot…':paused?'Automatic refresh paused':checked?`Last refreshed ${new Date(checked).toLocaleTimeString('en-GB')} · refresh every 5 seconds while visible`:'Waiting for statistics';
  find('stats-empty').hidden=!!rows.length;find('stats-empty').textContent=!available()?'Upgrade this host’s agent to view statistics.':loading?'Loading statistics…':failed?'Statistics unavailable. Use Refresh to try again.':'No containers match these filters.';
 }
 function render(){
  const nodes=rows.map(row=>{
   const tr=document.createElement('tr'),title=document.createElement('td'),cpu=document.createElement('td'),memory=document.createElement('td'),network=document.createElement('td'),block=document.createElement('td'),status=document.createElement('td'),s=row.stats;
   title.append(node('strong',row.name),node('small',row.project||row.image,'muted'));title.title=row.id;
   cpu.append(node('span',percent(s?.cpu_percent)),node('small',s?.cpu_interval_seconds?`${s.cpu_interval_seconds.toFixed(1)} s sample`:'No CPU sample','muted'));
   memory.append(node('span',bytes(s?.memory_bytes)),node('small',s?.memory_limit_bytes?`${percent(s.memory_percent)} of ${bytes(s.memory_limit_bytes)}`:'Limit unavailable','muted'));
   const rates=row.rates||{};
   network.append(node('span',`↓ ${rate(rates.network_rx_bytes)} · ↑ ${rate(rates.network_tx_bytes)}`),node('small',`Σ RX ${bytes(s?.network_rx_bytes)} · TX ${bytes(s?.network_tx_bytes)}`,'muted'));
   block.append(node('span',`R ${rate(rates.block_read_bytes)} · W ${rate(rates.block_write_bytes)}`),node('small',`Σ read ${bytes(s?.block_read_bytes)} · write ${bytes(s?.block_write_bytes)}`,'muted'));
   status.append(node('span',row.state,'tag '+(row.state==='running'?'running-state':'neutral-tag')),node('small',row.error||(s?`Sample ${new Date(s.read).toLocaleTimeString('en-GB')}`:'No live sample'),'muted'));
   tr.append(title,cpu,memory,network,block,status);return tr;
  });find('stats-rows').replaceChildren(...nodes);controls();
 }
 function stop(){epoch++;controller?.abort();controller=null;clearTimeout(timer);loading=false;}
 function withRates(data){
  const next=new Map();
  const result=data.map(row=>{if(!row.stats)return row;const time=Date.parse(row.stats.read),old=previous.get(row.id),rates={};
   if(old&&time===old.time){next.set(row.id,old);return {...row,rates:old.rates};}
   const elapsed=old?(time-old.time)/1000:0;
   for(const key of ['network_rx_bytes','network_tx_bytes','block_read_bytes','block_write_bytes']){const value=row.stats[key],prior=old?.stats[key];if(elapsed>0&&Number.isFinite(value)&&Number.isFinite(prior)&&value>=prior)rates[key]=(value-prior)/elapsed;}
   next.set(row.id,{time,stats:row.stats,rates});return {...row,rates};
  });previous=next;return result;
 }
 async function load(){
  if(!hostsReady||view.hidden||document.hidden||loading)return;
  if(!available()){notice('Upgrade this host’s agent to v0.14.0 or newer to view Statistics.');render();ready();return;}
  clearTimeout(timer);const current=epoch,chosenHost=host;controller=new AbortController();loading=true;controls();
  const query=new URLSearchParams({offset:String(offset),search:find('stats-search').value,state:find('stats-state').value});
  try{
   const data=await(await request('/api/stats?'+query,{signal:controller.signal},chosenHost)).json();if(current!==epoch)return;
   if(data.offset!==offset)previous.clear();offset=data.offset;total=data.total;hostTotal=data.host_total;checked=data.checked_at;rows=withRates(data.containers);loaded=true;failed=false;notice('');render();persist();
  }catch(error){if(current===epoch&&error.name!=='AbortError'){failed=true;notice(`${error.message}${loaded?' Showing previous samples; they may be stale.':''}`);render();}}
  finally{if(current===epoch){loading=false;controller=null;controls();ready();if(!loading&&!paused&&!view.hidden&&!document.hidden){clearTimeout(timer);timer=setTimeout(load,5000)};}}
 }
 function changeFilters(){stop();offset=0;previous.clear();controls();persist(true);timer=setTimeout(load,250);}
 find('stats-refresh').addEventListener('click',()=>{persist(true);stop();load();});
 find('stats-pause').addEventListener('click',()=>{persist(true);paused=!paused;stop();controls();if(!paused)load();else if(!window.reefStatsReady)ready();});
 find('stats-search').addEventListener('input',changeFilters);find('stats-state').addEventListener('change',changeFilters);
 find('stats-previous').addEventListener('click',()=>{if(loading||!offset)return;stop();offset=Math.max(0,offset-32);previous.clear();persist(true);load();});
 find('stats-next').addEventListener('click',()=>{if(loading||offset+32>=total)return;stop();offset+=32;previous.clear();persist(true);load();});
 window.addEventListener('reef-view-changed',()=>{const visible=!view.hidden,entered=visible&&!wasVisible;wasVisible=visible;if(!visible){stop();controls();}else if(entered&&!paused)load();});
 window.addEventListener('reef-host-changed',event=>{stop();host=window.reefHost||'local';window.reefStatsReady=false;rows=[];previous.clear();offset=0;total=0;hostTotal=0;checked='';loaded=false;failed=false;for(const id of ['stats-search','stats-state'])find(id).value='';if(event.detail?.restoring)restoreFilters();notice('');render();persist();load();});
 document.addEventListener('visibilitychange',()=>{if(document.hidden){stop();controls();}else if(!paused)load();});
 render();(window.reefHostsReady||Promise.resolve()).then(()=>{hostsReady=true;load();});
})();
