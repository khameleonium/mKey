/**
 * presets — готовые раскладки виртуальных устройств (FR-VD-4, FR-VD-8, ADR-0041): какой клавишей,
 * мышью или стиком настоящего геймпада управляется каждая кнопка и ось виртуального устройства,
 * и составление по ним раздела bindings проекта (docs/projects.md, «Привязки»).
 */
import type { Binding, VirtualTemplateInfo } from "../../lib/types";

/** Scheme — чем управлять устройством: клавиатурой, клавиатурой и мышью, настоящим геймпадом
 *  или ничем (раскладку человек настроит сам). */
export type Scheme = "keyboard" | "keyboard_mouse" | "gamepad" | "none";

/** Opts — настройки привязки, кроме from/to (docs/projects.md, «Привязки осей»). */
export type Opts = Omit<Binding, "from" | "to">;

/** Row — строка раскладки: что нажимает (кнопка или ось устройства to; у «кнопка → ось» — value
 *  в opts) и чем (from — клавиша "W", ось "LX" или мышь "MouseX"; "" — не назначено). analog —
 *  источник ось или мышь (его не ловят кнопкой «Нажмите клавишу…»). */
export interface Row {
  id: string;
  to: string;
  from: string;
  analog?: boolean;
  opts?: Opts;
}

/** KINDS — виды устройств по порядку показа в «Новом устройстве» и их значки. */
export const KINDS: { id: string; icon: string }[] = [
  { id: "xbox360", icon: "🎮" },
  { id: "ds4", icon: "🎮" },
  { id: "wheel", icon: "🏎" },
  { id: "flightstick", icon: "🕹" },
  { id: "joystick", icon: "🕹" },
  { id: "keyboard", icon: "⌨" },
  { id: "mouse", icon: "🖱" },
  { id: "touchscreen", icon: "👆" },
];

/** SCHEMES — чем можно управлять устройством каждого вида (первое — по умолчанию). */
export const SCHEMES: Record<string, Scheme[]> = {
  xbox360: ["keyboard", "keyboard_mouse"],
  ds4: ["keyboard", "keyboard_mouse"],
  wheel: ["keyboard_mouse", "keyboard", "gamepad"],
  flightstick: ["keyboard_mouse", "gamepad"],
};

/** schemesFor возвращает способы управления устройством вида kind. */
export function schemesFor(kind: string): Scheme[] {
  return SCHEMES[kind] ?? ["none"];
}

/** BASE_NAMES — имя устройства в макросах по умолчанию ({pad2.South}, {wheel.Gas}). */
const BASE_NAMES: Record<string, string> = {
  xbox360: "pad2",
  ds4: "pad2",
  wheel: "wheel",
  flightstick: "stick",
  joystick: "joy",
  keyboard: "kbd2",
  mouse: "mouse2",
  touchscreen: "touch",
};

/** freeName — имя устройства вида kind, которого ещё нет среди taken (без учёта регистра):
 *  "pad2", затем "pad2_2", "pad2_3"… */
export function freeName(kind: string, taken: string[]): string {
  const base = BASE_NAMES[kind] ?? "dev";
  const used = new Set(taken.map((n) => n.toLowerCase()));
  if (!used.has(base)) return base;
  for (let i = 2; ; i++) {
    if (!used.has(`${base}_${i}`)) return `${base}_${i}`;
  }
}

/** NAME_RE — имя устройства: буква, затем буквы, цифры и «_» (как имена в макросах). */
export const NAME_RE = /^\p{L}[\p{L}\p{N}_]*$/u;

/** key — строка «клавиша → кнопка устройства». */
function key(to: string, from: string, opts?: Opts): Row {
  return { id: `${to}<${from}`, to, from, ...(opts ? { opts } : {}) };
}

/** dir — строка «клавиша → ось в положение value» (стик в сторону, педаль, РУД). */
function dir(id: string, to: string, from: string, opts: Opts): Row {
  return { id, to, from, opts };
}

/** analog — строка «ось или мышь → ось» (руль мышью, стик → стик, курок → педаль). */
function analog(to: string, from: string, opts?: Opts): Row {
  return { id: `${to}<${from}`, to, from, analog: true, ...(opts ? { opts } : {}) };
}

