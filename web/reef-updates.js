'use strict';
(()=>{
 const get=id=>document.getElementById(id),button=get('open-reef-update'),dialog=get('reef-update-dialog'),check=get('reef-update-check');
 let busy=false;
 check.hidden=!window.reefAccess?.globalAdmin;
 function render(data){
  button.textContent=data.available?`Reef ${data.latest} available`:'Reef updates';
  button.classList.toggle('update-available',!!data.available);
  get('reef-update-versions').textContent=`Running ${data.current}${data.latest?` · Latest stable ${data.latest}`:''}`;
  get('reef-update-message').textContent=data.message;
  get('reef-update-checked').textContent=data.checking?'Checking GitHub…':data.checked_at&&!data.checked_at.startsWith('0001-')?`Last attempt: ${new Date(data.checked_at).toLocaleString()}`:'Not checked yet';
  const link=get('reef-update-link');
  // Only construct the known public changelog URL from a stable numeric tag.
  const valid=/^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/.test(data.latest||'');
  link.hidden=!valid;if(valid)link.href=`https://github.com/ReefHQ/reef/blob/v${data.latest}/CHANGELOG.md`;
 }
 async function refresh(manual=false){
  if(busy)return;busy=true;check.disabled=true;
  if(manual)get('reef-update-message').textContent='Checking GitHub…';
  try{
   const options=manual?{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams({csrf})}:undefined;
   render(await(await request('/api/reef-update',options,'local')).json());
  }catch(e){get('reef-update-message').textContent=`Update status unavailable: ${e.message}`;}
  finally{busy=false;check.disabled=false;}
 }
 button.addEventListener('click',()=>{if(!dialog.open)dialog.showModal();refresh();});
 get('reef-update-close').addEventListener('click',()=>dialog.close());
 check.addEventListener('click',()=>refresh(true));
 refresh();setInterval(()=>refresh(),60000);
})();
