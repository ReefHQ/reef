'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs');
function page(admin=true){
 const nodes=new Map(),calls=[];let next={current:'0.24.1',latest:'0.25.0',status:'available',available:true,message:'Reef 0.25.0 is available on GitHub.',checked_at:'2026-10-30T12:00:00Z'};
 const get=id=>{if(!nodes.has(id))nodes.set(id,{textContent:'',hidden:false,disabled:false,open:false,handlers:{},classList:{toggle(){}},addEventListener(type,fn){this.handlers[type]=fn},showModal(){this.open=true},close(){this.open=false}});return nodes.get(id)};
 const ctx={window:{reefAccess:{globalAdmin:admin},reefHost:'remote-one'},document:{getElementById:get},URLSearchParams,Date,csrf:'fixture-csrf',setInterval(fn){ctx.poll=fn},request:async(path,options,host)=>{calls.push({path,options,host});if(next instanceof Error)throw next;const result=next;return {json:async()=>result}}};
 vm.createContext(ctx);vm.runInContext(fs.readFileSync('web/reef-updates.js','utf8'),ctx);
 const flush=async()=>{for(let i=0;i<10;i++)await Promise.resolve()};
 return {get,ctx,calls,flush,set(value){next=value}};
}
test('update notification is central, survives host changes and links only a stable changelog',async()=>{
 const p=page();await p.flush();assert.equal(p.get('open-reef-update').textContent,'Reef 0.25.0 available');assert.equal(p.calls[0].host,'local');assert.equal(p.get('reef-update-link').href,'https://github.com/ReefHQ/reef/blob/v0.25.0/CHANGELOG.md');
 p.ctx.window.reefHost='remote-two';await p.ctx.poll();assert.equal(p.calls[1].host,'local');assert.equal(p.calls[1].options,undefined);
 p.set({current:'dev',latest:'https://evil.invalid',message:'Development build'});await p.ctx.poll();assert.equal(p.get('reef-update-link').hidden,true);
});
test('manual check uses CSRF, shows busy state and blocks duplicate checks',async()=>{
 const p=page();await p.flush();let resolve;p.set(new Promise(r=>resolve=r));const action=p.get('reef-update-check').handlers.click();await Promise.resolve();assert.equal(p.get('reef-update-check').disabled,true);await p.get('reef-update-check').handlers.click();assert.equal(p.calls.length,2);
 resolve({current:'0.25.0',latest:'0.25.0',message:'Current',available:false});await action;assert.equal(p.get('reef-update-check').disabled,false);assert.equal(p.calls[1].options.method,'POST');assert.equal(p.calls[1].options.body.get('csrf'),'fixture-csrf');assert.equal(p.calls[1].host,'local');assert.equal(p.get('open-reef-update').textContent,'Reef updates');
});
test('viewers can read update status and open and close the panel',async()=>{
 const p=page(false);await p.flush();assert.equal(p.get('reef-update-check').hidden,true);await p.get('open-reef-update').handlers.click();assert.equal(p.get('reef-update-dialog').open,true);p.get('reef-update-close').handlers.click();assert.equal(p.get('reef-update-dialog').open,false);assert.equal(p.calls.some(c=>c.options?.method==='POST'),false);
});
test('network failures retain the detected update and report the failed read',async()=>{
 const p=page();await p.flush();p.set(Error('offline'));await p.ctx.poll();assert.equal(p.get('open-reef-update').textContent,'Reef 0.25.0 available');assert.match(p.get('reef-update-message').textContent,/unavailable.*offline/);assert.equal(p.get('reef-update-check').disabled,false);
});
