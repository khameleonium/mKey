# Проекты mKey: события, триггеры, условия, действия

Требования: SPEC §5.2, §5.4, §8. Модель: `internal/lib/project`; хранилище: модуль `store`;
выполнение: модуль `engine`; горячие клавиши: модуль `hotkeys`.

Проект — файл `~/.config/mkey/projects/<id>.mkey.yaml`. Изменения файла применяются сразу;
если в файле ошибка, продолжает работать прежняя версия, а ошибка видна в `mkey project list`
и приходит уведомлением. При первом запуске создаётся выключенный проект-пример `example`.

## Пример

```yaml
version: 1
name: "Игры"
enabled: true

variables:
  clicks: { type: int, value: 0, persist: true }   # int | float | string | bool

events:
  - id: autoclick                                   # уникален в проекте
    name: "Автокликер на F8"
    trigger: { type: hotkey, keys: "{F8}", on: toggle, consume: true }
    actions:
      - repeat:
          while: toggled
          do:
            - send: "{Mouse0}[50]"
            - set_var: { name: clicks, add: 1 }

remaps:                                             # переназначения 1:1
  - { from: "{CapsLock}", to: "{Esc}" }
```

## Поля события

| Поле | Значение |
|---|---|
| `id` | идентификатор (буквы, цифры, `_`, `-`) |
| `name` | название для людей |
| `enabled` | `false` — событие выключено |
| `trigger` / `triggers` | один триггер или список: событие срабатывает от любого |
| `conditions` | условия: все должны выполняться в момент срабатывания |
| `actions` | действия по порядку |
| `policy` | повторное срабатывание во время выполнения: `ignore` (по умолчанию), `restart`, `queue`, `parallel` |
| `max_parallel` | предел для `parallel` (по умолчанию 4) |
| `release_modifiers` | `auto` (по умолчанию), `always`, `never` — отпускать модификаторы горячей клавиши перед действиями |

Клавиши, зажатые одним действием (`key_down`, `^{…}`), остаются зажатыми для следующих
действий события и отпускаются, когда событие закончится или будет остановлено.

## Триггеры

| Вид | Параметры | Что делает |
|---|---|---|
| `hotkey` | `keys` (`"{Ctrl+Alt+H}"`), `on`: `press`\|`release`\|`hold`\|`double`\|`toggle`, `consume`, `device`, `hold_ms` (500), `double_ms` (300) | горячая клавиша; `consume: true` — нажатие не доходит до программ (включает перехват устройства) |
| `sequence` | `keys` (`"{G}{G}"`), `within_ms` (1000) | последовательность нажатий |
| `hotstring` | `text`, `replace`, `end_chars`, `case_sensitive` | набранное слово (после пробела, Enter, знака препинания); `replace` — заменить его |
| `timer` | `every_ms` (≥ 10) **или** `after_ms` | периодически или однократно после загрузки проекта |
| `startup` | — | при загрузке (и перезагрузке) проекта |
| `device` | `match` (часть имени или `vid:pid`), `on`: `connected`\|`disconnected` | подключение/отключение устройства |
| `manual` | — | только ручной запуск: `mkey run проект/событие` |

Правило сочетаний: срабатывание по нажатию последней клавиши, когда остальные зажаты, а лишних
модификаторов (Ctrl, Shift, Alt, Super) нет. Лишние обычные клавиши не мешают.
`Ctrl` без стороны — любая из двух клавиш Ctrl.

## Условия

| Вид | Параметры |
|---|---|
| `variable` | `name`, `op` (`==`, `!=`, `<`, `>`, `<=`, `>=`), `value` |
| `key_state` | `key` (`"Mouse0"`), `state`: `down` (по умолчанию) \| `up` |
| `toggle` | `event` (по умолчанию — своё), `state` (по умолчанию `true`) |
| `time` | `from`, `to` (`"08:00"`, интервал через полночь допустим) |
| `any` / `all` / `not` | `of`: список условий (хотя бы одно / все / ни одного) |

Условия окна (`window`) и пикселя (`pixel`) появятся в фазе 8.

## Действия

| Вид | Значение | Пример |
|---|---|---|
| `send` | макрос DSL (docs/dsl.md) | `send: "^{Ctrl}{C}~{Ctrl}"` |
| `tap` / `key_down` / `key_up` | имя клавиши или сочетания | `key_down: Shift` |
| `hold` | `{key, ms}` | `hold: { key: Space, ms: 500 }` |
| `pause` | миллисекунды или `{min_ms, max_ms}` | `pause: 250` |
| `type_text` | текст | `type_text: "Привет!"` |
| `mouse_move` | `{dx, dy}` (относительно) | `mouse_move: { dx: 10, dy: -5 }` |
| `mouse_click` | кнопка (`Left` по умолчанию) | `mouse_click: Right` |
| `wheel` | `{direction, count}` | `wheel: { direction: Down, count: 3 }` |
| `repeat` | ровно одно из `times`, `while` (`toggled`, `held`, `forever`), `conditions`; `do` | см. пример выше |
| `if` | `{conditions, then, else}` | |
| `set_var` | `{name, value}` или `{name, add}` | `set_var: { name: clicks, add: 1 }` |
| `run_event` | ID события или `{project, event, wait}` | `run_event: other` |
| `notify` | текст или `{title, body}` | `notify: "Готово"` |
| `enable` / `disable` | ID события или `{project, event}`; без `event` — весь проект | `disable: autoclick` |
| `stop` | `self` (по умолчанию) или `all` | `stop: all` |
| `lua` | код или `{file}` / `{code}` | docs/lua-api.md |
| `shell` | код или `{file}` / `{code}`, `timeout_ms` | `shell: "notify-send hi"` |

Файлы для `lua` и `shell` лежат в `~/.config/mkey/scripts/`.

## Переменные

- Объявляются в `variables` с типом и начальным значением; `persist: true` — сохранять между перезапусками
  (`~/.local/state/mkey/vars.json`).
- При правке файла проекта текущие значения сохраняются, если тип переменной не изменился.
- Из терминала: `mkey var list --project <id>`, `mkey var set <имя> <значение> --project <id>`.
- В bash-скриптах: `$MKEY_VAR_<ИМЯ>` (только чтение) и `mkey var set …` (проект подставляется сам).

## Безопасность

- **Экстренная остановка — `Esc + Backspace + Enter` одновременно** (настраивается в
  `modules.input.emergency_stop`): останавливает все макросы, отпускает все клавиши, снимает перехват
  клавиатуры и **приостанавливает mKey**: горячие клавиши, таймеры и другие триггеры не срабатывают
  до команды `mkey resume` (ручные `mkey send` и `mkey run` работают). `mkey panic` — остановить
  всё и отпустить клавиши без приостановки.
- Перехват клавиатуры включается, только когда на ней не зажата ни одна клавиша.
- Импортированный проект (`mkey project import`) выключен, пока вы его не проверите и не включите.
