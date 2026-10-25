'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const source=fs.readFileSync(path.join(__dirname,'../web/console-picker.js'),'utf8');
function page(){
 const nodes=new Map();
 const element=tag=>({tag,textContent:'',events:{},attributes:{},children:[],open:false,addEventListener(name,fn){this.events[name]=fn;},setAttribute(name,value){this.attributes[name]=value;},replaceChildren(...children){this.children=children;},showModal(){this.open=true;},close(){this.open=false;}});
 const find=id=>{if(!nodes.has(id))nodes.set(id,element('div'));return nodes.get(id);};
 const opened=[];const window={openContainerConsole:c=>opened.push(c)};vm.runInNewContext(source,{window,document:{querySelector:find,createElement:element},encodeURIComponent});return {find,opened,open:window.openProjectConsole};
}
test('project console selection lists only running containers with safe links and does not start a shell',()=>{
 const p=page();p.open({project:'My project',containers:[{id:'a'.repeat(64),name:'<script>web</script>',compose_service:'web',state:'running'},{id:'b'.repeat(64),name:'worker',state:'exited'}]});
 const links=p.find('#project-console-containers').children;assert.equal(links.length,1);assert.equal(links[0].tag,'button');assert.equal(links[0].textContent,'web · <script>web</script>');assert.equal(p.find('#project-console').open,true);
 links[0].events.click();assert.equal(p.find('#project-console').open,false);assert.equal(p.opened[0].id,'a'.repeat(64));
});
test('empty project console selection explains that a running container is required',()=>{
 const p=page();p.open({project:'Stopped',containers:[]});const rows=p.find('#project-console-containers').children;assert.equal(rows[0].tag,'p');assert.match(rows[0].textContent,/No running containers/);assert.equal(p.find('#project-console-title').textContent,'Console · Stopped');
});
