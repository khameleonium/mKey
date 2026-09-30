// Конфигурация ESLint: рекомендованные правила JS, TypeScript (strict) и Svelte.
import js from "@eslint/js";
import ts from "typescript-eslint";
import svelte from "eslint-plugin-svelte";
import globals from "globals";

export default ts.config(
  // Не проверяем сборку и зависимости.
  { ignores: ["dist/", "node_modules/", "stub/"] },
  js.configs.recommended,
  ...ts.configs.strict,
  ...svelte.configs.recommended,
  // Глобальные переменные браузера и Node (для конфигов).
  { languageOptions: { globals: { ...globals.browser, ...globals.node } } },
  // В .svelte и .svelte.ts TypeScript разбирается парсером typescript-eslint.
  {
    files: ["**/*.svelte", "**/*.svelte.ts"],
    languageOptions: { parserOptions: { parser: ts.parser } },
  },
);
