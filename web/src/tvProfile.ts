/** Versioned presentation preferences. No terminal input, credentials or agent settings. */
export type TVValue = { widths: number[]; density: string; autoplay: boolean };
export type TVProfile = { schema_version: number; revision: number; value: TVValue; updated_by: string; updated_at: string|null; last_request_id: string };
export type TVResponse = { controller_id: string; profile: TVProfile; saved?: boolean };
export const initialTVValue = (): TVValue => ({widths:[9.5,3.5,8,6,8],density:'10',autoplay:false});
const clone = (v:TVValue):TVValue => ({...v,widths:[...v.widths]});
const same = (a:TVValue,b:TVValue) => a.density===b.density && a.autoplay===b.autoplay && a.widths.every((n,i)=>n===b.widths[i]);
const idPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
export function validTVValue(v:any):v is TVValue {
 const minima=[6.5,3,5,4.5,5];
 return !!v && Object.keys(v).sort().join(',')==='autoplay,density,widths' && ['10','12','14'].includes(v.density) && typeof v.autoplay==='boolean' && Array.isArray(v.widths) && v.widths.length===5 && v.widths.every((n:unknown,i:number)=>typeof n==='number'&&Number.isFinite(n)&&n>=minima[i]&&n<=4096);
}
export function parseTVResponse(raw:any,controller:string):TVResponse {
 const p=raw?.profile;
 if(raw?.controller_id!==controller||!p||p.schema_version!==1||!Number.isSafeInteger(p.revision)||p.revision<0||p.revision>9007199254740990||!validTVValue(p.value)||typeof p.updated_by!=='string'||typeof p.last_request_id!=='string'||(p.revision>0&&(!idPattern.test(p.last_request_id)||typeof p.updated_at!=='string'||!Number.isFinite(Date.parse(p.updated_at))||!p.updated_by))||(p.revision===0&&(p.updated_at!==null||p.last_request_id!=='')))throw new Error('Сервер вернул некорректный общий ТВ-профиль. Настройки не применены.');
 return {...raw,profile:{...p,value:clone(p.value)}};
}
export type TVSyncPhase='loading'|'local'|'synced'|'editing'|'saving'|'conflict'|'unknown'|'error';
export type TVSyncState={profile:TVProfile|null;value:TVValue;phase:TVSyncPhase;message:string;canEdit:boolean;inFlight:boolean;controller:string};
type SaveBody={expected_revision:number;request_id:string;value:TVValue};
type IO={read:()=>Promise<unknown>;write:(body:SaveBody)=>Promise<unknown>;key:()=>string};

/** A device never republishes its cache on load/reconnect. Writes require a
 * displayed revision. Hints only reread. One in-flight write, no replay queue. */
