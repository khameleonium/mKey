// Модель конструктора: проект в виде дерева блоков с постоянными идентификаторами (uid),
// удобная для перетаскивания и форм, и перевод обратно в проект для API.
//
// В проекте (API и YAML) действия записаны по-разному: верхний уровень события — {type, value},
// вложенные (в «Повторять», «Если») — {вид: значение}; условия — {type, params} и {type, ...параметры}.
// Где в значении блока лежат вложенные списки, говорит схема вида из реестра (x-widget
// "actions" / "conditions"), поэтому перевод не знает конкретных видов блоков.
// Чистые функции без Svelte — их проверяют тесты blocks.test.ts.
import { isObject, variantIndex } from "./schema";
import type { Action, Condition, Project, ProjectEvent, Registry, Schema, Trigger } from "./types";

/** ActionBlock — блок действия: вид и значение (вложенные списки уже переведены в блоки). */
export interface ActionBlock {
  uid: string;
  type: string;
  value: unknown;
}

/** CondBlock — блок условия: вид и параметры (вложенные условия уже переведены в блоки). */
export interface CondBlock {
  uid: string;
  type: string;
  params: Record<string, unknown>;
}

/** EditorEvent — событие в конструкторе: триггеры всегда списком, условия и действия — блоками. */
export interface EditorEvent {
  uid: string;
  /** Остальные поля события (id, name, enabled, policy…) — как в проекте. */
  data: Omit<ProjectEvent, "trigger" | "triggers" | "conditions" | "actions">;
  triggers: Trigger[];
  conditions: CondBlock[];
  actions: ActionBlock[];
}

/** EditorProject — проект в конструкторе. */
export interface EditorProject {
  /** Поля проекта без событий — как в проекте. */
  data: Omit<Project, "events">;
  events: EditorEvent[];
}

/** Счётчик для уникальных uid блоков в пределах страницы. */
let nextUID = 0;

/** uid возвращает новый уникальный идентификатор блока. */
export function uid(): string {
  nextUID += 1;
  return "b" + String(nextUID);
}

/** schemaOf возвращает схему параметров вида из реестра точки point ("action", "condition"). */
export function schemaOf(reg: Registry, point: string, type: string): Schema | undefined {
  return reg[point]?.find((e) => e.id === type)?.params_schema;
}

/** Mapper — что делать со вложенными списками при обходе значения по схеме. */
interface Mapper {
  actions: (list: unknown[]) => unknown[];
  conditions: (list: unknown[]) => unknown[];
}

/** walk обходит значение по схеме и заменяет вложенные списки действий и условий через mapper. */
function walk(s: Schema | undefined, v: unknown, m: Mapper): unknown {
  if (!s) return v;

  // Варианты: обходим по тому, под который подходит значение.
  if (s.oneOf) {
    return walk(s.oneOf[variantIndex(s, v)], v, m);
  }

  // Вложенные списки блоков.
  if (s["x-widget"] === "actions" && Array.isArray(v)) return m.actions(v);
  if (s["x-widget"] === "conditions" && Array.isArray(v)) return m.conditions(v);

  // Объект: обходим свойства, описанные в схеме.
  if (s.properties && isObject(v)) {
    const out: Record<string, unknown> = { ...v };
    for (const [name, p] of Object.entries(s.properties)) {
      if (name in v) out[name] = walk(p, v[name], m);
    }
    return out;
  }
  return v;
}

/** actionsIn переводит список действий проекта в блоки. nested — вложенная форма {вид: значение}. */
export function actionsIn(reg: Registry, list: unknown[], nested: boolean): ActionBlock[] {
  return list.map((raw) => {
    // Вид и значение из одной из двух форм записи.
    let type = "";
    let value: unknown;
    if (nested && isObject(raw)) {
      type = Object.keys(raw)[0] ?? "";
      value = raw[type];
    } else if (isObject(raw)) {
      type = String(raw.type ?? "");
      value = raw.value;
    }
    return { uid: uid(), type, value: walk(schemaOf(reg, "action", type), value, inMapper(reg)) };
  });
}

