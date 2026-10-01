/**
 * combo — сочетания клавиш понятными словами для подсказок: запись зажатием
 * "^{LCtrl}^{RAlt}{Space}" → «левый Ctrl + правый Alt + Пробел».
 */

/** Переводчик: ключ и параметры → текст (передаётся снаружи, чтобы функцию можно было проверять тестами). */
export type Translate = (key: string, args?: Record<string, string | number>) => string;

/** sideRe — левый/правый модификатор: LCtrl, RAlt, LShift, RMeta… */
const sideRe = /^([LR])(Ctrl|Alt|Shift|Meta|Win|Super)$/i;

/** keyText называет одну клавишу: стороны модификаторов и пробел — словами, остальное — как есть. */
function keyText(name: string, t: Translate): string {
  const side = sideRe.exec(name);
  if (side?.[1] && side[2]) {
    return t(side[1].toUpperCase() === "L" ? "key.left" : "key.right", { key: side[2] });
  }
  if (name.toLowerCase() === "space") return t("key.space");
  return name;
}

/** comboText переводит сочетание в слова; пусто или не сочетание — возвращает как есть. */
export function comboText(combo: string, t: Translate): string {
  const names = [...combo.matchAll(/\{([^}]+)\}/g)].map((m) => m[1] ?? "");
  if (names.length === 0) return combo;
  return names.map((n) => keyText(n, t)).join(" + ");
}
