// Тема оформления: как в системе (по умолчанию), светлая или тёмная (FR-UI-9).
// Выбор хранится в браузере; тема применяется атрибутом data-theme у <html>.

/** Theme — выбор темы. */
export type Theme = "system" | "light" | "dark";

/** Ключ хранения выбора в localStorage. */
const KEY = "mkey.theme";

/** Текущий выбор (реактивный). */
const state = $state<{ theme: Theme }>({ theme: load() });

/** load читает сохранённый выбор (если хранилище недоступно — «как в системе»). */
function load(): Theme {
  try {
    const v = localStorage.getItem(KEY);
    if (v === "light" || v === "dark") return v;
  } catch {
    // Хранилище браузера недоступно — тема по умолчанию.
  }
  return "system";
}

/** apply применяет тему к странице. */
function apply(t: Theme): void {
  if (t === "system") delete document.documentElement.dataset.theme;
  else document.documentElement.dataset.theme = t;
}

apply(state.theme);

/** theme возвращает текущий выбор темы. */
export function theme(): Theme {
  return state.theme;
}

/** setTheme меняет и запоминает тему. */
export function setTheme(t: Theme): void {
  state.theme = t;
  apply(t);
  try {
    localStorage.setItem(KEY, t);
  } catch {
    // Не удалось запомнить — тема всё равно применена до перезагрузки страницы.
  }
}
