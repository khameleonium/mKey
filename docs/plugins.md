# Плагины mKey

Плагин добавляет в mKey новые **действия**, **условия** и **триггеры** — они появляются
в конструкторе событий и в проектах наравне со встроенными. Решение и причины — ADR-0029.

## Виды плагинов

| `kind` | Что это | Для чего |
|---|---|---|
| `process` | любой исполняемый файл (Go, Python, bash, Rust…), общается с mKey по JSON-RPC 2.0 через stdin/stdout | всё: HTTP-запросы, вебхуки, MIDI, свои устройства |
| `lua` | `main.lua`, выполняется внутри mKey | простые действия и условия без отдельной программы |
| `data` | только файлы YAML | готовые шаблоны проектов |

## Где лежат и как включаются

- Папки плагинов: `~/.local/share/mkey/plugins/<id>/` (свои) и `/usr/share/mkey/plugins/<id>/`
  (из пакетов). В папке — `plugin.yaml` и файлы плагина.
- Установка: `mkey plugin install <папка>` или «Плагины» → «Установить» в окне.
- Новый плагин **выключен**: посмотрите, что он просит (`permissions`), и включите:
  `mkey plugin enable <id>` или переключатель в окне. Список включённых — в `config.yaml`:

  ```yaml
  modules:
    plugins:
      active: [io.example.http-actions]
  ```

- Журнал плагина (всё, что он пишет в stderr): `mkey plugin logs <id>`,
  файл `~/.local/state/mkey/plugins/<id>.log`.

## Манифест `plugin.yaml`

```yaml
id: io.example.http-actions        # уникальный ID: буквы, цифры, «.», «-», «_»
name: { ru: "HTTP-действия", en: "HTTP actions" }
description: { ru: "Запросы к сайтам и вебхуки", en: "Web requests and webhooks" }
version: 1.2.0
plugin_api: 1                      # мажорная версия API плагинов
kind: process                      # process | lua | data
entry: ./http-actions              # исполняемый файл (process) или main.lua (lua)
args: []                           # аргументы запуска (необязательно)
permissions: [net, vars.write]     # что плагин делает; показывается при включении
```

Разрешения:

| Разрешение | Что даёт | Проверяет mKey |
|---|---|---|
| `output.send` | нажимать клавиши и кнопки (`mkey.send`) | да |
| `vars.read`, `vars.write` | читать и менять переменные проектов | да |
| `notify` | показывать уведомления | да |
| `net` | ходить в сеть | нет — только предупреждение |
| `exec` | запускать программы | нет — только предупреждение |

**Честно:** плагин-процесс — обычная программа с вашими правами. mKey проверяет только его
запросы к mKey; запретить ему сеть или файлы mKey не может. Ставьте плагины, которым доверяете.

## Протокол (`kind: process`)

JSON-RPC 2.0, **одно сообщение на строку** (UTF-8, в конце `\n`). stdout — только протокол,
stderr — журнал. Запросы могут идти в обе стороны и перемежаться; отвечать можно в любом порядке.

### Запуск

mKey запускает `entry` из папки плагина (текущая папка — папка плагина) и отправляет:

```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{
  "plugin_api":1,"mkey_version":"1.0.0","lang":"ru","permissions":["net","vars.write"]}}
```

Плагин отвечает за 5 с, перечисляя свои виды:

```json
{"jsonrpc":"2.0","id":1,"result":{
  "actions":[{"id":"http_request","name":{"ru":"HTTP-запрос","en":"HTTP request"},
    "description":{"ru":"Отправить запрос на адрес","en":"Send a request"},
    "category":"system",
    "params_schema":{"type":"object","required":["url"],
      "properties":{"url":{"type":"string"},"method":{"enum":["GET","POST"],"default":"GET"}}}}],
  "conditions":[],
  "triggers":[{"id":"http_webhook","name":{"ru":"Вебхук","en":"Webhook"},
    "params_schema":{"type":"object","properties":{"port":{"type":"integer","default":8090}}}}]}}
```

ID видов должны быть уникальны среди всех видов mKey (встроенный или чужой вид с тем же ID —
вид не регистрируется, предупреждение в журнале). В проекте вид пишется как встроенный:
`- http_request: { url: "https://…" }`. Категории: `keyboard`, `mouse`, `logic`, `system`,
`input`, `text` (или своя — покажется как есть).

### Методы mKey → плагин

| Метод | Параметры | Ответ | Таймаут |
|---|---|---|---|
| `initialize` | см. выше | виды плагина | 5 с |
| `ping` | — | `{}` | 2 с (раз в 30 с) |
| `shutdown` | — | `{}`, затем плагин завершается | 2 с, затем — принудительно |
| `action.validate` | `{type, params}` | `{}` или ошибка с понятным текстом | 2 с |
| `action.run` | `{type, params, event:{project, event}, vars}` | `{}` или ошибка | нет; отменяется `$/cancelRequest` |
| `condition.validate` | `{type, params}` | `{}` или ошибка | 2 с |
| `condition.check` | `{type, params, event, vars}` | `{"result": true}` | 2 с |
| `trigger.validate` | `{type, params}` | `{}` или ошибка | 2 с |
| `trigger.arm` | `{type, params, handle, event}` | `{}` или ошибка | 2 с |
| `trigger.disarm` | `{handle}` | `{}` | 2 с |
| `$/cancelRequest` (уведомление) | `{id}` | — | — |

`params` — параметры вида из проекта (объект или значение); `vars` — значения от триггера
(например, набранный текст). Тексты ошибок — на языке `lang` из `initialize`: их видит человек.
`*.validate` можно не реализовывать (`-32601`) — тогда параметры проверяются только по схеме.

