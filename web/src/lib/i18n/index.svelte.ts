// Реактивные переводы для компонентов Svelte: текущий язык хранится в $state,
// поэтому смена языка сразу перерисовывает все тексты, полученные через t().
import en from "./en.json";
import ru from "./ru.json";
import { detectLang, translate, type Lang, type Messages } from "./translate";

/** Все встроенные переводы интерфейса. */
export const catalog: Record<Lang, Messages> = { ru, en };

/** Ключ хранения выбранного языка в localStorage. */
const KEY = "mkey.lang";

/** savedLang возвращает язык, выбранный раньше, или язык браузера. */
function savedLang(): Lang {
  try {
    const v = localStorage.getItem(KEY);
    if (v === "ru" || v === "en") return v;
  } catch {
    // Хранилище браузера недоступно — берём язык браузера.
  }
  return detectLang(navigator.languages);
}

/** Текущее состояние языка (реактивное). */
const state = $state<{ lang: Lang }>({ lang: savedLang() });

/** lang возвращает текущий язык интерфейса. */
export function lang(): Lang {
  return state.lang;
}

/** setLang переключает и запоминает язык интерфейса. */
export function setLang(next: Lang): void {
  state.lang = next;
  document.documentElement.lang = next;
  try {
    localStorage.setItem(KEY, next);
  } catch {
    // Не удалось запомнить — язык всё равно переключён до перезагрузки страницы.
  }
}

/** t переводит ключ на текущий язык с подстановкой параметров {name}. */
export function t(key: string, params?: Record<string, string | number>): string {
  return translate(catalog, state.lang, key, params);
}
