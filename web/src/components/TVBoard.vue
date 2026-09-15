<script setup lang="ts">
import ServiceList from './ServiceList.vue';
import {computed,nextTick,onMounted,onUnmounted,onUpdated,ref,watch} from 'vue';
import {pingDetail,bytes,number,stateLabel} from '../format';
import {worstDisk,overviewPriority} from '../presentation';
import {boardCapacity,pageSlice} from '../display';
const props=defineProps<{cards:any[];fresh:(c:any)=>boolean}>();
const board=ref<HTMLElement|null>(null),size=ref(4),page=ref(0),autoplay=ref(false),hovered=ref(false),now=ref(Date.now());
const screen=computed(()=>pageSlice(props.cards,page.value,size.value));
let largestRow=0,lastWidth=0,lastFont="";
let frame=0,observer:ResizeObserver|null=null,timer=0,lastPage=Date.now();
function measure(){
 if(frame)return;frame=requestAnimationFrame(()=>{
  frame=0;const el=board.value;if(!el)return;
  const font=getComputedStyle(document.documentElement).fontSize;
  if(lastWidth!==window.innerWidth||lastFont!==font){largestRow=0;lastWidth=window.innerWidth;lastFont=font;}
  const heights=Array.from(el.querySelectorAll('.tv-machine')).map(row=>row.getBoundingClientRect().height);
  largestRow=Math.max(largestRow,...heights);const rowHeight=largestRow||84;
  const head=el.querySelector('.tv-columns')?.getBoundingClientRect().height||24;
  size.value=boardCapacity(window.innerHeight,el.getBoundingClientRect().top+head,rowHeight+4);
 });
}
function turn(delta:number){page.value=Math.max(0,Math.min(screen.value.pages-1,screen.value.page+delta));lastPage=Date.now();}
watch(()=>props.cards.map(c=>c.id).join('|'),()=>{page.value=Math.min(page.value,screen.value.pages-1);});
watch(()=>props.cards.filter(c=>overviewPriority(c,props.fresh(c))>0).map(c=>c.id).join('|'),(value,old)=>{
 const previous=new Set((old||'').split('|'));if(value.split('|').some(id=>id&&!previous.has(id))){page.value=0;lastPage=Date.now();}
});
watch(autoplay,()=>lastPage=Date.now());
onMounted(()=>{
 window.addEventListener('resize',measure);
 if(typeof ResizeObserver!=='undefined'){observer=new ResizeObserver(measure);if(board.value)observer.observe(board.value);}
 timer=window.setInterval(()=>{
  now.value=Date.now();
  if(!autoplay.value||document.hidden||hovered.value||board.value?.contains(document.activeElement)||screen.value.pages<2){lastPage=now.value;return;}
  if(now.value-lastPage>=20000){page.value=(screen.value.page+1)%screen.value.pages;lastPage=now.value;}
 },1000);void nextTick(measure);
});
onUpdated(measure);
onUnmounted(()=>{window.removeEventListener('resize',measure);observer?.disconnect();cancelAnimationFrame(frame);clearInterval(timer);});
</script>
<template><section ref="board" class="tv-board" aria-label="Экран мониторинга" @mouseenter="hovered=true" @mouseleave="hovered=false">
 <div class="tv-columns" aria-hidden="true"><span>Машина</span><span>CPU</span><span>RAM</span><span>DISK</span><span>Ping</span><span>Выбранные сервисы</span></div>
 <article v-for="c in screen.rows" :key="c.id" class="tv-machine" :class="{'measurements-stale':!fresh(c),'has-problem':overviewPriority(c,fresh(c))>0}">
  <div class="tv-identity"><router-link :to="`/machines/${encodeURIComponent(c.id)}`" :title="c.name"><strong>{{c.name}}</strong></router-link><small>{{c.os}} / {{c.arch}} · <router-link :to="`/machines/${encodeURIComponent(c.id)}/console`" :aria-label="`Консоль ${c.name}`">Консоль</router-link></small><span class="badge"><span class="dot" :class="fresh(c)?c.state:'stale'"/>{{stateLabel(!fresh(c)&&c.state==='ok'?'stale':c.state)}}</span><small v-if="c.maintenance_active">Обслуживание</small></div>
  <div class="tv-metric" :class="{err:fresh(c)&&c.breaches?.some((b:any)=>b.metric==='cpu')}"><span class="sr">CPU </span><strong>{{number(c.cpu,'%',0)}}</strong></div>
  <div class="tv-metric" :class="{err:fresh(c)&&c.breaches?.some((b:any)=>b.metric==='ram')}"><span class="sr">RAM </span><strong>{{c.ram_total>0?number(c.ram_used/c.ram_total*100,'%',0):'Нет данных'}}</strong><small>{{bytes(c.ram_used)}} / {{bytes(c.ram_total)}}</small></div>
  <div class="tv-metric" :class="{err:fresh(c)&&c.breaches?.some((b:any)=>b.metric==='disk')}"><span class="sr">DISK </span><strong>{{number(worstDisk(c.disks)?.used_percent,'%',0)}}</strong><small :title="worstDisk(c.disks)?.mount">{{worstDisk(c.disks)?.mount||'Нет данных'}}</small></div>
  <div class="tv-metric"><span class="sr">Ping </span><strong>{{number(c.ping?.mean_ms,' мс',0)}}</strong><small>Потери {{number(c.ping?.loss_percent,'%',0)}}</small><small v-if="pingDetail(c.ping)" :title="pingDetail(c.ping)">{{pingDetail(c.ping)}}</small></div>
  <div class="tv-services"><ServiceList v-if="c.services?.length" :services="c.services" compact columns/>
   <router-link v-else :to="`/machines/${encodeURIComponent(c.id)}#services`">Выбрать сервисы</router-link>
   <small v-if="c.unselected_service_problems" class="err">Проблем вне списка: {{c.unselected_service_problems}}</small>
  </div>
 </article>
 <footer class="tv-pager"><button type="button" :disabled="screen.page===0" @click="turn(-1)">← Назад</button><span aria-live="polite">{{screen.page+1}} / {{screen.pages}} · {{cards.length}} машин</span><button type="button" :disabled="screen.page===screen.pages-1" @click="turn(1)">Далее →</button><label><input v-model="autoplay" type="checkbox"/> Листать каждые 20 с</label></footer>
</section></template>
