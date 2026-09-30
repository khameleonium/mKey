// Всплывающие сообщения внизу окна: «Сохранено», ошибки и т.п.

/** Toast — одно сообщение. */
export interface Toast {
  id: number;
  text: string;
  kind: "ok" | "error" | "info";
}

/** Текущие сообщения (реактивные). */
export const toasts = $state<Toast[]>([]);

/** Счётчик идентификаторов сообщений. */
let next = 0;

/** toast показывает сообщение на несколько секунд (ошибки — дольше). */
export function toast(text: string, kind: Toast["kind"] = "ok"): void {
  next += 1;
  const id = next;
  toasts.push({ id, text, kind });
  setTimeout(
    () => {
      const i = toasts.findIndex((t) => t.id === id);
      if (i >= 0) toasts.splice(i, 1);
    },
    kind === "error" ? 8000 : 3000,
  );
}

/** errorText возвращает понятный текст ошибки. */
export function errorText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
