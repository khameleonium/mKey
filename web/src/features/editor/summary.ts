// Краткое описание параметров блока одной строкой — для строки триггера в листе событий:
// «{F8}, нажатие включает, повторное — выключает».
import { enumLabel, fields, isObject, variantIndex } from "../../lib/schema";
import type { Schema } from "../../lib/types";

/** summarize возвращает значения параметров через запятую в порядке полей схемы. */
export function summarize(s: Schema | undefined, v: unknown, ms: string): string {
  if (!s) return "";

  // Варианты — по подходящему варианту.
  if (s.oneOf) return summarize(s.oneOf[variantIndex(s, v)], v, ms);

  // Скаляр: подпись значения списка, миллисекунды или само значение.
  if (!isObject(v)) {
    if (v === undefined || v === null || v === "") return "";
    if (s.enum) return enumLabel(s, v);
    return s["x-widget"] === "ms" ? `${String(v)} ${ms}` : String(v);
  }

  // Объект: непустые поля; флажки — подписью поля, если включены.
  const parts: string[] = [];
  for (const f of fields(s)) {
    const x = v[f.name];
    if (x === true) parts.push(f.title.toLowerCase());
    else if (x !== false && !Array.isArray(x)) {
      const part = summarize(f.schema, x, ms);
      if (part) parts.push(part);
    }
  }
  return parts.join(", ");
}