/** GAMEPAD_KEYS — клавиши кнопок геймпада по умолчанию (как в прежнем мастере «второй геймпад»). */
const GAMEPAD_KEYS: Record<string, string> = {
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

/** gamepadRows — геймпад клавиатурой: левый стик — WASD, правый — стрелки или мышь, кнопки —
 *  GAMEPAD_KEYS; ramp — плавный наклон стиков клавишами (мс, 0 — сразу). */
function gamepadRows(info: VirtualTemplateInfo, mouse: boolean, ramp: number): Row[] {
  const r = ramp > 0 ? { ramp_ms: ramp } : {};
  const rows: Row[] = [
    dir("LY-", "LY", "W", { value: -1, ...r }),
    dir("LY+", "LY", "S", { value: 1, ...r }),
    dir("LX-", "LX", "A", { value: -1, ...r }),
    dir("LX+", "LX", "D", { value: 1, ...r }),
  ];
  if (mouse) {
    rows.push(analog("RX", "MouseX"), analog("RY", "MouseY"));
  } else {
    rows.push(
      dir("RY-", "RY", "Up", { value: -1, ...r }),
      dir("RY+", "RY", "Down", { value: 1, ...r }),
      dir("RX-", "RX", "Left", { value: -1, ...r }),
      dir("RX+", "RX", "Right", { value: 1, ...r }),
    );
  }
  for (const b of info.buttons) rows.push(key(b, GAMEPAD_KEYS[b] ?? ""));
  return rows;
}

/** DPAD — крестовина: у руля и лётного джойстика с геймпада — та же крестовина. */
const DPAD = ["DPadUp", "DPadDown", "DPadLeft", "DPadRight"];

/** wheelRows — раскладки руля: мышь крутит руль (держит угол), клавиши — плавно до упора,
 *  геймпад — стик с кривой, курки — педали. */
function wheelRows(scheme: Scheme): Row[] {
  // Кнопки руля, общие для клавиатуры: лепестки, меню, крестовина — стрелками.
  const keys = [
    key("ShiftUp", "E"),
    key("ShiftDown", "Q"),
    key("Cross", "Enter"),
    key("Circle", "Backspace"),
    key("Square", "R"),
    key("Triangle", "F"),
    key("Options", "Tab"),
    key("DPadUp", "Up"),
    key("DPadDown", "Down"),
    key("DPadLeft", "Left"),
    key("DPadRight", "Right"),
  ];
  const pedals = [
    dir("Gas+", "Gas", "W", { value: 1, ramp_ms: 250 }),
    dir("Brake+", "Brake", "S", { value: 1, ramp_ms: 150 }),
    dir("Clutch+", "Clutch", "LShift", { value: 1, ramp_ms: 100 }),
  ];
  switch (scheme) {
    case "keyboard_mouse":
      return [analog("Wheel", "MouseX", { steer: true }), ...pedals, ...keys];
    case "keyboard":
      return [
        dir("Wheel-", "Wheel", "A", { value: -1, ramp_ms: 350 }),
        dir("Wheel+", "Wheel", "D", { value: 1, ramp_ms: 350 }),
        ...pedals,
        ...keys,
      ];
    default:
      return [
        analog("Wheel", "LX", { curve: 1.5, deadzone: 0.05 }),
        analog("Gas", "RT"),
        analog("Brake", "LT"),
        key("ShiftUp", "RB"),
        key("ShiftDown", "LB"),
        key("Cross", "South"),
        key("Circle", "East"),
        key("Square", "West"),
        key("Triangle", "North"),
        key("Options", "Start"),
        key("Share", "Select"),
        ...DPAD.map((d) => key(d, d)),
      ];
  }
}

/** flightRows — раскладки лётного джойстика: мышь — ручка (сама к центру), W/S — РУД «как
 *  рычаг», A/D — поворот ручки; геймпад — стики, курок — РУД. */
function flightRows(scheme: Scheme): Row[] {
  if (scheme === "gamepad") {
    return [
      analog("StickX", "LX", { deadzone: 0.05 }),
      analog("StickY", "LY", { deadzone: 0.05 }),
      analog("Twist", "RX", { deadzone: 0.1 }),
      analog("Throttle", "RT"),
      key("Trigger", "South"),
      key("Button2", "East"),
      key("Button3", "West"),
      key("Button4", "North"),
      key("Button5", "LB"),
      key("Button6", "RB"),
      key("Button7", "Select"),
      key("Button8", "Start"),
      ...DPAD.map((d) => key(d, d)),
    ];
  }
  const rows: Row[] = [
    analog("StickX", "MouseX", { steer: true, recenter_ms: 800 }),
    analog("StickY", "MouseY", { steer: true, recenter_ms: 800 }),
    dir("Twist-", "Twist", "A", { value: -1, ramp_ms: 150 }),
    dir("Twist+", "Twist", "D", { value: 1, ramp_ms: 150 }),
    dir("Throttle+", "Throttle", "W", { value: 1, ramp_ms: 2000, latch: true }),
    dir("Throttle-", "Throttle", "S", { value: 0, ramp_ms: 2000, latch: true }),
    dir("Throttle0", "Throttle", "X", { value: 0, latch: true }),
    key("Trigger", "Mouse0"),
    key("Button2", "Mouse1"),
    key("Button3", "Space"),
  ];
  for (let i = 4; i <= 12; i++) rows.push(key(`Button${i}`, String(i - 3)));
  rows.push(
    key("DPadUp", "Up"),
    key("DPadDown", "Down"),
    key("DPadLeft", "Left"),
    key("DPadRight", "Right"),
  );
  return rows;
}

/** presetRows возвращает раскладку по умолчанию для вида kind и способа управления scheme;
 *  ramp — плавный наклон стиков геймпада клавишами (мс). Остальные виды — без раскладки. */
export function presetRows(
  kind: string,
  scheme: Scheme,
  info: VirtualTemplateInfo | undefined,
  ramp = 0,
): Row[] {
  switch (kind) {
    case "xbox360":
    case "ds4":
      return info ? gamepadRows(info, scheme === "keyboard_mouse", ramp) : [];
    case "wheel":
      return wheelRows(scheme);
    case "flightstick":
      return flightRows(scheme);
  }
  return [];
}

/** hidesByDefault — прятать ли источник от других программ по умолчанию: клавиатурный геймпад и
 *  настоящий геймпад — да (игра не должна видеть оба), мышь — нет (указатель замер бы). */
export function hidesByDefault(kind: string, scheme: Scheme): boolean {
  if (scheme === "gamepad") return true;
  return (kind === "xbox360" || kind === "ds4") && scheme === "keyboard";
}

/** source — источник привязки: "{W}" или "{Геймпад.South}" у конкретного устройства. */
export function source(device: string, from: string): string {
  return device ? `{${device}.${from}}` : `{${from}}`;
}

/** rowBindings составляет привязки по строкам: name — имя устройства в макросах, device —
 *  устройство-источник ("" — любое), hide — прятать клавиши (мышь не прячется никогда: указатель
 *  нужен человеку). Строки без источника пропускаются. */
export function rowBindings(rows: Row[], name: string, device: string, hide: boolean): Binding[] {
  const out: Binding[] = [];
  for (const r of rows) {
    if (!r.from) continue;
    const mouse = r.from.startsWith("Mouse") && r.analog;
    const b: Binding = { from: source(mouse ? "" : device, r.from), to: `{${name}.${r.to}}` };
    Object.assign(b, r.opts ?? {});
    if (hide && !mouse) b.hide = true;
    out.push(b);
  }
  return out;
}

/** BuildOptions — что нужно для файла проекта нового устройства. */
export interface BuildOptions {
  /** project — название проекта; name — имя устройства; kind — его вид (шаблон). */
  project: string;
  name: string;
  kind: string;
  /** device — устройство-источник ("" — любое); hide — прятать клавиши; rows — раскладка. */
  device: string;
  hide: boolean;
  rows: Row[];
}

/** buildProject составляет файл выключенного проекта с устройством и раскладкой (YAML; записи
 *  привязок — в виде JSON, это правильный YAML). header — пояснение в начале файла. */
export function buildProject(o: BuildOptions, header: string): string {
  const lines = [
    ...header.split("\n").map((l) => `# ${l}`),
    "version: 1",
    `name: ${JSON.stringify(o.project)}`,
    "enabled: false",
    "events: []",
    "virtual_devices:",
    `  - name: ${o.name}`,
    `    template: ${o.kind}`,
  ];
  const bindings = rowBindings(o.rows, o.name, o.device, o.hide);
  if (bindings.length) {
    lines.push("bindings:");
    for (const b of bindings) lines.push(`  - ${JSON.stringify(b)}`);
  }
  return lines.join("\n") + "\n";
}

/** targets сообщает, что привязка b управляет устройством name ({name.…}, без учёта регистра). */
export function targets(b: Binding, name: string): boolean {
  return b.to.trim().toLowerCase().startsWith(`{${name.toLowerCase()}.`);
}

/** rowsFromBindings превращает привязки устройства name обратно в строки (для правки раскладки
 *  в редакторе): источник без устройства и фигурных скобок, настройки — как есть. */
export function rowsFromBindings(bindings: Binding[], name: string): Row[] {
  return bindings
    .filter((b) => targets(b, name))
    .map((b, i) => {
      const to = b.to.trim().slice(name.length + 2, -1);
      const raw = b.from.trim().replace(/^\{|\}$/g, "");
      const from = raw.includes(".") ? raw.slice(raw.indexOf(".") + 1) : raw;
      const opts: Opts = { ...b };
      delete (opts as Partial<Binding>).from;
      delete (opts as Partial<Binding>).to;
      delete opts.hide;
      const analogSrc =
        /^(Mouse[XY]|MouseH?Wheel|[LR][XYT]|DPad[XY])$/.test(from) && !("value" in opts);
      return {
        id: `${i}:${to}<${from}`,
        to,
        from,
        ...(analogSrc ? { analog: true } : {}),
        ...(Object.keys(opts).length ? { opts } : {}),
      };
    });
}

/** sourceDevice — устройство-источник привязок устройства name ("" — любое или разные). */
export function sourceDevice(bindings: Binding[], name: string): string {
  const devices = new Set(
    bindings
      .filter((b) => targets(b, name) && !b.from.includes("Mouse"))
      .map((b) => {
        const raw = b.from.trim().replace(/^\{|\}$/g, "");
        return raw.includes(".") ? raw.slice(0, raw.indexOf(".")) : "";
      }),
  );
  return devices.size === 1 ? ([...devices][0] ?? "") : "";
}
