# ADR-0034: Путь Go-модуля `github.com/khameleonium/mKey`

- **Статус:** принято (2026-10-07, решение владельца)
- **Связано:** SPEC §14 (открытый вопрос 1), T9.4, ADR-0029

## Контекст

Go-модуль назывался `mkey`. Такой путь нельзя получить командой `go get`/`go install`: авторы
плагинов на Go не могли подключить `pkg/pluginsdk` из своего репозитория, а собрать mKey одной
командой из исходников было нельзя.

## Решение

Путь модуля — `github.com/khameleonium/mKey` (адрес публичного репозитория). Импорты, ldflags сборки
(`-X github.com/khameleonium/mKey/internal/lib/buildinfo.…` в Makefile и `.goreleaser.yaml`),
архитектурный тест, генератор модулей и настройки goimports переведены на него.

## Последствия

- SDK плагинов: `go get github.com/khameleonium/mKey/pkg/pluginsdk@latest`, импорт
  `github.com/khameleonium/mKey/pkg/pluginsdk`. Плагины, собранные со старым путём внутри этого
  репозитория (`examples/plugins/`), переведены; протокол плагинов (ADR-0029) не меняется —
  уже собранные плагины продолжают работать.
- Сборка из исходников: `go install github.com/khameleonium/mKey/cmd/mkey@latest` (без окна —
  веб-интерфейс встраивается только при `make build`).

## Альтернативы

- Оставить `mkey` — отклонено: SDK недоступен извне.
- Свой домен (vanity import) — лишняя инфраструктура для одного репозитория.
