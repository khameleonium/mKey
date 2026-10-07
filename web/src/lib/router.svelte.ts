// Простой роутер на адресе после «#»: #/projects, #/editor/games. Хэш не уходит на сервер,
// поэтому все разделы — одна страница, встроенная в бинарник.

/** Route — текущий раздел и его параметры. */
export interface Route {
  /** name — раздел: "home", "projects", "editor", "recordings", "scripts", "devices", "plugins",
   *  "diagnostics", "settings", "setup". */
  name: string;
  /** param — параметр раздела (ID проекта для редактора). */
  param: string;
}

/** parse разбирает хэш адреса в раздел. Неизвестный или пустой адрес — главная. */
export function parse(hash: string): Route {
  const parts = hash.replace(/^#\/?/, "").split("/");
  const name = parts[0] || "home";
  return { name, param: decodeURIComponent(parts.slice(1).join("/")) };
}

/** Текущий раздел (реактивный). */
const state = $state<{ route: Route }>({ route: parse(location.hash) });

// Смена адреса (ссылки, кнопки «Назад»/«Вперёд» браузера) меняет раздел.
window.addEventListener("hashchange", () => {
  state.route = parse(location.hash);
});

/** route возвращает текущий раздел. */
export function route(): Route {
  return state.route;
}

/** href возвращает ссылку на раздел. */
export function href(name: string, param = ""): string {
  return "#/" + name + (param ? "/" + encodeURIComponent(param) : "");
}

/** navigate открывает раздел. */
export function navigate(name: string, param = ""): void {
  location.hash = href(name, param);
}
