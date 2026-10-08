// Тексты монитора нажатий (FR-DEV-8): строка журнала для экрана и для файла, имя файла журнала.
import type { WatchEntry, WatchGroup } from "../../lib/types";

/** Translate — функция перевода (t из i18n): ключ и параметры → текст. */
export type Translate = (key: string, params?: Record<string, string | number>) => string;

/** describeWatch возвращает описание события: «{A} нажата», «ось LX = 120», «колёсико +1»… */
export function describeWatch(e: WatchEntry, t: Translate): string {
  switch (e.kind) {
    case "key":
      return t(e.action === "up" ? "devices.ev_up" : "devices.ev_down", { key: `{${e.name}}` });
    case "axis":
      return t("devices.ev_axis", { axis: e.name, value: e.value ?? 0 });
    case "wheel":
      return t("devices.ev_wheel", {
        value: (e.value ?? 0) > 0 ? "+" + String(e.value) : String(e.value),
      });
    case "touch":
      return t("devices.ev_touch", { axis: e.name, value: e.value ?? 0 });
    default:
      return t("devices.ev_move", { dx: e.value ?? 0, dy: e.dy ?? 0 });
  }
}

/** WATCH_GROUPS — группы событий для галочек «Показывать» в порядке показа. */
export const WATCH_GROUPS: WatchGroup[] = ["wheel", "axes", "moves", "touch"];

/** pad дополняет число нулями слева до width цифр. */
function pad(n: number, width = 2): string {
  return String(n).padStart(width, "0");
}

/** clockText возвращает местное время события с миллисекундами: «12:34:56.789». */
export function clockText(iso: string): string {
  const d = new Date(iso);
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.${pad(d.getMilliseconds(), 3)}`;
}

/**
 * logText — журнал для файла: события по порядку (старые сверху), по строке на событие:
 * «12:34:56.789  {A} нажата  — Logitech USB Keyboard  (KEY_A)».
 */
export function logText(entries: WatchEntry[], t: Translate): string {
  return entries
    .map(
      (e) =>
        `${clockText(e.time)}  ${describeWatch(e, t)}  — ${e.device_name || e.device}  (${e.kernel})`,
    )
    .join("\n")
    .concat(entries.length ? "\n" : "");
}

/** logFileName — имя файла журнала по времени сохранения: «mkey-monitor-2026-10-02_14-05-09.txt». */
export function logFileName(d: Date): string {
  const date = `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
  const time = `${pad(d.getHours())}-${pad(d.getMinutes())}-${pad(d.getSeconds())}`;
  return `mkey-monitor-${date}_${time}.txt`;
}
