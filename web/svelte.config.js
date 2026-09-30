// Конфигурация компилятора Svelte: препроцессор TypeScript через Vite.
import { vitePreprocess } from "@sveltejs/vite-plugin-svelte";

export default {
  preprocess: vitePreprocess(),
};
