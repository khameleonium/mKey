// Места на диске, где mKey хранит файлы пользователя («Где что лежит»): список приходит от демона
// (GET /places — его собирают модули) и запоминается до смены языка, чтобы подсказки в разных
// разделах не запрашивали его каждый раз.
import { api } from "./api";
import { lang } from "./i18n/index.svelte";
import type { PlaceInfo } from "./types";

/** Загруженный список и язык, на котором он получен (реактивно). */
const state = $state<{ list: PlaceInfo[] | null; lang: string }>({ list: null, lang: "" });

/** pending — идущая загрузка (одна на все компоненты). */
let pending: Promise<void> | null = null;

/** loadPlaces загружает список, если его ещё нет на текущем языке; ошибки молча пропускаются
 * (подсказки о папках необязательны — без демона их просто не видно). */
export function loadPlaces(): Promise<void> {
  const l = lang();
  if (state.list && state.lang === l) return Promise.resolve();
  pending ??= api
    .places()
    .then((r) => {
      state.list = r.places;
      state.lang = l;
    })
    .catch(() => undefined)
    .finally(() => {
      pending = null;
    });
  return pending;
}

/** places возвращает загруженный список (пустой, пока не загружен). */
export function places(): PlaceInfo[] {
  return state.list ?? [];
}

/** place возвращает место по ID (undefined — нет: модуль отключён или список не загружен). */
export function place(id: string): PlaceInfo | undefined {
  return state.list?.find((p) => p.ids.includes(id));
}

/** copyPath копирует путь в буфер обмена; false — браузер не дал доступа к буферу. */
export async function copyPath(path: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(path);
    return true;
  } catch {
    return false;
  }
}
