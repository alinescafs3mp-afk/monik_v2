<script setup lang="ts">
import {computed,nextTick,onBeforeUnmount,ref,watch} from 'vue';
import {useRoute,onBeforeRouteLeave} from 'vue-router';
import {get,submitOp} from '../api';
import {loadCheck,buildCheck,applyPreset,serviceHint,requestPreview,checkOperationFeedback,type CheckForm} from '../checkDraft';
const props=defineProps<{detail:any;readonly?:boolean}>();
const emit=defineEmits<{toast:[string,string?];refresh:[]}>();const route=useRoute();
const root=ref<HTMLElement|null>(null);
const selected=ref(''),f=ref<CheckForm>(loadCheck()),dirty=ref(false),pending=ref(''),error=ref(''),status=ref(''),operation=ref<any>(null),secretList=ref<any[]>([]),baseRevision=ref(0),draftID=ref('');
let timer:ReturnType<typeof setTimeout>|undefined,disposed=false,watchGeneration=0;
const services=computed<any[]>(()=>props.detail?.services||[]);
const service=computed(()=>services.value.find(s=>s.id===selected.value));
const capabilities=computed(()=>{const c=props.detail?.agent?.capabilities;try{return typeof c==='string'?JSON.parse(c):c||{}}catch{return {}}});
const supported=computed(()=>capabilities.value.http_custom_v1?.status==='supported');
const drift=computed(()=>dirty.value&&baseRevision.value!==props.detail?.agent?.desired_revision);
const suggestions=computed<any[]>(()=>service.value?.discovery?.suggestions||[]);
const secrets=computed(()=>secretList.value.filter(s=>s.agent_id===props.detail?.agent?.id&&(!s.check_id||s.check_id===f.value.id)));
const evidence=computed(()=>operation.value?.targets?.[0]?.evidence);
const opURL=computed(()=>operation.value?.operation_id?`/operations/${operation.value.operation_id}`:'');
async function loadSecrets(){try{const r:any=await get('/api/v1/secrets');secretList.value=r.secrets||[]}catch{error.value='Не удалось прочитать список секретов. Повторите чтение.'}}
function reset(id=selected.value){const d=props.detail?.desired_config?.checks?.find((c:any)=>c.service_id===id)||{};const s=services.value.find(s=>s.id===id)||{};selected.value=id;f.value=loadCheck(d,s);draftID.value=d.id||crypto.randomUUID();baseRevision.value=props.detail.agent.desired_revision;dirty.value=false;status.value='';error.value='';}
function choose(event:Event){const id=(event.target as HTMLSelectElement).value;if(dirty.value&&!confirm('Отменить несохранённые изменения запроса?')){(event.target as HTMLSelectElement).value=selected.value;return;}reset(id);}
watch(()=>[props.detail?.agent?.id,services.value.length],()=>{if(!selected.value&&services.value.length){reset(services.value.some(s=>s.id===route.query.service)?String(route.query.service):services.value[0].id);void loadSecrets();}},{immediate:true});
function focusEditor(){root.value?.scrollIntoView({block:'start',behavior:'auto'});root.value?.querySelector<HTMLElement>('h3')?.focus({preventScroll:true});}
function openService(id:string):boolean{
 if(!services.value.some(s=>s.id===id)){error.value='Этот сервис не найден в текущем инвентаре.';return false;}
 if(pending.value&&id!==selected.value){error.value='Сначала дождитесь результата текущей проверки.';focusEditor();return false;}
 if(id!==selected.value){if(dirty.value&&!confirm('Отменить несохранённые изменения запроса?'))return false;reset(id);}
 return true;
}
defineExpose({openService,focusEditor});
watch(()=>[route.query.service,services.value.length],async()=>{
 const id=String(route.query.service||'');
 if(id&&services.value.some(s=>s.id===id)&&openService(id)){await nextTick();focusEditor();}
},{immediate:true,flush:'post'});
watch(()=>props.detail?.agent?.desired_revision,()=>{if(selected.value&&!dirty.value&&!pending.value)reset();});
function preset(value:string){f.value=applyPreset(f.value,value);dirty.value=true;}
function useSuggestion(s:any){if(dirty.value&&!confirm('Заменить несохранённый черновик рекомендацией?'))return;f.value=loadCheck({...s.definition,id:f.value.id,service_id:selected.value,paused:f.value.paused,ignored:f.value.ignored},service.value);dirty.value=true;status.value='Рекомендация загружена в черновик. Пробный запрос и сохранение выполняются отдельно.';}
function secretChange(){f.value.secretHeader=secrets.value.find(s=>s.id===f.value.secretID)?.header||'';dirty.value=true;}
async function poll(id:string,generation:number,until:number){if(disposed||generation!==watchGeneration)return;try{const op:any=await get(`/api/v1/operations/${encodeURIComponent(id)}`);if(disposed||generation!==watchGeneration)return;operation.value=op;
 const feedback=checkOperationFeedback(op);status.value=feedback.message;
 if(feedback.done){pending.value='';emit('refresh');return;}
 }catch(e){error.value=(e as Error).message;}
 if(Date.now()<until){timer=setTimeout(()=>void poll(id,generation,until),1500);}else{pending.value='';status.value='Агент ещё не подтвердил результат. Операция сохранена в Центре операций, повтор автоматически не отправлен.';}}