/** actionsOut переводит блоки действий обратно в список проекта. */
export function actionsOut(reg: Registry, blocks: ActionBlock[], nested: boolean): unknown[] {
  return blocks.map((b) => {
    const value = walk(schemaOf(reg, "action", b.type), b.value, outMapper(reg));
    if (nested) return { [b.type]: value ?? null };
    return value === undefined ? { type: b.type } : { type: b.type, value };
  });
}

/** condsIn переводит список условий проекта в блоки. nested — форма {type, ...параметры}. */
export function condsIn(reg: Registry, list: unknown[], nested: boolean): CondBlock[] {
  return list.map((raw) => {
    const r = isObject(raw) ? raw : {};
    const type = String(r.type ?? "");
    let params: Record<string, unknown>;
    if (nested) {
      const { type: _type, ...rest } = r;
      void _type;
      params = rest;
    } else {
      params = isObject(r.params) ? r.params : {};
    }
    const walked = walk(schemaOf(reg, "condition", type), params, inMapper(reg));
    return { uid: uid(), type, params: isObject(walked) ? walked : {} };
  });
}

/** condsOut переводит блоки условий обратно в список проекта. */
export function condsOut(reg: Registry, blocks: CondBlock[], nested: boolean): unknown[] {
  return blocks.map((b) => {
    const params = walk(schemaOf(reg, "condition", b.type), b.params, outMapper(reg));
    const p = isObject(params) ? params : {};
    if (nested) return { type: b.type, ...p };
    return Object.keys(p).length ? { type: b.type, params: p } : { type: b.type };
  });
}

/** inMapper переводит вложенные списки проекта в блоки. */
function inMapper(reg: Registry): Mapper {
  return {
    actions: (list) => actionsIn(reg, list, true),
    conditions: (list) => condsIn(reg, list, true),
  };
}

/** outMapper переводит вложенные блоки обратно в списки проекта. */
function outMapper(reg: Registry): Mapper {
  return {
    actions: (list) => actionsOut(reg, list as ActionBlock[], true),
    conditions: (list) => condsOut(reg, list as CondBlock[], true),
  };
}

/** projectIn переводит проект из API в модель конструктора. */
export function projectIn(reg: Registry, p: Project): EditorProject {
  const { events, ...data } = p;
  return {
    data,
    events: (events ?? []).map((e) => {
      const { trigger, triggers, conditions, actions, ...rest } = e;
      return {
        uid: uid(),
        data: rest,
        triggers: [...(trigger ? [trigger] : []), ...(triggers ?? [])],
        conditions: condsIn(reg, conditions ?? [], false),
        actions: actionsIn(reg, actions ?? [], false),
      };
    }),
  };
}

/** projectOut переводит модель конструктора в проект для API. */
export function projectOut(reg: Registry, ep: EditorProject): Project {
  return {
    ...ep.data,
    events: ep.events.map((e) => {
      const out: ProjectEvent = {
        ...e.data,
        actions: actionsOut(reg, e.actions, false) as Action[],
      };
      // Один триггер записывается полем trigger, несколько — списком triggers.
      if (e.triggers.length === 1) out.trigger = e.triggers[0];
      else if (e.triggers.length > 1) out.triggers = e.triggers;
      const conds = condsOut(reg, e.conditions, false) as Condition[];
      if (conds.length) out.conditions = conds;
      return out;
    }),
  };
}

/** newEventID возвращает свободный ID события вида "event1", "event2"… */
export function newEventID(events: EditorEvent[]): string {
  const used = new Set(events.map((e) => e.data.id));
  let n = events.length + 1;
  while (used.has("event" + String(n))) n += 1;
  return "event" + String(n);
}

/** cloneBlock копирует блок действия с новыми uid у него и всех вложенных блоков. */
export function cloneBlock<T extends { uid: string }>(b: T): T {
  return renew(structuredClone(b)) as T;
}

/** renew заменяет uid у всех объектов с полем uid внутри значения. */
function renew(v: unknown): unknown {
  if (Array.isArray(v)) {
    v.forEach(renew);
  } else if (isObject(v)) {
    if (typeof v.uid === "string") v.uid = uid();
    Object.values(v).forEach(renew);
  }
  return v;
}
