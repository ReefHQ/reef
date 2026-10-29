
'use strict';
(()=>{
 const data=document.body.dataset,role=data.userRole||'admin';
 window.reefAccess={canWrite:role==='admin',globalAdmin:data.instanceAdmin!=='false',username:data.userName||'',userId:data.userId||''};
 if(data.initialHost)window.reefHost=data.initialHost;
 const summary=document.getElementById('access-summary');if(summary&&role==='viewer'){summary.hidden=false;summary.textContent='Viewer · read-only access to your assigned hosts.';}
})();
