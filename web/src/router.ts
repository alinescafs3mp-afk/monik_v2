import { createRouter, createWebHistory } from "vue-router";

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: "/login", component: () => import("./pages/Login.vue"), meta: { public: true, title: "Вход" } },
    { path: "/setup", component: () => import("./pages/Setup.vue"), meta: { public: true, title: "Настройка" } },
    { path: "/", component: () => import("./pages/Overview.vue"), meta: { title: "Обзор" } },
    { path: "/machines", component: () => import("./pages/Machines.vue"), meta: { title: "Машины" } },
    { path: "/machines/:id", component: () => import("./pages/Machine.vue"), meta: { title: "Машина" } },
    { path: "/services", component: () => import("./pages/Services.vue"), meta: { title: "Сервисы" } },
    { path: "/problems", component: () => import("./pages/Problems.vue"), meta: { title: "Проблемы и история" } },
    { path: "/agents", component: () => import("./pages/Agents.vue"), meta: { title: "Агенты" } },
    { path: "/operations", component: () => import("./pages/Operations.vue"), meta: { title: "Операции" } },
    { path: "/operations/:id", component: () => import("./pages/Operation.vue"), meta: { title: "Операция" } },
    { path: "/updates", component: () => import("./pages/Updates.vue"), meta: { title: "Обновления" } },
    { path: "/settings", component: () => import("./pages/Settings.vue"), meta: { title: "Настройки" } },
    { path: "/add", component: () => import("./pages/AddMachine.vue"), meta: { title: "Добавить машину" } },
  ],
});
