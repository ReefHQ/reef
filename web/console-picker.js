'use strict';
(()=>{
 const dialog=document.querySelector('#project-console'),list=document.querySelector('#project-console-containers');
 window.openProjectConsole=({containers,project})=>{
  document.querySelector('#project-console-title').textContent=`Console · ${project}`;
  const running=containers.filter(c=>c.state==='running');
  const links=running.map(c=>{
   const link=document.createElement('button');link.type='button';link.className='console-link';
   link.textContent=`${c.compose_service?c.compose_service+' · ':''}${c.name}`;
   link.setAttribute('aria-label',`Console for ${c.name}`);
   link.addEventListener('click',()=>{dialog.close();window.openContainerConsole(c);});return link;
  });
  if(!links.length){const empty=document.createElement('p');empty.className='muted small';empty.textContent='No running containers. Start the project, then open its console.';links.push(empty);}
  list.replaceChildren(...links);if(!dialog.open)dialog.showModal();
 };
 document.querySelector('#close-project-console').addEventListener('click',()=>dialog.close());
})();
