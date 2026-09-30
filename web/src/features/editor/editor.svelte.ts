// Состояние редактора проекта (конструктора): проект в виде блоков, признак несохранённых
// изменений, перетаскивание блоков, сохранение и проверка через API.
// Все изменения проекта идут через методы Editor, а не прямым присваиванием в компонентах:
// так изменения видны в одном месте и помечают проект как изменённый.
import { getContext, setContext } from "svelte";
import { api, ApiError } from "../../lib/api";
import {
  actionsIn,
  actionsOut,
  cloneBlock,
  newEventID,
  projectIn,
  projectOut,
  uid,
  type ActionBlock,
  type CondBlock,
  type EditorEvent,
  type EditorProject,
} from "../../lib/blocks";
import { defaultValue, isObject } from "../../lib/schema";
import type {
  Action,
  ProblemPlace,
  Project,
  ProjectInfo,
  Registry,
  RegistryEntry,
  Trigger,
} from "../../lib/types";

/** Drag — что сейчас перетаскивается: существующий блок (uid) или новый блок из палитры (type). */
export interface Drag {
  uid?: string;
  type?: string;
}

/** Editor — редактор одного проекта. */
export class Editor {
  /** id — ID проекта; reg — реестр видов блоков. */
  readonly id: string;
  readonly reg: Registry;
  /** project — проект в виде блоков. */
  project = $state<EditorProject>({ data: { id: "", version: 1, name: "" }, events: [] });
  /** dirty — есть несохранённые изменения; saving — идёт сохранение. */
  dirty = $state(false);
  saving = $state(false);
  /** error — ошибка проверки или сохранения (текст сервера). */
  error = $state("");
  /** problem — место последней ошибки проверки (подсвечивается в листе событий). */
  problem = $state<ProblemPlace | null>(null);
  /** drag — перетаскиваемый блок. */
  drag = $state<Drag | null>(null);
  /** target — список, в который добавляет палитра по щелчку (последний выбранный). */
  target = $state<ActionBlock[] | null>(null);
  /** projects и devices — списки для выбора в полях «Проект» и «Устройство». */
  projects = $state<ProjectInfo[]>([]);
  devices = $state<string[]>([]);
  recordings = $state<string[]>([]);
  /** savedAt — время последнего своего сохранения (чтобы не считать его чужой правкой файла). */
  savedAt = 0;

  constructor(id: string, reg: Registry) {
    this.id = id;
    this.reg = reg;
  }

  /** load заменяет проект пришедшим с сервера и сбрасывает признак изменений. */
  load(p: Project): void {
    this.project = projectIn(this.reg, p);
    this.dirty = false;
    this.error = "";
    this.target = this.project.events[0]?.actions ?? null;
  }

  /** out возвращает проект для API. */
  out(): Project {
    return projectOut(this.reg, $state.snapshot(this.project) as EditorProject);
  }

  /** entry возвращает вид блока из реестра. */
  entry(point: string, type: string): RegistryEntry | undefined {
    return this.reg[point]?.find((e) => e.id === type);
  }

  /** set меняет поле объекта проекта и отмечает изменение. */
  set<T extends object, K extends keyof T>(obj: T, key: K, value: T[K]): void {
    obj[key] = value;
    this.dirty = true;
  }

  /** unset удаляет необязательное поле объекта. */
  unset(obj: Record<string, unknown>, key: string): void {
    Reflect.deleteProperty(obj, key);
    this.dirty = true;
  }

  // ---- События ----

  /** addEvent добавляет пустое событие «Когда я нажимаю клавиши…» в конец проекта. */
  addEvent(): EditorEvent {
    const ev: EditorEvent = {
      uid: uid(),
      data: { id: newEventID(this.project.events), name: "" },
      triggers: [],
      conditions: [],
      actions: [],
    };
    this.project.events.push(ev);
    this.target = this.project.events.at(-1)?.actions ?? null;
    this.dirty = true;
    return ev;
  }

