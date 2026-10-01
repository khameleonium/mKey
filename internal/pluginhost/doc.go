// Package pluginhost — модуль mKey «plugins» (ADR-0029, docs/plugins.md, FR-PLG-1…6): хост
// плагинов.
//
// Модуль находит плагины в ~/.local/share/mkey/plugins/<id> и /usr/share/mkey/plugins/<id>
// (manifest.go: plugin.yaml), запускает включённые (список modules.plugins.active в
// config.yaml) и регистрирует их виды в обычных точках расширения — action, condition, trigger,
// project_template. Для движка и конструктора плагинный вид ничем не отличается от встроенного.
//
// Виды плагинов:
//   - process (process.go, proxy.go) — отдельная программа на любом языке, JSON-RPC 2.0 через
//     stdin/stdout (internal/lib/jsonrpc). Надзор перезапускает упавший процесс (1, 2, 4… с),
//     после трёх падений за минуту плагин отключается с уведомлением; взведённые триггеры
//     взводятся заново. Запросы плагина к mKey (mkey.send, mkey.vars.*, mkey.notify) проверяются
//     по разрешениям манифеста;
//   - lua (kinds.go) — main.lua выполняется модулем lua (contracts.LuaPluginLoader) без доступа
//     к файлам и программам;
//   - data (kinds.go) — шаблоны проектов templates/*.mkey.yaml.
//
// Сервис contracts.Plugins (list, включение, установка из папки или .zip — install.go, удаление,
// журнал) используют API (/api/v1/plugins…), команда mkey plugin и раздел «Плагины» окна.
// При смене набора видов публикуется contracts.TopicExtensionsChanged: движок перепроверяет
// проекты, окно — палитру.
//
// Расширение: новый метод протокола — в proxy.go (обе стороны) и docs/plugins.md; новый вид
// плагина — константа Kind* в manifest.go и запуск в launch.
package pluginhost
