// Presentation only: controller transactions are authoritative for scope and delivery.
export type RolloutMember = {agent_id:string;platform:string;wave:number;released_at?:string;status:string;stage:string;message:string};
export type Rollout = {operation_id:string;release_id:string;state:string;revision:number;batch_size:number;observe_seconds:number;wave:number;canary_waves:number;reason:string;deadline:string;stable_since?:string;members:RolloutMember[]};
export function rolloutLabel(state:string):string {return ({running:'Выполняется',paused:'На паузе',blocked:'Заблокировано после проверки',cancelling:'Отмена неотправленных целей',cancelled:'Отменено',completed:'Подтверждено'} as Record<string,string>)[state]||state;}
export function rolloutCounts(r:Rollout){
 const all=r.members||[];const bad=new Set(['failed','rejected','unsupported','expired','unknown_result','rolled_back']);
 return {total:all.length,confirmed:all.filter(m=>m.status==='succeeded').length,held:all.filter(m=>!m.released_at&&m.status==='queued').length,failed:all.filter(m=>bad.has(m.status)).length,cancelled:all.filter(m=>m.status==='cancelled_before_execution').length};
}
export function canaryPreview(ids:string[],agents:any[]){
 const selected=new Set(ids);const ordered=agents.filter(a=>selected.has(a.id)).slice().sort((a,b)=>{
 const ap=`${a.os}/${a.arch}`,bp=`${b.os}/${b.arch}`;return ap<bp?-1:ap>bp?1:a.id<b.id?-1:a.id>b.id?1:0;
 });const seen=new Set<string>();return ordered.filter(a=>{const platform=`${a.os}/${a.arch}`;if(seen.has(platform))return false;seen.add(platform);return true;});
}
export function canResume(r:Rollout){return r.state==='paused'&&!r.members.some(m=>['failed','rejected','unsupported','expired','unknown_result','rolled_back'].includes(m.status));}
export function controlResult(op:any):boolean{return op?.status==='completed'&&op.targets?.some((t:any)=>t.agent_id==='server'&&t.status==='succeeded'&&t.stage==='rollout.control_committed');}

export function rolloutSubmitNotice(op:any):string {
 const failure=op?.targets?.find((t:any)=>['failed','rejected','unsupported','expired','unknown_result','rolled_back'].includes(t.status));
 if(failure)return `Раскатка не начата: ${failure.message||'проверка выбранных машин не пройдена'}. Откройте операцию.`;
 if(op?.status==='running')return 'План сохранён. Следующие партии ждут подтверждения и наблюдения пробных машин.';
 return 'Запрос принят, подготовка плана ещё не подтверждена. Проверьте исходную операцию.';
}
