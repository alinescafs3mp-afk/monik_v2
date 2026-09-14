<script setup lang="ts">
import SessionPanel from "../components/SessionPanel.vue";
import {ref} from 'vue';import {get,submitOp} from '../api';import {usePolling} from '../composables/usePolling';import {bytes} from '../format';
import RuleEditor from '../components/RuleEditor.vue';import MaintenancePanel from '../components/MaintenancePanel.vue';import PasswordForm from '../components/PasswordForm.vue';
const emit=defineEmits<{toast:[string,string?]}>();
const settings=ref<any>(null),diag=ref<any>(null),pending=ref(false);
const {loading,error,refresh}=usePolling(async()=>{const [s,d]=await Promise.all([get('/api/v1/settings'),get('/api/v1/diagnostics')]);settings.value=s;diag.value=d;});
async function backup(){if(pending.value)return;pending.value=true;try{const r=await submitOp('backup.create',{});emit('toast','Согласованный снимок базы создан. Это не полная копия контроллера.',`/operations/${r.op?.operation_id}`);}catch{/* global feedback */}finally{pending.value=false;}}
</script>
<template><div>
 <p v-if="error" class="panel err" role="alert">{{error}} <button @click="refresh">Повторить чтение</button></p><p v-if="loading" role="status">Читаем настройки…</p>
 <RuleEditor/><MaintenancePanel/>
 <section class="panel"><h2>Контроллер и диагностика</h2><p>Адрес: <code>{{settings?.advertised_url}}</code></p><p>Версия: {{diag?.version}} · <code>{{diag?.commit}}</code></p><p>TLS действует до: {{settings?.tls_leaf_expiry}}</p>
 <div class="diagnostic-grid"><div><strong>{{bytes(diag?.database_bytes)}}</strong><small>База SQLite</small></div><div><strong>{{bytes(diag?.wal_bytes)}}</strong><small>Журнал WAL</small></div><div><strong>{{diag?.agents_with_configuration_lag ?? 'Нет данных'}}</strong><small>Агентов ожидают конфигурацию</small></div></div>
 <p>SQLite: {{diag?.journal_mode}} / synchronous={{diag?.synchronous}} (2 = FULL). Цель сырой истории: {{diag?.retention_target_hours}} ч.</p>
 <p v-if="diag?.host_samples?.retention_lag_seconds>0||diag?.service_observations?.retention_lag_seconds>0" class="data-warning">Очистка истории догоняет накопленные данные небольшими порциями.</p>
 <details><summary>Границы данных и технические сведения</summary><pre>{{JSON.stringify(diag,null,2)}}</pre><p class="muted">Первая и последняя запись не доказывают, что между ними нет пропусков. Длительные агрегаты пока не реализованы.</p></details>
 <details><summary>Доверие и параметры подключения</summary><p>Listen: {{settings?.listen}}</p><p>ID: {{settings?.controller_id}}</p><p>Корень обновлений: {{settings?.tuf_root_enrolled?'Настроен':'Не настроен'}}</p><pre>{{settings?.ca_cert_pem}}</pre></details>
 <p class="data-warning">Снимок SQLite не включает полный набор TLS, ключей, настроек и подтверждённого восстановления контроллера. Сохраняйте защищённый каталог отдельно; проверка полного restore ещё требуется.</p><button :disabled="pending||!settings" @click="backup">{{pending?'Создаём снимок…':'Создать снимок базы'}}</button>
 </section><SessionPanel/><PasswordForm/>
</div></template>