  /** removeEvent удаляет событие. */
  removeEvent(ev: EditorEvent): void {
    const i = this.project.events.indexOf(ev);
    if (i >= 0) this.project.events.splice(i, 1);
    this.dirty = true;
  }

  /** duplicateEvent вставляет копию события после него (с новым ID). */
  duplicateEvent(ev: EditorEvent): void {
    const i = this.project.events.indexOf(ev);
    const copy = cloneBlock($state.snapshot(ev) as EditorEvent);
    copy.data.id = newEventID(this.project.events);
    this.project.events.splice(i + 1, 0, copy);
    this.dirty = true;
  }

  /** moveEvent сдвигает событие вверх (-1) или вниз (+1). */
  moveEvent(ev: EditorEvent, delta: number): void {
    const list = this.project.events;
    const i = list.indexOf(ev);
    const j = i + delta;
    if (i < 0 || j < 0 || j >= list.length) return;
    list.splice(i, 1);
    list.splice(j, 0, ev);
    this.dirty = true;
  }

  /** setTrigger заменяет триггер номер index (index = длине списка — добавляет). */
  setTrigger(ev: EditorEvent, index: number, t: Trigger): void {
    ev.triggers[index] = t;
    this.dirty = true;
  }

  /** removeTrigger удаляет триггер. */
  removeTrigger(ev: EditorEvent, index: number): void {
    ev.triggers.splice(index, 1);
    this.dirty = true;
  }

  // ---- Блоки действий и условий ----

  /** newAction создаёт блок действия вида type со значением по умолчанию. */
  newAction(type: string): ActionBlock {
    return { uid: uid(), type, value: defaultValue(this.entry("action", type)?.params_schema) };
  }

  /** newCondition создаёт блок условия вида type с параметрами по умолчанию. */
  newCondition(type: string): CondBlock {
    const p = defaultValue(this.entry("condition", type)?.params_schema);
    return { uid: uid(), type, params: isObject(p) ? p : {} };
  }

  /** insert вставляет блок в список на место index (в конец, если index не задан). */
  insert<T>(list: T[], block: T, index = list.length): void {
    list.splice(index, 0, block);
    this.dirty = true;
  }

  /** remove убирает блок из списка. */
  remove<T>(list: T[], block: T): void {
    const i = list.indexOf(block);
    if (i >= 0) list.splice(i, 1);
    this.dirty = true;
  }

  /** duplicate вставляет копию блока сразу после него. */
  duplicate<T extends { uid: string }>(list: T[], block: T): void {
    const i = list.indexOf(block);
    list.splice(i + 1, 0, cloneBlock($state.snapshot(block) as T));
    this.dirty = true;
  }

  /** replaceAll заменяет содержимое списка (например, после перевода из текста макроса). */
  replaceAll<T>(list: T[], items: T[]): void {
    list.splice(0, list.length, ...items);
    this.dirty = true;
  }

  /** addToTarget добавляет новый блок из палитры в выбранный список. */
  addToTarget(type: string): void {
    const list = this.target ?? this.project.events[0]?.actions ?? this.addEvent().actions;
    this.insert(list, this.newAction(type));
  }

  /**
   * drop вставляет перетаскиваемый блок в список list на место index: новый — из палитры,
   * существующий — переносится из своего места. Блок нельзя бросить внутрь него самого.
   */
  drop(list: ActionBlock[], index: number): void {
    const d = this.drag;
    this.drag = null;
    if (!d) return;

    // Новый блок из палитры.
    if (d.type) {
      this.insert(list, this.newAction(d.type), index);
      this.target = list;
      return;
    }

    // Перенос существующего блока: находим его список, проверяем, что цель не внутри него.
    const found = this.findBlock(d.uid ?? "");
    if (!found || contains(found.block, list)) return;
    const from = found.list.indexOf(found.block);
    found.list.splice(from, 1);
    const at = found.list === list && from < index ? index - 1 : index;
    list.splice(at, 0, found.block);
    this.dirty = true;
  }

