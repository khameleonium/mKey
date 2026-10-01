/**
 * gamepad — мастер «второй геймпад» (FR-VD-4): раскладка по умолчанию и составление файла
 * проекта с виртуальным геймпадом и привязками (раздел bindings, docs/projects.md).
 */

/** StickDir — направление стика: ось и положение при нажатии. */
export interface StickDir {
  axis: string;
  value: number;
}

/** Row — строка мастера: кнопка геймпада или направление стика и клавиша, которая её нажимает. */
export interface Row {
  /** id — уникальный ключ строки ("South", "LX-"). */
  id: string;
  /** control — кнопка геймпада ("South") или направление стика (stick). */
  control: string;
  stick?: StickDir;
  /** key — клавиша-источник ("Space"; пусто — не назначена). */
  key: string;
}

/** Options — выбор человека в мастере. */
export interface Options {
  /** name — название проекта; pad — имя геймпада в макросах; template — шаблон (xbox360). */
  name: string;
  pad: string;
  template: string;
  /** device — авто-ID или имя устройства-источника ("" — любая клавиатура). */
  device: string;
  rows: Row[];
  /** mouseRight — правый стик — мышью (строки правого стика не нужны). */
  mouseRight: boolean;
  /** hide — прятать клавиши от других программ; rampMS — плавный наклон стика (0 — сразу). */
  hide: boolean;
  rampMS: number;
}

/** STICKS — направления стиков: левый — WASD, правый — стрелки. */
const STICKS: { id: string; axis: string; value: number; key: string }[] = [
  { id: "LY-", axis: "LY", value: -1, key: "W" },
  { id: "LY+", axis: "LY", value: 1, key: "S" },
  { id: "LX-", axis: "LX", value: -1, key: "A" },
  { id: "LX+", axis: "LX", value: 1, key: "D" },
  { id: "RY-", axis: "RY", value: -1, key: "Up" },
  { id: "RY+", axis: "RY", value: 1, key: "Down" },
  { id: "RX-", axis: "RX", value: -1, key: "Left" },
  { id: "RX+", axis: "RX", value: 1, key: "Right" },
];

/** PRESET — клавиши кнопок по умолчанию (кнопки без клавиши остаются неназначенными). */
const PRESET: Record<string, string> = {
  South: "Space",
  East: "C",
  West: "R",
  North: "F",
  LB: "Q",
  RB: "E",
  LT: "Z",
  RT: "X",
  Start: "Enter",
  Select: "Tab",
  LS: "LShift",
  RS: "V",
  DPadUp: "1",
  DPadDown: "2",
  DPadLeft: "3",
  DPadRight: "4",
};

/**
 * defaultRows возвращает строки по умолчанию для шаблона: направления стиков (если у шаблона есть
 * такие оси), затем все его кнопки.
 */
export function defaultRows(buttons: string[], axes: string[]): Row[] {
  const rows: Row[] = STICKS.filter((s) => axes.includes(s.axis)).map((s) => ({
    id: s.id,
    control: s.axis,
    stick: { axis: s.axis, value: s.value },
    key: s.key,
  }));
  for (const b of buttons) rows.push({ id: b, control: b, key: PRESET[b] ?? "" });
  return rows;
}

/** isRightStick сообщает, что строка — направление правого стика. */
export function isRightStick(r: Row): boolean {
  return r.stick?.axis === "RX" || r.stick?.axis === "RY";
}

/** q заключает строку в двойные кавычки YAML (JSON-строка — правильная строка YAML). */
function q(s: string): string {
  return JSON.stringify(s);
}

/** source — клавиша-источник в записи привязки: "{W}" или "{Клава.W}" для конкретного устройства. */
function source(device: string, key: string): string {
  return device ? `{${device}.${key}}` : `{${key}}`;
}

/** buildProject составляет файл проекта (YAML с пояснениями) по выбору в мастере. */
export function buildProject(o: Options, comments: { header: string; mouse: string }): string {
  const lines = [
    `# ${comments.header}`,
    "version: 1",
    `name: ${q(o.name)}`,
    "events: []",
    "virtual_devices:",
    `  - name: ${o.pad}`,
    `    template: ${o.template}`,
    "bindings:",
  ];
  const hide = o.hide ? ", hide: true" : "";

  // Кнопки и направления стиков с назначенными клавишами.
  for (const r of o.rows) {
    if (!r.key || (o.mouseRight && isRightStick(r))) continue;
    const from = q(source(o.device, r.key));
    if (r.stick) {
      const ramp = o.rampMS > 0 ? `, ramp_ms: ${o.rampMS}` : "";
      lines.push(
        `  - { from: ${from}, to: ${q(`{${o.pad}.${r.stick.axis}}`)}, value: ${r.stick.value}${ramp}${hide} }`,
      );
    } else {
      lines.push(`  - { from: ${from}, to: ${q(`{${o.pad}.${r.control}}`)}${hide} }`);
    }
  }

  // Правый стик мышью.
  if (o.mouseRight) {
    lines.push(`  # ${comments.mouse}`);
    lines.push(`  - { from: "{MouseX}", to: ${q(`{${o.pad}.RX}`)}${hide} }`);
    lines.push(`  - { from: "{MouseY}", to: ${q(`{${o.pad}.RY}`)}${hide} }`);
  }
  return lines.join("\n") + "\n";
}
