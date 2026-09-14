export type ServiceGroup = { id:string; name:string; os:string; arch:string; state:string; services:any[] };
export function groupServices(services:any[],agents:any[],query=''):ServiceGroup[]{
 const groups=new Map<string,ServiceGroup>();
 for(const a of agents)groups.set(String(a.id),{id:String(a.id),name:a.display_name||a.hostname||a.id,os:a.os||'',arch:a.arch||'',state:a.state||'unknown',services:[]});
 for(const s of services){const id=String(s.agent_id);if(!groups.has(id))groups.set(id,{id,name:id,os:'',arch:'',state:s.agent_state||'unknown',services:[]});groups.get(id)!.services.push(s);}
 const q=query.trim().toLocaleLowerCase();
 return [...groups.values()].map(g=>({...g,services:g.services.filter(s=>!q||[g.name,g.id,s.display_name,s.url,s.summary].join(' ').toLocaleLowerCase().includes(q))})).filter(g=>g.services.length>0).sort((a,b)=>a.name.localeCompare(b.name)||a.id.localeCompare(b.id));
}
