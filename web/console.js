'use strict';
(()=>{
 const find=s=>document.querySelector(s),csrf=find('meta[name="csrf-token"]').content;
 const shell=find('#console-shell'),connect=find('#console-connect'),disconnect=find('#console-disconnect');
 const status=find('#console-status'),error=find('#console-error'),host=find('#terminal');
 const terminal=new Terminal({cursorBlink:true,scrollback:2000,fontSize:14,fontFamily:'ui-monospace, SFMono-Regular, Menlo, monospace',allowProposedApi:false,theme:{background:'#091c26',foreground:'#c3e2e7',cursor:'#7be0db'}});
 const fit=new FitAddon.FitAddon();terminal.loadAddon(fit);terminal.open(host);
 let active='',controller=null,generation=0,queue=[],queued=0,buffered=0,inputTimer=null,resizeTimer=null,chain=Promise.resolve();
 const utf8=new TextEncoder();
 const base64=b=>btoa(String.fromCharCode(...b));
 function controls(busy){connect.disabled=busy;shell.disabled=busy;disconnect.disabled=!busy;}
 async function post(path,fields,signal){
  const response=await fetch(path,{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams({csrf,...fields}),signal});
  if(response.status===401){location.href='/login';throw Error('Sign-in required');}
  if(!response.ok)throw Error(await response.text());return response;
 }
 function stop(message='Disconnected.',beacon=false){
  const id=active;active='';generation++;controller?.abort();controller=null;clearTimeout(inputTimer);clearTimeout(resizeTimer);inputTimer=null;resizeTimer=null;queue=[];queued=0;buffered=0;chain=Promise.resolve();controls(false);status.textContent=message;
  if(id){const body=new URLSearchParams({csrf,console:id});if(beacon)navigator.sendBeacon('/api/console/close',body);else post('/api/console/close',{console:id}).catch(()=>{});}
 }
 function fail(failure){error.textContent=failure.message;error.hidden=false;stop('Connection ended. Reconnect to open a new shell.');}
 function enqueue(bytes){
  if(!active)return;
  if(buffered+bytes.length>32768){fail(Error('Terminal input queue is full. Reconnect before continuing.'));return;}
  queue.push(bytes);queued+=bytes.length;buffered+=bytes.length;if(inputTimer===null)inputTimer=setTimeout(flushInput,20);
 }
 function flushInput(){
  inputTimer=null;if(!active||!queued)return;
  const data=new Uint8Array(queued);let offset=0;for(const b of queue){data.set(b,offset);offset+=b.length;}queue=[];queued=0;
  const id=active,token=generation,signal=controller.signal;
  chain=chain.then(async()=>{for(let i=0;i<data.length;i+=16384){if(token!==generation)return;await post('/api/console/input',{console:id,data:base64(data.subarray(i,i+16384))},signal);}}).catch(failure=>{if(token===generation&&failure.name!=='AbortError')fail(failure);}).finally(()=>{if(token===generation)buffered-=data.length;});
 }
 terminal.onData(data=>enqueue(utf8.encode(data)));
 terminal.onBinary(data=>enqueue(Uint8Array.from(data,c=>c.charCodeAt(0))));
 function dimensions(){fit.fit();return {cols:String(Math.max(2,Math.min(300,terminal.cols))),rows:String(Math.max(2,Math.min(150,terminal.rows)))};}
 function resize(){clearTimeout(resizeTimer);resizeTimer=setTimeout(()=>{const size=dimensions();if(active){const id=active,token=generation;post('/api/console/resize',{console:id,...size},controller.signal).catch(failure=>{if(token===generation&&failure.name!=='AbortError')fail(failure);});}},150);}
 new ResizeObserver(resize).observe(host);
 async function open(event){
  event.preventDefault();if(controller)return;
  error.hidden=true;controls(true);terminal.reset();status.textContent='Connecting…';
  controller=new AbortController();const token=++generation;
  try{
   const response=await post('/api/console/open',{id:document.body.dataset.containerId,shell:shell.value,confirmed:'yes',...dimensions()},controller.signal);
   const reader=response.body.getReader(),decoder=new TextDecoder();let pending='';
   const handle=async frame=>{
    if(frame.type==='ready'){active=frame.id;status.textContent=`Connected · ${frame.name} · ${shell.value}`;terminal.focus();}
    else if(frame.type==='output'){const bytes=Uint8Array.from(atob(frame.data),c=>c.charCodeAt(0));await new Promise(resolve=>terminal.write(bytes,resolve));}
    else if(frame.type==='closed')stop(frame.message);
   };
   for(;;){const {value,done}=await reader.read();if(token!==generation)return;if(done)break;
    pending+=decoder.decode(value,{stream:true});let newline;
    while((newline=pending.indexOf('\n'))!==-1){const line=pending.slice(0,newline);pending=pending.slice(newline+1);if(line)await handle(JSON.parse(line));if(token!==generation)return;}
    if(pending.length>65536)throw Error('Invalid console response.');
   }
   if(token===generation)stop('Console connection ended. Reconnect to open a new shell.');
  }catch(failure){if(token===generation&&failure.name!=='AbortError')fail(failure);}
 }
 find('#console-connect-form').addEventListener('submit',open);
 disconnect.addEventListener('click',()=>stop());
 find('#console-clear').addEventListener('click',()=>{terminal.clear();terminal.focus();});
 window.addEventListener('beforeunload',()=>stop('Disconnected.',true));
})();
