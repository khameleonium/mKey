// Package pluginsdk — набор для авторов плагинов mKey на Go (ADR-0029, docs/plugins.md).
//
// Плагин-процесс общается с mKey по JSON-RPC 2.0 через stdin/stdout. Пакет берёт протокол на
// себя: автор описывает виды (действия, условия, триггеры) и функции, которые их выполняют.
//
//	func main() {
//		p := pluginsdk.New()
//		p.Action(pluginsdk.Type{ID: "hello", Name: pluginsdk.Text{"ru": "Привет", "en": "Hello"}},
//			func(ctx context.Context, c *pluginsdk.Call) error {
//				return c.Host.Send(ctx, `{"Привет!"}`)
//			})
//		p.Run()
//	}
//
// Писать в stdout нельзя — это канал протокола; журнал плагина — stderr (log.Printf) или Host.Log.
// Проверка плагина на соответствие протоколу — пакет plugintest.
//
// Пакет — публичное обещание: ломающее изменение только с новой мажорной версией API плагинов.
package pluginsdk