export function createTVProfileSync(io:IO,publish:(s:TVSyncState)=>void){
 let state:TVSyncState={profile:null,value:initialTVValue(),phase:'loading',message:'Получаем общий ТВ-профиль…',canEdit:false,inFlight:false,controller:''};
 let epoch=0,identity='',reading:Promise<void>|null=null,editBase:number|null=null,pending:SaveBody|null=null;
 const emit=()=>publish({...state,value:clone(state.value),profile:state.profile?{...state.profile,value:clone(state.profile.value)}:null});
 const normal=()=>{state.phase=state.profile?.revision?'synced':'local';state.message=state.profile?.revision?`Общий ТВ-профиль · версия ${state.profile.revision}`:'Общий ТВ-профиль ещё не создан. Опубликуйте текущую раскладку с компьютера.';};
 const canChange=()=>!!identity&&!!state.profile&&state.canEdit&&!state.inFlight&&!['saving','conflict','unknown','error'].includes(state.phase);
 function accept(raw:unknown){
  const p=parseTVResponse(raw,state.controller).profile;
  if(state.profile&&p.revision<state.profile.revision)return;
  if(state.profile&&p.revision===state.profile.revision&&(p.last_request_id!==state.profile.last_request_id||!same(p.value,state.profile.value)))throw new Error('Одна ревизия содержит разные настройки. Обновите соединение с контроллером.');
  state.profile=p;
  if(pending){
   if(p.last_request_id===pending.request_id&&p.revision===pending.expected_revision+1&&same(p.value,pending.value)){pending=null;editBase=null;state.value=clone(p.value);normal();}
   else if(p.revision>pending.expected_revision){state.phase='conflict';state.message='Профиль изменён на другом устройстве. Загрузите актуальную версию перед новой правкой.';}
   return;
  }
  if(editBase!==null||state.phase==='conflict'||state.phase==='unknown')return;
  if(p.revision>0)state.value=clone(p.value); // Rev 0 preserves the old device-local draft until an explicit publish.
  normal();
 }
 async function read(){
  if(!identity)return;
  if(reading)return reading;
  const generation=epoch;
  const task=(async()=>{try{const data=await io.read();if(generation!==epoch)return;accept(data);emit();}catch(e){if(generation!==epoch)return;if(!['editing','saving','conflict','unknown'].includes(state.phase))state.phase='error';state.message=(e as Error)?.message||'Не удалось синхронизировать ТВ-профиль. Последние настройки сохранены на экране.';emit();}})();
  reading=task;try{await task;}finally{if(reading===task)reading=null;}
 }
 function start(key:string,controller:string,writable:boolean,local=initialTVValue()){
  if(key===identity){state.canEdit=writable;emit();return;}
  epoch++;identity=key;reading=null;editBase=null;pending=null;
  state={profile:null,value:validTVValue(local)?clone(local):initialTVValue(),phase:'loading',message:'Получаем общий ТВ-профиль…',canEdit:writable,inFlight:false,controller};emit();void read();
 }
 function stop(){epoch++;identity='';reading=null;editBase=null;pending=null;state={profile:null,value:initialTVValue(),phase:'loading',message:'',canEdit:false,inFlight:false,controller:''};emit();}
 function begin(){if(!canChange())return false;if(editBase===null)editBase=state.profile!.revision;state.phase='editing';state.message='Предпросмотр на этом экране. Изменения отправятся после завершения настройки.';emit();return true;}
 function cancel(){
  if(state.inFlight)return;
  pending=null;editBase=null;if(state.profile?.revision)state.value=clone(state.profile.value);normal();emit();
 }
 async function commit(value:TVValue){
  if(!validTVValue(value)||!canChange())return false;
  if(editBase===null)editBase=state.profile!.revision;
  state.value=clone(value);
  if(state.profile!.revision!==editBase){state.phase='conflict';state.message='Во время настройки профиль изменён с другого устройства. Черновик не отправлен. Загрузите общий профиль.';emit();return false;}
  let key:string;try{key=io.key();if(!idPattern.test(key))throw new Error();}catch{editBase=null;state.phase='error';state.message='Не удалось создать идентификатор сохранения. Запрос не отправлен.';emit();return false;}
  const body:SaveBody={expected_revision:editBase,request_id:key,value:clone(value)};
  pending=body;state.inFlight=true;state.phase='saving';state.message='Сохраняем общий ТВ-профиль…';emit();const generation=epoch;
  try{
   const response=parseTVResponse(await io.write(body),state.controller);if(generation!==epoch)return false;
   if(response.saved!==true||response.profile.last_request_id!==body.request_id||response.profile.revision!==body.expected_revision+1||!same(response.profile.value,body.value))throw new Error('Сохранение не подтверждено. Проверяем профиль.');
   accept(response);emit();return pending===null;
  }catch(e){
   if(generation!==epoch)return false;
   if(state.profile?.last_request_id===body.request_id&&same(state.profile.value,body.value)){pending=null;editBase=null;normal();emit();return true;}
   const status=Number((e as any)?.status);
   if(status>=400&&status<500&&status!==409){pending=null;state.phase='error';state.message=(e as Error)?.message||'Сохранение отклонено. Загрузите общий профиль.';emit();return false;}
   state.phase=status===409?'conflict':'unknown';state.message=status===409?'Профиль уже изменён. Загрузите общий профиль перед новой правкой.':'Ответ на сохранение не получен. Проверяем результат, без повторной отправки.';emit();
   // A fresh read, not a possible pre-write GET already in flight. Never retry POST.
   try{const data=await io.read();if(generation!==epoch)return false;accept(data);}catch{/* keep unknown and the local draft */}
   if(pending&&state.phase==='unknown')state.message='Сохранение не подтверждено. На других экранах могут быть прежние настройки. Перечитайте общий профиль.';
   emit();return !pending;
  }finally{if(generation===epoch){state.inFlight=false;emit();}}
 }
 async function update(patch:Partial<TVValue>){if(!begin())return false;return commit({...state.value,...patch,widths:patch.widths?[...patch.widths]:[...state.value.widths]});}
 async function adopt(){
  // Explicitly discard local edits. A still-running mutation cannot be discarded.
  if(state.inFlight)return;
  pending=null;editBase=null;if(state.profile?.revision)state.value=clone(state.profile.value);normal();emit();await read();
 }
 emit();return{start,stop,read,begin,cancel,commit,update,adopt,canChange,get value(){return clone(state.value);}};
}
