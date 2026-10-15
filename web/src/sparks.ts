// Data-only presentation and anti-stale bindings. Server and local helper authorize effects.
export type Quality = 'ok'|'unsupported'|'permission_denied'|'unknown'|'stale'|'error';
export interface Observation { value:number|null;unit:string;source:string;observed_at:string;quality:Quality;reason?:string }
export interface Metric {metric_id:string;observation:Observation}
export type ModelAction='start'|'stop'|'restart';
export interface Profile {
 profile_id:string;display_name:string;profile_revision:string;engine:string;supervisor:string;
 control_allowed:boolean;control_reason:string;configured_model_ref:string;allowed_actions:ModelAction[];log_allowed?:boolean;
 settings:Array<{name:string;value:string|number|boolean|null;provenance:string}>;
 engine_version?:string|null;supervisor_version?:string|null;endpoint_hint?:string|null;model_aliases?:string[];
}
export interface ModelState {profile_id:string;generation:number;observed_at:string;supervisor_state:string;readiness:string;quality:Quality;reason?:string}
export interface SparkPayload {
 schema_version:number;memory_topology:string;metrics:Metric[];
 inventory?:{revision:string;complete:boolean;observed_at:string;profiles:Profile[];removed_profile_ids?:string[]};
 model_states?:ModelState[];control_state?:{enabled:boolean;epoch:string|null;observed_at:string;reason:string};
}
export interface SparkSummary {
 agent_id:string;display_name:string;hostname:string;online:boolean;agent_session:string;
 received_at:string;observed_at:string;memory_topology:string;metrics:Metric[];classification:string;
 profiles:Array<{profile_id:string;display_name:string;engine:string;supervisor:string;state:string;readiness:string;quality:Quality;generation:number;observed_at:string;reason?:string}>;
 host?:{disks?:Array<{mount:string;fs:string;total_bytes:number;used_bytes:number;available_bytes:number}>};host_observed_at?:string;
}
export interface SparkDetail {agent_id:string;agent_session:string;online:boolean;received_at:string;observed_at:string;classification:string;spark:SparkPayload}
export function supervisorLabel(value:string){return value==='existing-coordinator'?'Существующий контроллер Spark':value;}
export const qualityLabel:Record<Quality,string>={ok:'Актуально',unsupported:'Не поддерживается',permission_denied:'Нет доступа',unknown:'Нет данных',stale:'Устаревшие данные',error:'Ошибка сбора'};
export const errorLabel:Readonly<Record<string,string>>={
 forbidden:'Недостаточно прав',control_disabled:'Управление не разрешено локально',local_control_not_enabled:'Управление не разрешено локально',
 target_untrusted:'Локальное разрешение цели требует проверки',control_fence_changed:'Полномочия управления изменились',
 profile_changed:'Профиль изменился: обновите данные',instance_changed:'Экземпляр модели изменился',
 expired:'Команда истекла и не будет выполнена',state_stale:'Нет свежего состояния машины',supervisor_unknown:'Не подтверждено состояние процесса',
 transition_busy:'Уже выполняется действие с этой моделью',weights_missing:'Не найдены нужные файлы модели',engine_missing:'Не найден настроенный движок',
 port_conflict:'Нужный порт занят другим сервисом',resource_conflict:'Конфликт с разрешённой группой моделей',memory_insufficient:'Недостаточно памяти по проверенным требованиям',
 required_disk_unavailable:'Недоступно необходимое хранилище',memory_estimate_unknown:'Требование модели к памяти пока не измерено',
 confirmation_required:'Подтвердите действие, прерывающее работу модели',not_running_use_start:'Модель остановлена: используйте «Старт»',
 already_running:'Модель уже запущена',already_stopped:'Модель уже остановлена',unknown_result:'Результат уточняется, повторного запуска нет',
 observation_only:'Доступен просмотр; управление не разрешено',gb10_unified_memory_no_dedicated_framebuffer:'UMA: отдельный объём видеопамяти не предоставляется',active_helper_cpu_not_yet_reaped:'CPU работающих вспомогательных процессов ещё уточняется',unified_memory_no_dedicated_framebuffer:'UMA: отдельный объём видеопамяти не предоставляется',permission_denied:'Нет доступа к источнику',
 logs_disabled:'Логи не разрешены локально',log_gap:'Часть записей пропущена',
 configured_catalog_runtime_unobserved:'Профиль настроен; состояние запуска и локальное управление ещё не подключены',
 failed_service_shutdown_verified:'Служба завершилась с ошибкой; остановка всех процессов подтверждена',
};
export function explainCode(code:string){return errorLabel[code]||code||'Причина не сообщена';}
export function freshAt(value:string|undefined,now=Date.now(),seconds=30){const at=Date.parse(value||'');return Number.isFinite(at)&&at<=now+5000&&now-at<=seconds*1000;}
const sparkObservationFreshnessSeconds=45;
export function metricLabel(id:string){
 const known:Record<string,string>={'cpu.usage_percent':'CPU','cpu.load1':'Средняя нагрузка за минуту','memory.total_bytes':'Общая физическая память','memory.available_bytes':'Доступно памяти (MemAvailable)',
 'memory.pressure_used_bytes':'Занято (MemTotal − MemAvailable)','memory.swap_total_bytes':'Всего swap','memory.swap_free_bytes':'Свободно swap',
 'memory.psi.some.avg10':'Ожидание памяти PSI some (10 с)','memory.psi.full.avg10':'Ожидание памяти PSI full (10 с)',
 'agent.goroutines':'Горутины агента','agent.fd_count':'Открытые файлы агента','agent.cpu_core_percent':'CPU агента (% одного ядра)',
 'host.cpu.utilization_percent':'CPU','host.memory.total_bytes':'Общая физическая память','host.memory.available_bytes':'Доступно памяти (MemAvailable)',
 'host.memory.used_bytes':'Занято памяти','host.memory.cached_bytes':'Кэш памяти','host.memory.swap_used_bytes':'Занято swap','host.memory.swap_total_bytes':'Всего swap',
 'agent.rss_bytes':'RSS агента','agent.pss_bytes':'PSS агента','agent.cpu_percent':'CPU агента'};
 if(known[id])return known[id];
 const match=id.match(/^gpu\.(\d+)\.(.+)$/);
 if(match){const labels:Record<string,string>={utilization_percent:'Загрузка GPU',memory_interface_percent:'Активность интерфейса памяти GPU',memory_utilization_percent:'Активность интерфейса памяти GPU',temperature_celsius:'Температура GPU',power_watts:'Мощность GPU/SoC',framebuffer_used_bytes:'Память GPU по драйверу',framebuffer_total_bytes:'Объём памяти GPU по драйверу',clock_mhz:'Частота GPU',clock_sm_mhz:'Частота SM GPU',clock_memory_mhz:'Частота памяти GPU'};return `${labels[match[2]]||match[2]} · GPU ${match[1]}`;}
 return id;
}
export function formatObservation(o:Observation,now=Date.now(),online=true){
 if(o.value===null||!Number.isFinite(o.value)||!['ok','stale'].includes(o.quality))return qualityLabel[o.quality]||'Нет данных';
 const n=new Intl.NumberFormat('ru-RU',{maximumFractionDigits:2});
 const value=o.unit==='bytes'?`${n.format(o.value/1073741824)} GiB`: `${n.format(o.value)} ${(['percent','percent_one_core'].includes(o.unit)?'%':o.unit==='celsius'?'°C':o.unit==='watts'?'Вт':o.unit==='count'?'шт.':o.unit==='bytes/second'?'Б/с':o.unit)}`;
 return value+(o.quality==='stale'||!online||!freshAt(o.observed_at,now)?' (устарело)':'');
}
export function modelLabel(s:Pick<ModelState,'supervisor_state'|'readiness'|'quality'|'observed_at'|'reason'>|undefined,online=true,now=Date.now()){
 if(!s)return 'Состояние не получено';
 if(s.reason==='configured_catalog_runtime_unobserved')return 'Профиль настроен · состояние запуска не наблюдается'+(!online?' · машина не на связи':'');
 const labels:Record<string,string>={stopped:'Остановлена',starting:'Запускается',stopping:'Останавливается',failed:'Ошибка процесса',unknown:'Состояние процесса неизвестно'};
 const text=s.supervisor_state==='stopped'&&s.reason==='start_preflight_pending'?'Остановлена · проверка перед запуском':s.supervisor_state==='running'?(s.readiness==='ready'?'Готова к запросам':s.readiness==='unready'?'Работает, к запросам не готова':'Работает, готовность не подтверждена'):(labels[s.supervisor_state]||'Состояние неизвестно');
 return text+(!online||s.quality!=='ok'||!freshAt(s.observed_at,now,sparkObservationFreshnessSeconds)?' · последнее наблюдение':'');
}
export function actionReason(d:SparkDetail|null,p:Profile,s:ModelState|undefined,action:ModelAction,owner:boolean,now=Date.now(),pending=false){
 if(!owner)return 'Управление доступно владельцу';
 if(!d?.online)return 'Машина не на связи. Команда не будет поставлена в очередь';
 if(p.control_reason==='configured_catalog_runtime_unobserved')return explainCode(p.control_reason);
 if(!freshAt(d.received_at,now)||!freshAt(d.spark.control_state?.observed_at,now,sparkObservationFreshnessSeconds)||!freshAt(s?.observed_at,now,sparkObservationFreshnessSeconds)||s?.quality!=='ok')return explainCode('state_stale');
 if(!p.control_allowed||!d.spark.control_state?.enabled)return explainCode(p.control_reason||d.spark.control_state?.reason||'control_disabled');
 if(!p.allowed_actions.includes(action))return 'Это действие не разрешено локальным профилем';
 if(!d.agent_session||!d.spark.control_state.epoch||!/^sha256:[0-9a-f]{64}$/.test(p.profile_revision)||!Number.isSafeInteger(s?.generation)||s!.generation<0)return 'Нет подтверждённой привязки к профилю и экземпляру';
 if(pending)return 'Запрос уже отправляется';
 if(!s||s.supervisor_state==='unknown')return explainCode('supervisor_unknown');
 if(action==='start'&&s.reason==='start_preflight_pending')return 'Запуск уже проходит проверку. Его можно остановить';
 if(action==='start'&&s.supervisor_state!=='stopped'&&!(s.supervisor_state==='failed'&&s.reason==='failed_service_shutdown_verified'))return s.supervisor_state==='failed'?'Не подтверждена остановка экземпляра. Обновите состояние':explainCode(s.supervisor_state==='running'?'already_running':'transition_busy');
 if(action==='stop'&&(s.supervisor_state==='stopping'||s.supervisor_state==='stopped'&&s.reason!=='start_preflight_pending'))return explainCode(s.supervisor_state==='stopped'?'already_stopped':'transition_busy');
 if(action==='restart'&&s.supervisor_state!=='running')return explainCode(s.supervisor_state==='stopped'?'not_running_use_start':'transition_busy');
 return '';
}
export function modelParams(d:SparkDetail,p:Profile,s:ModelState,confirmDisruption:boolean){return {profile_id:p.profile_id,profile_revision:p.profile_revision,expected_generation:s.generation,control_epoch:d.spark.control_state!.epoch,expected_agent_session:d.agent_session,confirm_disruption:confirmDisruption};}
export interface LogRecord {sequence:number;at:string;text:string;gap?:boolean}
export function boundedLogs(existing:LogRecord[],incoming:LogRecord[],maxRecords=600,maxChars=180000){
 const bySequence=new Map(existing.map(r=>[r.sequence,r]));
 for(const r of incoming.slice(-maxRecords))if(Number.isSafeInteger(r.sequence)&&r.sequence>=0&&typeof r.text==='string')bySequence.set(r.sequence,{...r,text:r.text.replace(/\x1b\[[0-?]*[ -/]*[@-~]/g,'').replace(/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/g,'').slice(0,8192)});
 const rows=[...bySequence.values()].sort((a,b)=>a.sequence-b.sequence).slice(-maxRecords);let chars=rows.reduce((n,r)=>n+r.text.length,0);
 while(rows.length&&chars>maxChars)chars-=rows.shift()!.text.length;
 return {rows,truncated:bySequence.size>rows.length};
}
