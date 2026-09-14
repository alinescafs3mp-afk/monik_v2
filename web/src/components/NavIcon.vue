<script setup lang="ts">
defineProps<{ name: string }>();
const paths: Record<string,string> = {
 overview:'M3 3h7v7H3z M14 3h7v7h-7z M3 14h7v7H3z M14 14h7v7h-7z',
 machine:'M3 4h18v12H3z M8 21h8 M12 16v5',
 service:'M8 4L2 12l6 8 M16 4l6 8-6 8 M14 4l-4 16',
 problem:'M12 3L2 21h20z M12 9v5 M12 17v1',
 agent:'M5 3h14v7H5z M5 14h14v7H5z M8 6h1 M8 17h1',
 operation:'M8 6h13 M8 12h13 M8 18h13 M3 6h1 M3 12h1 M3 18h1',
 update:'M12 16V3 M7 8l5-5 5 5 M4 16v5h16v-5',
 settings:'M4 6h16 M4 12h16 M4 18h16 M8 3v6 M16 9v6 M10 15v6',
 add:'M12 4v16 M4 12h16', menu:'M3 6h18 M3 12h18 M3 18h18'
};
</script>
<template><svg class="nav-icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path :d="paths[name] || paths.overview"/></svg></template>
