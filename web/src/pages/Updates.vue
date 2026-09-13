<script setup lang="ts">
import { ref } from "vue";
import { get } from "../api";
import { usePolling } from "../composables/usePolling";
const rows=ref<any[]>([]);
const {loading,error,refresh}=usePolling(async()=>{rows.value=(await get<any>('/api/v1/releases')).releases || [];});
</script>
<template><section class="panel"><h2>Обновления агентов</h2><p class="data-warning" role="status">Подписанные обновления обязательны для релиза, но текущий механизм не прошёл необходимые проверки безопасности. Импорт и активация заблокированы на сервере и в служебном IPC, а не только этими кнопками.</p><p>Не реализованы: независимый доверенный TUF root, проверка срока/версий метаданных, безопасная пробная установка, подтверждение новой версии, нативный откат и обновление самого service-host.</p><button disabled>Обновление недоступно</button> <router-link to="/agents">Состояние агентов</router-link></section><section class="panel"><h3>Ранее импортированные записи</h3><p v-if="loading">Загрузка…</p><p v-if="error" role="alert">{{ error }} <button @click="refresh">Повторить</button></p><p v-else-if="!rows.length&&!loading">Каталог пуст.</p><p v-for="r in rows" :key="r.id">{{ r.version }} · {{ r.imported_at }} · <span class="data-warning">Доверие требует повторной проверки новым механизмом</span></p></section></template>