### Методы плагин → mKey

| Метод | Параметры | Ответ | Разрешение |
|---|---|---|---|
| `trigger.fire` (уведомление) | `{handle, vars?}` | — | — |
| `mkey.send` | `{macro}` — макрос (`"{Ctrl}{C}"`, docs/dsl.md) | `{}` | `output.send` |
| `mkey.vars.get` | `{project, name}` | `{value}` | `vars.read` |
| `mkey.vars.set` | `{project, name, value}` | `{}` | `vars.write` |
| `mkey.notify` | `{title, body}` | `{}` | `notify` |
| `mkey.log` | `{level: "info"\|"warn"\|"error", message}` | `{}` | — |

### Коды ошибок

| Код | Значение |
|---|---|
| `-32601` | метода нет |
| `-32602` | неверные параметры |
| `-32000` | ошибка плагина (текст — для человека) |
| `-32001` | нет разрешения |
| `-32002` | запрос отменён |

### Падения и зависания

Если процесс завершился, не ответил на `ping` или на `initialize`, mKey перезапускает его через
1, 2, 4… с и взводит триггеры заново. Три падения за минуту — плагин отключается до следующего
включения (приходит уведомление). Ошибка или зависание плагина никогда не останавливают mKey.

## Lua-плагины (`kind: lua`)

Работают внутри mKey, отдельная программа не нужна. `main.lua` регистрирует виды:

```lua
local count = 0          -- данные плагина живут, пока он включён

mkey.register_action{
  id = "counter_add",
  name = { ru = "Прибавить к счётчику", en = "Add to counter" },
  category = "logic",
  params = { type = "object", properties = { step = { type = "integer", default = 1 } } },
  validate = function(params)                         -- необязательно: текст — ошибка
    if params.step and params.step <= 0 then return "шаг должен быть больше нуля" end
  end,
  run = function(params, event)                       -- event.project, event.id
    count = count + (params.step or 1)
    mkey.log("counter = " .. count)
  end,
}

mkey.register_condition{
  id = "counter_reached",
  check = function(params) return count >= (params.value or 10) end,
}

mkey.register_trigger{
  id = "counter_full",
  name = { ru = "Когда счётчик дошёл до…", en = "When the counter reaches…" },
  params = { type = "object", properties = { value = { type = "integer", default = 10 } } },
  interval_ms = 500,                                  -- как часто спрашивать (по умолчанию 1000, не меньше 50)
  poll = function(params, state, event)               -- state — память этого события между вызовами
    if count >= (params.value or 10) and not state.fired then
      state.fired = true
      return { count = count }                        -- событие срабатывает; count доступен действиям
    end
  end,
}
```

**Триггер** — функция `poll`: mKey вызывает её раз в `interval_ms`, пока событие включено. Вернула
таблицу или `true` — событие срабатывает (значения таблицы получают действия, как у триггеров
плагинов-программ). Сработать «один раз, когда…» помогает `state` — своя таблица у каждого события.
В `poll` можно только смотреть (`mkey.var`, `mkey.is_down`, `mkey.log`, `mkey.event`): нажатия,
`mkey.sleep` и `mkey.run` дают ошибку — их место в действиях события. Один вызов — не дольше
секунды; ошибка опроса пишется в журнал (одна и та же — не чаще раза в минуту), опрос продолжается.

Внутри `run` и `check` доступны функции `mkey.*` из [Lua API](lua-api.md) — с проверкой
разрешений манифеста (`mkey.tap` — `output.send`, `mkey.notify` — `notify`, `mkey.var` —
`vars.read`/`vars.write`, `mkey.is_down` — `input.read`). Библиотек `os` и `io` у Lua-плагина нет.
Вызовы одного плагина идут по очереди (опросы триггеров — тоже), поэтому `poll` должна быть быстрой.

## Плагины-данные (`kind: data`)

Без кода: файлы `templates/*.mkey.yaml` в папке плагина становятся шаблонами проектов
(«Проекты» → «Создать» → шаблон). Название шаблона — поле `name` проекта.

## Плагины на Go

`pkg/pluginsdk` берёт протокол на себя:

```go
func main() {
	p := pluginsdk.New()
	p.Action(pluginsdk.Type{ID: "hello", Name: pluginsdk.Text{"ru": "Привет", "en": "Hello"}},
		func(ctx context.Context, c *pluginsdk.Call) error {
			return c.Host.Send(ctx, `{"Привет!"}`)
		})
	p.Run()
}
```

Проверка плагина на соответствие протоколу — `pkg/pluginsdk/plugintest`:

```go
func TestPlugin(t *testing.T) {
	plugintest.Conformance(t, plugintest.Start(t, "./my-plugin")) // initialize, ping, -32601, shutdown
	h := plugintest.Start(t, "./my-plugin")
	h.Initialize("ru", "output.send")
	_ = h.Action(context.Background(), "hello", map[string]any{"name": "Вера"})
	// h.Sent() — что плагин просил нажать; h.Arm(...) — срабатывания триггера
}
```

Примеры — в `examples/plugins/`: `go-hello-action` (Go, SDK; соберите `go build -o
go-hello-action .`), `python-webhook` (Python без SDK — протокол вручную), `lua-counter` (Lua).

**Пока ограничение:** модуль Go называется `mkey`, поэтому подключить `pkg/pluginsdk` из другого
репозитория командой `go get` нельзя — пишите плагин в папке `examples/plugins/` этого
репозитория (или на любом другом языке по протоколу выше). Вопрос — SPEC §14.