async function execute(trial:boolean){if(pending.value||props.readonly)return;error.value='';status.value='';try{const d=buildCheck(f.value,draftID.value);pending.value=trial?'trial':'save';const rev=baseRevision.value;
 const r=await submitOp(trial?'check.trial':'check.apply',trial?d:{check:d,base_revision:rev},[props.detail.agent.id]);operation.value=r.op;status.value='Операция сохранена сервером. Ожидаем агент.';
 if(!trial){dirty.value=false;f.value.id=String(d.id);baseRevision.value=rev+1;emit('refresh');}
 emit('toast',status.value,opURL.value);clearTimeout(timer);void poll(String(r.op.operation_id),++watchGeneration,Date.now()+90000);
 }catch(e){error.value=(e as Error).message;pending.value='';}}
function beforeUnload(event:BeforeUnloadEvent){if(dirty.value){event.preventDefault();event.returnValue='';}}
window.addEventListener('beforeunload',beforeUnload);
onBeforeRouteLeave(()=>!dirty.value||confirm('Покинуть страницу без сохранения запроса?'));
onBeforeUnmount(()=>{window.removeEventListener('beforeunload',beforeUnload);disposed=true;++watchGeneration;clearTimeout(timer);});
</script>
<template>
<section ref="root" id="check-editor" class="panel check-editor">
 <h3 tabindex="-1">Кастомный запрос к сервису</h3>
 <p>Один шаблон используется и для пробного запроса, и для периодического мониторинга. Запрос выполняется на агенте, не в браузере и не на контроллере.</p>
 <p v-if="!supported" class="data-warning" role="status">Сначала обновите агент до версии с http_custom_v1 и дождитесь его отчёта. Иначе прежний агент может не понять новые поля.</p>
 <label>Сервис <select :value="selected" :disabled="!!pending" @change="choose"><option value="">Выберите сервис</option><option v-for="s in services" :value="s.id" :key="s.id">{{s.display_name||s.url}}</option></select></label>
 <p v-if="serviceHint(service)" class="data-warning">{{serviceHint(service)}}</p>
 <div v-if="suggestions.length" class="advice"><h4>Агент предлагает проверить</h4><p class="muted">Это результаты последнего ограниченного обнаружения, не текущее здоровье. Автоопределение не заменяет уже настроенную проверку.</p>
 <div v-for="s in suggestions" :key="s.definition.path" class="panel"><strong>{{s.definition.method}} {{s.definition.path||'/'}}</strong> · HTTP {{s.http_status}} · {{s.health}}<p>{{s.reason}}</p><small>{{new Date(s.observed_at).toLocaleString()}} · {{s.confidence}} · {{s.auto_eligible?'Проверен отдельный маршрут':'Нужно проверить вручную'}}</small> <button :disabled="!!pending||readonly" @click="useSuggestion(s)">Взять в черновик</button></div></div>
 <form @submit.prevent="execute(false)" @input="dirty=true" @change="dirty=true">
 <fieldset :disabled="!!pending||readonly||!selected||!supported">
 <label>Готовый шаблон <select value="" @change="preset(($event.target as HTMLSelectElement).value)"><option value="">Выберите, затем проверьте настройки</option><option value="ready">Readiness с явным статусом</option><option value="health">Health с явным статусом</option><option value="spring">Spring Actuator</option><option value="http200">Документированный GET /health → 200</option><option value="rpc">JSON-RPC: пример health (POST)</option></select></label>
 <div class="check-grid"><label>URL сервиса <input v-model="f.url" autocomplete="off" required aria-label="URL сервиса"/></label><label>Путь и query <input v-model="f.path" placeholder="/health?detail=brief" autocomplete="off" aria-label="Путь и query"/></label>
 <label>Метод <select v-model="f.method" aria-label="Метод"><option>GET</option><option>HEAD</option><option>OPTIONS</option><option>POST</option></select></label><label>Интервал, с <input v-model.number="f.interval" type="number" min="5" max="3600" step="5" aria-label="Интервал, с"/></label><label>Таймаут, с <input v-model.number="f.timeout" type="number" min="1" max="30" aria-label="Таймаут, с"/></label>
 <label>Назначение <select v-model="f.purpose"><option value="responsiveness">HTTP отвечает</option><option value="readiness">Готовность к работе</option><option value="liveness">Работоспособность процесса</option><option value="application">Прикладное здоровье</option></select></label></div>
 <details><summary>Адрес соединения, Host и TLS</summary><p class="muted">Для виртуального хоста: локальный IP:port задаётся отдельно от Host/SNI. Доступ к произвольной удалённой сети не включается.</p><div class="check-grid"><label>Локальный IP:port <input v-model="f.dial" placeholder="127.0.0.1:8080"/></label><label>Host <input v-model="f.host" placeholder="app.internal"/></label><label>TLS Server Name <input v-model="f.sni" placeholder="app.internal"/></label></div><p v-if="f.insecureTLS" class="err">Для этой существующей проверки выключено подтверждение TLS. Автоопределение так не делает.</p><button v-if="f.insecureTLS" type="button" @click="f.insecureTLS=false;dirty=true">Вернуть проверку TLS</button></details>
 <label>Открытые заголовки, JSON <textarea v-model="f.headers" rows="4" spellcheck="false" placeholder='{"Accept":"application/json"}' aria-label="Открытые заголовки, JSON"/></label>
 <p class="muted">Без паролей и токенов. Открытые шаблоны видны в конфигурации; для чувствительных значений используйте секрет.</p>
 <label>Секрет заголовка <select v-model="f.secretID" @change="secretChange"><option value="">Не использовать</option><option v-for="s in secrets.filter(s=>s.header!=='Body')" :key="s.id" :value="s.id">{{s.name}} → {{s.header}} · v{{s.version}}</option></select></label><button type="button" @click="loadSecrets">Обновить список секретов</button>
 <template v-if="f.method==='POST'"><label class="post-consent"><input v-model="f.allowPOST" type="checkbox"/> Я проверил, что этот POST безопасен для периодического вызова: не создаёт задачи, не меняет состояние и не запускает платные или тяжёлые операции.</label><p class="data-warning">Monik не способен доказать отсутствие побочных эффектов POST. Автоподбор никогда не отправляет POST.</p>
 <label>Открытое тело запроса, до 16 КиБ <textarea v-model="f.body" rows="5" spellcheck="false" :disabled="!!f.bodySecretID" aria-label="Открытое тело запроса, до 16 КиБ"/></label><label>Либо секрет всего тела <select v-model="f.bodySecretID" @change="f.bodySecretID&&(f.body='')"><option value="">Открытое тело</option><option v-for="s in secrets" :key="s.id" :value="s.id">{{s.name}} · v{{s.version}}</option></select></label></template>
 <p class="panel request-preview"><strong>Будет отправлено:</strong> {{requestPreview(f)}}<br/><small>Соединение: {{f.dial||'из URL'}} · каждые {{f.interval}} с · таймаут {{f.timeout}} с</small></p>
 <h4>Когда считать проверку успешной</h4><label>Проверять <select v-model="f.kind"><option value="baseline_http">Только ответ HTTP, без заявления о здоровье</option><option value="http_health">Все заданные условия ответа</option></select></label>
 <div v-if="f.kind!=='baseline_http'" class="check-grid"><label>Ожидаемые HTTP-коды <input v-model="f.expectStatus" placeholder="200,204"/></label><label>Максимальная задержка, мс <input v-model="f.latency" placeholder="Не ограничивать"/></label><label>Подстрока ответа <input v-model="f.expectText"/></label><label>Поле JSON / JSON Pointer <input v-model="f.jsonPath" placeholder="result.status или /checks/0/ok"/></label><label>Тип JSON <select v-model="f.jsonType"><option value="">Совместимое сравнение старой проверки</option><option value="string">Строка</option><option value="boolean">Логическое</option><option value="number">Число</option><option value="exists">Поле существует</option><option value="null">null</option></select></label><label>Значение JSON <input v-model="f.jsonValue" :disabled="['exists','null'].includes(f.jsonType)"/></label><label><input v-model="f.expectHealth" type="checkbox"/> Требовать явный положительный health-статус в JSON/text</label></div>
 <p v-if="f.paused||f.ignored" class="data-warning">Проверка сейчас {{f.ignored?'игнорируется':'на паузе'}}. Сохранение шаблона не включит периодические запросы. Пробный запрос выполняется только по отдельному нажатию.</p>
 <p v-if="drift" class="data-warning">Конфигурация уже изменилась. Черновик сохранён на экране, но сохранение будет отклонено до сверки.</p>
 <div class="row"><button type="button" @click="execute(true)">Пробный запрос</button><button type="submit" :disabled="drift">Сохранить проверку</button><button type="button" @click="reset()">Перечитать сохранённое</button><span v-if="dirty" class="muted">Есть несохранённые изменения</span></div>
 </fieldset>
 </form>
 <p v-if="pending" role="status">{{pending==='trial'?'Выполняется пробный запрос':'Ожидаем применения'}}…</p>
 <p v-if="error" class="err" role="alert">{{error}}</p><p v-if="status" role="status">{{status}} <router-link v-if="opURL" :to="opURL">Открыть операцию</router-link></p>
 <div v-if="evidence?.trial && evidence?.vantage" class="trial-result panel"><h4>Результат пробного запроса</h4><dl class="metric-grid"><div><dt>Соединение</dt><dd>{{evidence.transport}}</dd></div><div><dt>HTTP</dt><dd>{{evidence.http_status??'Нет ответа'}}</dd></div><div><dt>Длительность</dt><dd>{{evidence.latency_ms==null?'Нет измерения':`${Number(evidence.latency_ms).toFixed(1)} мс`}}</dd></div><div><dt>Условия</dt><dd>{{evidence.app_result}}</dd></div></dl><p>{{evidence.app_reason}}</p><p>{{serviceHint(evidence)}}</p><p v-if="evidence.feedback?.health">Сервис сообщил: {{evidence.feedback.health}}</p><p class="muted">Успешное выполнение пробного запроса не означает, что сам сервис здоров. Полное тело и секреты не сохраняются.</p></div>
</section>
</template>
<style scoped>
.check-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(210px,1fr));gap:12px}.check-editor label{display:flex;flex-direction:column;gap:6px;margin:12px 0}.check-editor textarea{width:100%;font-family:ui-monospace,monospace;min-height:80px;resize:vertical}.check-editor fieldset{border:0;padding:0;min-width:0}.check-editor input[type=checkbox]{width:auto;align-self:flex-start}.check-editor details{margin:16px 0}.post-consent{font-weight:600}.trial-result,.request-preview{overflow-wrap:anywhere}.advice .panel{padding:10px}
</style>
