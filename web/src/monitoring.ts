export type Threshold = {metric:string;warning:number;critical:number;recovery:number;persist_seconds:number;recover_seconds:number};
export function thresholdError(rules:Threshold[]):string {
 if(rules.length!==3 || new Set(rules.map(r=>r.metric)).size!==3 || rules.some(r=>!['cpu','ram','disk'].includes(r.metric)))return 'Нужны ровно три правила: CPU, RAM и DISK.';
 for(const r of rules){
  if(![r.warning,r.critical,r.recovery].every(v=>typeof v==='number'&&Number.isFinite(v)) || !(r.warning>=1&&0<=r.recovery&&r.recovery<r.warning&&r.warning<r.critical&&r.critical<=100))return 'Пороги: 0 ≤ восстановление < предупреждение < критический ≤ 100.';
  if(![r.persist_seconds,r.recover_seconds].every(v=>Number.isInteger(v)&&v>=5&&v<=3600))return 'Выдержка и восстановление: целые секунды от 5 до 3600.';
 }
 return '';
}
export function maintenanceState(window:any,now:Date):string {
 const stamp=now.getTime(),start=Date.parse(window.start_at),end=Date.parse(window.end_at),cancel=Date.parse(window.cancelled_at);
 if(!Number.isFinite(stamp)||!Number.isFinite(start)||!Number.isFinite(end)||end<=start)return 'Неизвестно';
 if(Number.isFinite(cancel)&&stamp>=cancel)return 'Завершено вручную';
 if(stamp<start)return 'Запланировано';if(stamp>=end)return 'Завершено';return 'Активно';
}
export function acknowledgementFilter(value:unknown):string{return ['read','unread'].includes(String(value))?String(value):'all';}
