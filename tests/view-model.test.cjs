const {test}=require('node:test');const assert=require('node:assert/strict');const {filterContainers,projectRows,operationSteps}=require('../web/model.js');
const containers=[{id:'a',name:'api-worker',image:'worker:v1',state:'exited',compose_project:'api'},{id:'b',name:'api-web',image:'web:v2',state:'running',compose_project:'api',managed_project_id:'api-managed'},{id:'c',name:'standalone',image:'web:v2',state:'paused',compose_project:''}];
test('container filters combine search, exact state/project and management access',()=>{
 assert.deepEqual(filterContainers(containers,{search:' WEB ',state:'running',project:'api',access:'managed'}).map(c=>c.id),['b']);
 assert.deepEqual(filterContainers(containers,{state:'exited',access:'visible'}).map(c=>c.id),['a']);
 assert.deepEqual(filterContainers(containers,{project:'__none__'}).map(c=>c.id),['c']);
 assert.equal(filterContainers(containers,{search:'api',state:'paused'}).length,0);assert.equal(filterContainers(containers).length,3);
});
test('project overview includes configured projects with no containers, without granting discovery access',()=>{
 const managed=[{id:'api-managed',project_name:'api',name:'API'},{id:'stopped',project_name:'stopped-project',name:'Stopped'}];const rows=projectRows(containers,managed);
 assert.equal(rows.length,2);assert.equal(rows.find(p=>p.name==='api').containers.length,2);assert.equal(rows.find(p=>p.name==='stopped-project').containers.length,0);
 assert.equal(projectRows(containers,[]).find(p=>p.name==='api').managed,null);
 assert.equal(projectRows([{...containers[0],is_reef:true},{...containers[1],is_reef:false}],[])[0].self,true);
});
test('operation progress distinguishes a pull failure before downtime from failed startup',()=>{
 const pull={id:'op',action:'apply',phase:'failed',running:false,log:'> docker compose config --quiet\nFile saved; a backup exists.\n> docker compose pull\nerror'};
 assert.deepEqual(operationSteps(pull).map(s=>s.state),['done','failed','pending','pending','pending']);
 const startup={...pull,log:pull.log+'\n> docker compose config --quiet\n> docker compose down\n> docker compose up -d\nerror',needs_start:true};
 assert.deepEqual(operationSteps(startup).map(s=>s.state),['done','done','done','done','failed']);
 assert.deepEqual(operationSteps({...startup,phase:'completed'}).map(s=>s.state),['done','done','done','done','done']);
 assert.deepEqual(operationSteps({}),[]);
});
test('save progress does not imply that services were restarted',()=>{
 const rows=operationSteps({id:'save',action:'save',phase:'completed',log:'> docker compose config --quiet\nFile saved;'});assert.deepEqual(rows.map(r=>r.label),['Validate','Save file']);assert.ok(rows.every(r=>r.state==='done'));
});
