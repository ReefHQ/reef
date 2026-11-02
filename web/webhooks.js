'use strict';
(()=>{
 const find=s=>document.querySelector(s),dialog=find('#webhook-dialog');let busy=false,saved=null,epoch=0,categoryInputs=[],categoriesReady=false;
 const api=(options)=>request('/api/webhook',options,'local');
 const error=e=>{find('#webhook-error').textContent=e.message;find('#webhook-error').hidden=false;};
 function stats(value){
  saved=value;
  find('#webhook-destination').textContent=value.configured?`Saved destination: ${value.destination}. Leave the URL empty to keep it.`:'No webhook URL saved.';
  find('#webhook-status').textContent=`${value.enabled?'Enabled':'Disabled'} · ${value.pending} waiting · ${value.sent} sent · ${value.dropped} dropped${value.last_success&&!value.last_success.startsWith('0001')?' · Last sent: '+new Date(value.last_success).toLocaleString():''}${value.last_error?' · '+value.last_error:''}${value.persistence_error?' · Notification state could not be persisted':''}`;
  find('#webhook-test').disabled=busy||!value.enabled||!value.configured;find('#webhook-remove').disabled=busy||!value.configured;
 }
 function categorySummary(){const count=categoryInputs.filter(input=>input.checked).length;find('#webhook-category-summary').textContent=count?`${count} of ${categoryInputs.length} categories selected.`:'No automatic notifications selected. Send test still works.';}
 function categories(value){
  if(!Array.isArray(value.category_options)||!value.category_options.length||!Array.isArray(value.categories))throw Error('Notification categories unavailable; reload Reef.');
  const selected=new Set(value.categories);categoryInputs=[];const labels=value.category_options.map(option=>{const label=document.createElement('label'),input=document.createElement('input'),name=document.createElement('span');label.className='webhook-category';input.type='checkbox';input.value=option.id;input.checked=selected.has(option.id);input.addEventListener('change',categorySummary);name.textContent=option.name;label.append(input,name);categoryInputs.push(input);return label;});find('#webhook-categories').replaceChildren(...labels);categoriesReady=true;categorySummary();
 }
 function lock(value){busy=value;for(const id of ['#webhook-enabled','#webhook-format','#webhook-url','#webhook-save','#webhook-categories-all','#webhook-categories-none'])find(id).disabled=value||!categoriesReady&&['#webhook-save','#webhook-categories-all','#webhook-categories-none'].includes(id);for(const input of categoryInputs)input.disabled=value;if(saved)stats(saved);}
 async function refresh(initial=false){
  if(busy)return;const ticket=epoch;if(initial){lock(true);find('#webhook-status').textContent='Loading webhook settings…';}
  try{const result=await(await api()).json();if(ticket!==epoch||!dialog.open)return;stats(result);if(initial){find('#webhook-enabled').checked=result.enabled;find('#webhook-format').value=result.format;find('#webhook-url').value='';categories(result);}}catch(e){if(ticket===epoch)error(e);}finally{if(initial&&ticket===epoch)lock(false);}
 }
 async function action(fields){const {categories,...rest}=fields,body=new URLSearchParams({csrf,confirmed:'yes',...rest});if(Array.isArray(categories))for(const id of categories)body.append('categories',id);return (await api({method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body})).json();}
 find('#open-webhook').addEventListener('click',()=>{epoch++;busy=false;categoriesReady=false;find('#webhook-error').hidden=true;dialog.showModal();refresh(true);});
 find('#webhook-close').addEventListener('click',()=>dialog.close());
 dialog.addEventListener('close',()=>{epoch++;lock(false);find('#webhook-url').value='';});
 find('#webhook-form').addEventListener('submit',async event=>{
  event.preventDefault();if(busy||!categoriesReady)return;const ticket=epoch;
  const fields={action:'save',enabled:String(find('#webhook-enabled').checked),format:find('#webhook-format').value,url:find('#webhook-url').value.trim(),categories_present:'yes',categories:categoryInputs.filter(input=>input.checked).map(input=>input.value)};
  if(!await ask(`Save webhook settings? ${fields.categories.length?`Receive ${fields.categories.length} of ${categoryInputs.length} notification categories for Local and remote hosts.`:'No automatic notifications will be sent. Send test remains available.'} Changing settings discards waiting notifications.`))return;
  if(ticket!==epoch||!dialog.open||busy)return;
  lock(true);find('#webhook-error').hidden=true;
  try{stats(await action(fields));find('#webhook-url').value='';}catch(e){error(e);}finally{lock(false);}
 });
 find('#webhook-test').addEventListener('click',async()=>{if(busy||!await ask('Send a test notification to the saved webhook destination?'))return;lock(true);try{stats(await action({action:'test'}));}catch(e){error(e);}finally{lock(false);}});
 find('#webhook-remove').addEventListener('click',async()=>{if(busy||!await ask('Remove the webhook URL and discard waiting notifications?'))return;lock(true);try{stats(await action({action:'save',enabled:'false',format:saved.format,clear:'yes'}));find('#webhook-enabled').checked=false;find('#webhook-url').value='';}catch(e){error(e);}finally{lock(false);}});
 for(const [id,checked] of [['#webhook-categories-all',true],['#webhook-categories-none',false]])find(id).addEventListener('click',()=>{if(busy||!categoriesReady)return;for(const input of categoryInputs)input.checked=checked;categorySummary();});
 setInterval(()=>{if(dialog.open)refresh();},3000);
})();
