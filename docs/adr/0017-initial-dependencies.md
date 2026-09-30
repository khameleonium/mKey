# ADR-0017: Стартовый набор зависимостей (фаза 0)

- **Статус:** принято
- **Дата:** 2026-09-30
- **Связано:** SPEC.md §11; T0.1, T0.3, T0.9

## Контекст

Для каркаса нужны CLI-библиотека, инструменты фронтенда и линтеры. Все они
перечислены в SPEC §11 как планируемые; этот ADR фиксирует фактически подключённые.

## Решение

**Go (go.mod):**
- `github.com/spf13/cobra` — CLI (подтягивает `spf13/pflag`, `inconshreveable/mousetrap`).
- В `go.mod` добавлена директива `ignore ./web/node_modules` (Go ≥ 1.25): в npm-пакетах
  встречаются `.go`-файлы (например, `flatted`), которые иначе попадают в `go test ./...`.

**Инструменты разработчика (не в бинарнике):**
- `golangci-lint` v2 (линтеры: standard + revive, godot, misspell, errorlint, gocritic,
  unconvert, unparam, nolintlint, bodyclose; форматтеры gofmt, goimports).

**Фронтенд (web/package.json, только devDependencies — в бинарник попадает лишь сборка):**
- `svelte` 5, `vite`, `@sveltejs/vite-plugin-svelte` — фреймворк и сборка;
- `typescript`, `@tsconfig/svelte`, `@types/node`, `svelte-check` — типизация;
- `vitest` — тесты;
- `eslint`, `@eslint/js`, `typescript-eslint`, `eslint-plugin-svelte`, `globals`,
  `prettier`, `prettier-plugin-svelte` — линтеры и форматирование.
- i18n фронтенда — собственная лёгкая реализация (`web/src/lib/i18n`), без `svelte-i18n`.

## Последствия

- `go build ./...` не требует Node: без собранного фронтенда встраивается заглушка `web/stub`.
- Версии npm-пакетов зафиксированы в `web/package-lock.json`.

## Альтернативы

- `urfave/cli` вместо cobra — cobra распространённее и даёт автодополнение для bash/zsh/fish (FR-CLI-2).
- `svelte-i18n` — лишняя зависимость ради простой подстановки ключей.
