'use strict';
(()=>{
 const find=s=>document.querySelector(s),channel=document.body.dataset.consoleChannel;
 const shell=find('#console-shell'),connect=find('#console-connect'),disconnect=find('#console-disconnect'),status=find('#console-status'),error=find('#console-error'),host=find('#terminal');
 const terminal=new Terminal({cursorBlink:true,scrollback:2000,fontSize:13,fontFamily:'ui-monospace, SFMono-Regular, Menlo, monospace',allowProposedApi:false,theme:{background:'#091c26',foreground:'#c3e2e7',cursor:'#7be0db'}}),fit=new FitAddon.FitAddon();terminal.loadAddon(fit);terminal.open(host);
 let connected=false,opening=false,visible=true,generation=0,next=0,queue=[],queued=0,buffered=0,inputTimer=null,resizeTimer=null,chain=Promise.resolve();const pending=new Map(),utf8=new TextEncoder();
 const send=message=>parent.postMessage({type:'reef-console-frame',channel,...message},location.origin);
 function rpc(action,fields={}){const request=++next;return new Promise((resolve,reject)=>{const timer=setTimeout(()=>{pending.delete(request);reject(Error('Console request timed out.'));},15000);pending.set(request,{resolve,reject,timer});send({request,action,fields});});}
 function controls(busy){connect.disabled=busy;shell.disabled=busy;disconnect.disabled=!busy;}
 function stop(message='Disconnected.',remote=false){
  const wasActive=connected||opening;connected=false;opening=false;generation++;controls(false);status.textContent=message;clearTimeout(inputTimer);clearTimeout(resizeTimer);inputTimer=null;resizeTimer=null;queue=[];queued=0;buffered=0;chain=Promise.resolve();
  for(const request of pending.values()){clearTimeout(request.timer);request.reject(Error('Console disconnected.'));}pending.clear();
  if(wasActive&&!remote)rpc('close').catch(()=>{});
 }
 function fail(failure){error.textContent=failure.message;error.hidden=false;stop('Connection ended. Reconnect to open a new shell.');}
 function enqueue(bytes){if(!connected)return;if(buffered+bytes.length>32768){fail(Error('Terminal input queue is full. Reconnect before continuing.'));return;}queue.push(bytes);queued+=bytes.length;buffered+=bytes.length;if(inputTimer===null)inputTimer=setTimeout(flush,20);}
 function flush(){inputTimer=null;if(!connected||!queued)return;const data=new Uint8Array(queued);let offset=0;for(const bytes of queue){data.set(bytes,offset);offset+=bytes.length;}queue=[];queued=0;const token=generation;chain=chain.then(async()=>{for(let i=0;i<data.length;i+=16384){if(token!==generation)return;await rpc('input',{data:btoa(String.fromCharCode(...data.subarray(i,i+16384)))});}}).catch(failure=>{if(token===generation)fail(failure);}).finally(()=>{if(token===generation)buffered-=data.length;});}
 terminal.onData(data=>enqueue(utf8.encode(data)));terminal.onBinary(data=>enqueue(Uint8Array.from(data,c=>c.charCodeAt(0))));
 function dimensions(){if(visible)fit.fit();return {cols:String(Math.max(2,Math.min(300,terminal.cols))),rows:String(Math.max(2,Math.min(150,terminal.rows)))};}
 function resize(){clearTimeout(resizeTimer);if(!visible)return;resizeTimer=setTimeout(()=>{const size=dimensions(),token=generation;if(connected)rpc('resize',size).catch(failure=>{if(token===generation)fail(failure);});},150);}
 new ResizeObserver(resize).observe(host);
 find('#console-connect-form').addEventListener('submit',async event=>{
  event.preventDefault();if(connected||opening)return;error.hidden=true;terminal.reset();opening=true;controls(true);status.textContent='Connecting…';const token=++generation;
  try{await rpc('open',{shell:shell.value,...dimensions()});}catch(failure){if(token===generation)fail(failure);}
 });
 window.addEventListener('message',event=>{
  const message=event.data;if(event.origin!==location.origin||event.source!==parent||message?.type!=='reef-console-host'||message.channel!==channel)return;
  if(message.request){const request=pending.get(message.request);if(request){clearTimeout(request.timer);pending.delete(message.request);if(message.ok)request.resolve();else request.reject(Error(message.message));}return;}
  if(message.action==='disconnect'){stop();return;}
  if(message.action==='activate'){visible=!!message.active;if(visible){resize();if(connected)terminal.focus();}return;}
  const frame=message.frame;
  if(frame?.type==='ready'){opening=false;connected=true;controls(true);status.textContent=`Connected · ${shell.value}`;if(visible)terminal.focus();}
  else if(frame?.type==='output')terminal.write(Uint8Array.from(atob(frame.data),c=>c.charCodeAt(0)));
  else if(frame?.type==='closed')stop(frame.message,true);
 });
 disconnect.addEventListener('click',()=>stop());find('#console-clear').addEventListener('click',()=>{terminal.clear();terminal.focus();});send({action:'loaded'});
})();