  /** findBlock ищет блок действия по uid во всём проекте; возвращает блок и его список. */
  findBlock(id: string): { block: ActionBlock; list: ActionBlock[] } | undefined {
    for (const ev of this.project.events) {
      const r = findIn(ev.actions, id);
      if (r) return r;
    }
    return undefined;
  }

  // ---- Текст макроса (режим DSL, FR-UI-4) ----

  /** toDSL переводит блоки списка в текст макроса (ошибка — среди блоков есть не-макросные). */
  async toDSL(list: ActionBlock[]): Promise<string> {
    const actions = actionsOut(this.reg, $state.snapshot(list) as ActionBlock[], false) as Action[];
    return (await api.actionsToDsl(actions)).text;
  }

  /** fromDSL заменяет блоки списка блоками из текста макроса. */
  async fromDSL(list: ActionBlock[], text: string): Promise<void> {
    const { actions } = await api.dslToActions(text);
    this.replaceAll(list, actionsIn(this.reg, actions, false));
  }

  // ---- Сохранение ----

  /** save проверяет и сохраняет проект; true — сохранено. */
  async save(): Promise<boolean> {
    this.saving = true;
    this.error = "";
    this.problem = null;
    try {
      this.savedAt = Date.now();
      await api.saveProject(this.id, this.out());
      this.dirty = false;
      return true;
    } catch (e) {
      this.fail(e);
      return false;
    } finally {
      this.saving = false;
    }
  }

  /** check проверяет проект, не сохраняя; true — ошибок нет. */
  async check(): Promise<boolean> {
    this.error = "";
    this.problem = null;
    try {
      await api.validateProject(this.out());
      return true;
    } catch (e) {
      this.fail(e);
      return false;
    }
  }

  /** fail запоминает ошибку проверки и её место (для подсветки) и прокручивает к нему. */
  private fail(e: unknown): void {
    this.error = e instanceof Error ? e.message : String(e);
    const d = e instanceof ApiError ? e.details : undefined;
    this.problem = d && typeof d.event === "string" ? (d as unknown as ProblemPlace) : null;
    // После перерисовки показываем подсвеченное место.
    setTimeout(() => {
      document.querySelector(".bad")?.scrollIntoView({ block: "center", behavior: "smooth" });
    }, 50);
  }
}

/** findIn ищет блок по uid в списке и во вложенных списках его блоков. */
function findIn(
  list: ActionBlock[],
  id: string,
): { block: ActionBlock; list: ActionBlock[] } | undefined {
  for (const b of list) {
    if (b.uid === id) return { block: b, list };
    for (const inner of nestedLists(b.value)) {
      const r = findIn(inner, id);
      if (r) return r;
    }
  }
  return undefined;
}

/** nestedLists возвращает вложенные списки блоков действий в значении блока. */
function nestedLists(v: unknown): ActionBlock[][] {
  if (!isObject(v)) return [];
  return Object.values(v).filter(
    (x): x is ActionBlock[] =>
      Array.isArray(x) && x.every((b) => isObject(b) && typeof b.uid === "string" && "value" in b),
  );
}

/** contains сообщает, лежит ли список list внутри блока b (на любой глубине). */
function contains(b: ActionBlock, list: ActionBlock[]): boolean {
  return nestedLists(b.value).some(
    (inner) => inner === list || inner.some((x) => contains(x, list)),
  );
}

/** Ключ контекста редактора. */
const KEY = Symbol("editor");

/** provideEditor делает редактор доступным вложенным компонентам. */
export function provideEditor(ed: Editor): void {
  setContext(KEY, ed);
}

/** useEditor возвращает редактор страницы. */
export function useEditor(): Editor {
  return getContext<Editor>(KEY);
}
