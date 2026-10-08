/**
 * labels — понятные подписи строк раскладки виртуального устройства: что нажимается («газ»,
 * «левый стик вверх», «кнопка 7») и чем, если это не клавиша («мышь влево-вправо»).
 */
import type { Row } from "./presets";

/** Translate — функция перевода (t из i18n): ключ и параметры → текст. */
export type Translate = (key: string, params?: Record<string, string | number>) => string;

/** maybe возвращает перевод ключа или undefined, если перевода нет (t возвращает сам ключ). */
function maybe(
  t: Translate,
  key: string,
  params?: Record<string, string | number>,
): string | undefined {
  const s = t(key, params);
  return s === key ? undefined : s;
}

/** direction — суффикс направления строки «кнопка → ось»: "-", "+" или "0" (рычаг в ноль сразу);
 *  "" — строка не «кнопка → ось». Рычаг в ноль плавно (с ramp_ms) — «назад», то есть "-". */
export function direction(r: Row): string {
  const v = r.opts?.value;
  if (v === undefined) return r.opts?.latch ? (r.opts.ramp_ms ? "-" : "0") : "";
  if (v < 0) return "-";
  if (v > 0) return "+";
  return r.opts?.ramp_ms ? "-" : "0";
}

/** rowLabel — что нажимает строка: направление оси, ось или кнопка; неизвестное — имя как есть. */
export function rowLabel(r: Row, t: Translate): string {
  // Кнопка → ось: «левый стик вверх», «газ», «РУД вперёд (держите)».
  const d = direction(r);
  if (d) {
    return maybe(t, `vd.dir.${r.to}${d}`) ?? maybe(t, `gpw.stick.${r.to}${d}`) ?? `${r.to} ${d}`;
  }

  // Ось или кнопка: свои подписи, затем подписи кнопок геймпада, «кнопка N».
  const n = /^Button(\d+)$/.exec(r.to);
  if (n) return t("vd.ctl.ButtonN", { n: n[1] ?? "" });
  const own = maybe(t, `vd.ctl.${r.to}`);
  if (own) return own;
  const pad = maybe(t, `gpw.btn.${r.to}`);
  return pad ? `${r.to} — ${pad}` : r.to;
}

/** sourceLabel — чем управляется строка, если это не клавиша: мышь, стик или курок геймпада. */
export function sourceLabel(r: Row, t: Translate): string {
  return maybe(t, `vd.src.${r.from}`) ?? r.from;
}
