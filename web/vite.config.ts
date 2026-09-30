// Конфигурация Vite: сборка GUI в web/dist (встраивается в бинарник через go:embed)
// и dev-сервер с проксированием API на локальный демон.
import { defineConfig } from "vitest/config";
import { svelte } from "@sveltejs/vite-plugin-svelte";

export default defineConfig({
  plugins: [svelte()],
  // Относительные пути к ресурсам: страница открывается с любого порта 127.0.0.1.
  base: "./",
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  server: {
    // Запросы к API в режиме разработки уходят на демон (адрес задаётся переменной окружения).
    proxy: {
      "/api": process.env.MKEY_DEV_API ?? "http://127.0.0.1:17420",
    },
  },
  test: {
    include: ["src/**/*.test.ts"],
  },
});
