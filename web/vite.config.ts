import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import { resolve } from "node:path";

export default defineConfig({
  plugins: [vue()],
  base: "/",
  build: {
    outDir: resolve(__dirname, "../internal/webui/dist"),
    emptyOutDir: true,
    sourcemap: false,
    assetsInlineLimit: 4096,
  },
  server: {
    proxy: {
      "/api": { target: "https://127.0.0.1:8777", secure: false },
    },
  },
});
