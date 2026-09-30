// Работа с JSON Schema параметров блоков: значения по умолчанию, выбор варианта oneOf,
// список полей формы. Чистые функции без Svelte — их проверяют тесты schema.test.ts.
import type { Schema } from "./types";

/** isObject сообщает, что значение — обычный объект (не массив и не null). */
export function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

/** schemaType возвращает основной тип схемы: "object", "string", "integer"… или "" (любой). */
export function schemaType(s: Schema): string {
  // Явный тип; для списка типов берём первый.
  if (Array.isArray(s.type)) return s.type[0] ?? "";
  if (s.type) return s.type;

  // Тип выводится из подсказок: список значений, свойства, вложенные блоки.
  if (s.enum) return "enum";
  if (s.properties) return "object";
  if (s["x-widget"] === "actions" || s["x-widget"] === "conditions") return "array";
  return "";
}

/** defaultValue возвращает начальное значение нового блока или поля по схеме. */
export function defaultValue(s: Schema | undefined): unknown {
  if (!s) return undefined;

  // Явное значение по умолчанию; для вариантов — значение первого варианта.
  if (s.default !== undefined) return structuredClone(s.default);
  if (s.oneOf?.[0]) return defaultValue(s.oneOf[0]);

  switch (schemaType(s)) {
    case "enum":
      return s.enum?.[0];
    case "object": {
      // Объект: обязательные поля и поля со значением по умолчанию.
      const out: Record<string, unknown> = {};
      for (const [name, p] of Object.entries(s.properties ?? {})) {
        if (s.required?.includes(name) || p.default !== undefined) {
          out[name] = defaultValue(p);
        }
      }
      return out;
    }
    case "array":
      return [];
    case "string":
      return "";
    case "integer":
    case "number":
      return s.minimum ?? 0;
    case "boolean":
      return false;
  }
  return "";
}

/** matches сообщает, подходит ли значение под вариант схемы (по типу и обязательным полям). */
function matches(s: Schema, v: unknown): boolean {
  switch (schemaType(s)) {
    case "enum":
      return (s.enum ?? []).includes(v);
    case "string":
      return typeof v === "string";
    case "integer":
      return Number.isInteger(v);
    case "number":
      return typeof v === "number";
    case "boolean":
      return typeof v === "boolean";
    case "object":
      return isObject(v) && (s.required ?? []).every((k) => k in v);
    case "array":
      return Array.isArray(v);
  }
  return true;
}

/** variantIndex возвращает номер варианта oneOf, под который подходит значение (0, если ни под какой). */
export function variantIndex(s: Schema, v: unknown): number {
  const i = (s.oneOf ?? []).findIndex((variant) => matches(variant, v));
  return i < 0 ? 0 : i;
}

/**
 * switchVariant возвращает значение для варианта index: значение по умолчанию этого варианта,
 * в которое перенесены совпадающие по имени поля старого значения (например, вложенные блоки «Делать»).
 */
export function switchVariant(s: Schema, v: unknown, index: number): unknown {
  const variant = s.oneOf?.[index];
  const next = defaultValue(variant);
  if (isObject(next) && isObject(v)) {
    // Переносим только поля, которые есть в новом варианте и подходят ему по типу.
    for (const [name, p] of Object.entries(variant?.properties ?? {})) {
      if (name in v && matches(p, v[name])) {
        next[name] = v[name];
      }
    }
  }
  return next;
}

/** Field — поле формы объекта. */
export interface Field {
  /** name — имя свойства; title — подпись (или имя, если подписи нет). */
  name: string;
  title: string;
  schema: Schema;
  /** required — поле обязательно; advanced — показывать под «Подробнее». */
  required: boolean;
  advanced: boolean;
}

/** fields возвращает поля формы объекта в порядке схемы. */
export function fields(s: Schema): Field[] {
  return Object.entries(s.properties ?? {}).map(([name, p]) => ({
    name,
    title: p.title ?? name,
    schema: p,
    required: s.required?.includes(name) ?? false,
    advanced: p["x-advanced"] === true,
  }));
}

/** enumLabel возвращает подпись значения списка (или само значение). */
export function enumLabel(s: Schema, v: unknown): string {
  const i = (s.enum ?? []).indexOf(v);
  return (i >= 0 ? s["x-enum-labels"]?.[i] : undefined) ?? String(v ?? "");
}
