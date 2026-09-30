// Реактивные переводы для компонентов Svelte: текущий язык хранится в $state,
// поэтому смена языка сразу перерисовывает все тексты, полученные через t().
import en from "./en.json";
import ru from "./ru.json";
import { detectLang, translate, type Lang, type Messages } from "./translate";

/** Все встроенные переводы интерфейса. */
export const catalog: Record<Lang, Messages> = { ru, en };

/** Текущее состояние языка (реактивное). */
const state = $state<{ lang: Lang }>({ lang: detectLang(navigator.languages) });

/** lang возвращает текущий язык интерфейса. */
export function lang(): Lang {
  return state.lang;
}

/** setLang переключает язык интерфейса. */
export function setLang(next: Lang): void {
  state.lang = next;
}

/** t переводит ключ на текущий язык с подстановкой параметров {name}. */
export function t(key: string, params?: Record<string, string | number>): string {
  return translate(catalog, state.lang, key, params);
}
