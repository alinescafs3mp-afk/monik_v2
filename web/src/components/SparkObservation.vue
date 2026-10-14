<script setup lang="ts">
import {formatObservation,freshAt,qualityLabel,metricLabel,explainCode,type Metric} from '../sparks';
defineProps<{metric:Metric;now:number;online:boolean}>();
</script>
<template><div :class="{'measurements-stale':!online||!freshAt(metric.observation.observed_at,now)||metric.observation.quality==='stale'}">
 <dt>{{metricLabel(metric.metric_id)}}</dt><dd>{{formatObservation(metric.observation,now,online)}}</dd>
 <small>{{metric.observation.source}} · {{qualityLabel[metric.observation.quality]}}</small>
 <small>{{new Date(metric.observation.observed_at).toLocaleString()}}</small>
 <small v-if="metric.observation.reason" class="data-warning">{{explainCode(metric.observation.reason)}}</small>
</div></template>
