// Типы ответов HTTP API демона mKey (docs/openapi.yaml).
// Написаны вручную по openapi.yaml (ADR-0022): генератор типов — лишняя зависимость
// для небольшого API; при изменении API правьте оба файла вместе.

/** Место ошибки в проекте (details ошибки api.project_invalid): событие, часть и номер с 0 (-1 — часть целиком). */
export interface ProblemPlace {
  event: string;
  part?: "trigger" | "condition" | "action";
  index: number;
}

/** Ошибка API: код (i18n-ключ), сообщение на языке клиента и подробности. */
export interface ApiErrorBody {
  code: string;
  message: string;
  details?: { pos?: { line: number; col: number } } & Record<string, unknown>;
}

/** Состояние модуля программы. */
export interface ModuleStatus {
  id: string;
  core: boolean;
  state: string;
  error?: string;
}

/** Ответ GET /status. */
export interface Status {
  version: string;
  commit: string;
  pid: number;
  started_at?: string;
  port: number;
  running: number;
  modules?: ModuleStatus[];
  input?: { open: number; denied?: string[] };
  output?: { available: boolean; error?: string };
  grab_suspended: boolean;
}

/** Строка списка проектов (GET /projects). */
export interface ProjectInfo {
  id: string;
  name: string;
  enabled: boolean;
  events: number;
  remaps: number;
  path: string;
  error?: string;
}

/** Триггер события: вид и параметры. */
export interface Trigger {
  type: string;
  params?: Record<string, unknown>;
}

/** Условие события: вид и параметры. */
export interface Condition {
  type: string;
  params?: Record<string, unknown>;
}

/** Действие верхнего уровня: вид и значение (вложенные действия — в форме {вид: значение}). */
export interface Action {
  type: string;
  value?: unknown;
}

/** Событие проекта. */
export interface ProjectEvent {
  id: string;
  name?: string;
  enabled?: boolean;
  trigger?: Trigger;
  triggers?: Trigger[];
  conditions?: Condition[];
  actions: Action[];
  policy?: string;
  max_parallel?: number;
  release_modifiers?: string;
}

/** Переменная проекта. */
export interface Variable {
  type: string;
  value: unknown;
  persist?: boolean;
}

/** Проект целиком. */
export interface Project {
  id: string;
  version: number;
  name: string;
  enabled?: boolean;
  active_when?: Condition[];
  variables?: Record<string, Variable>;
  events: ProjectEvent[];
  remaps?: { from: string; to: string; device?: string }[];
  virtual_devices?: VirtualDeviceSpec[];
  bindings?: Binding[];
}

/** Виртуальное устройство в проекте (раздел virtual_devices, ADR-0028). */
export interface VirtualDeviceSpec {
  name: string;
  template: string;
  buttons?: string[];
  axes?: Record<string, { min: number; max: number }>;
}

/** Привязка «физический ввод → виртуальный выход» (раздел bindings, docs/projects.md). */
export interface Binding {
  from: string;
  to: string;
  value?: number;
  hide?: boolean;
  invert?: boolean;
  deadzone?: number;
  sensitivity?: number;
  threshold?: number;
  ramp_ms?: number;
  latch?: boolean;
  steer?: boolean;
  recenter_ms?: number;
  curve?: number;
}

/** Ответ GET /projects/{id}. */
export interface ProjectFile {
  project: Project;
  raw: string;
  path: string;
  error?: string;
}

/** Шаблон проекта. */
export interface Template {
  id: string;
  name: string;
  description: string;
  content: string;
}

/** Состояние события (GET /events). */
export interface EventStatus {
  project: string;
  event: string;
  name?: string;
  enabled: boolean;
  running: number;
  toggled: boolean;
  triggers: string[];
}

/** Узел JSON Schema параметров вида (с подсказками для GUI x-*). */
export interface Schema {
  type?: string | string[];
  enum?: unknown[];
  default?: unknown;
  minimum?: number;
  title?: string;
  description?: string;
  required?: string[];
  properties?: Record<string, Schema>;
  oneOf?: Schema[];
  items?: Schema;
  "x-widget"?: string;
  "x-lang"?: string;
  "x-advanced"?: boolean;
  "x-enum-labels"?: string[];
  /** x-confirm — значения списка, выбор которых нужно подтвердить: значение → i18n-ключ вопроса. */
  "x-confirm"?: Record<string, string>;
}

/** Вид триггера, условия или действия из реестра (GET /registry). */
export interface RegistryEntry {
  id: string;
  name: string;
  name_key: string;
  description?: string;
  category?: string;
  category_name?: string;
  icon?: string;
  params_schema?: Schema;
  provider: string;
}

/** Ответ GET /registry: точки расширения → виды. */
export type Registry = Record<string, RegistryEntry[] | undefined>;

/** Проверка диагностики. */
export interface DoctorCheck {
  id: string;
  status: "ok" | "info" | "warn" | "fail";
  message: string;
  fix?: string;
}

/** Устройство ввода. */
export interface InputDevice {
  info: {
    path: string;
    name: string;
    phys?: string;
    id: { bustype: number; vendor: number; product: number; version: number };
  };
  kinds: string[];
}

/** Виртуальное устройство проекта (GET /devices/virtual, FR-VD-1). */
export interface VirtualDeviceInfo {
  name: string;
  template: string;
  project: string;
  system_name: string;
  node?: string;
  error?: string;
}

/** Виртуальное устройство любого проекта с состоянием (GET /devices/virtual → all, ADR-0041). */
export interface VirtualCard {
  name: string;
  template: string;
  system_name: string;
  project: string;
  project_name: string;
  /** on — подключено (игры видят), off — проект выключен, error — не удалось создать. */
  state: "on" | "off" | "error";
  error?: string;
  node?: string;
  bindings: number;
  hides: boolean;
  others: number;
}

/** Нажатое и положения осей подключённого виртуального устройства (GET /devices/virtual/{name}/state). */
export interface VirtualState {
  buttons: string[];
  axes: Record<string, number>;
}

/** Состав шаблона виртуального устройства: кнопки и оси именами для макросов (GET /devices/virtual). */
export interface VirtualTemplateInfo {
  id: string;
  buttons: string[];
  axes: string[];
}

/** Запись монитора нажатий (GET /api/v1/input/watch, поток «input»). */
/** Группы событий монитора, которые можно скрыть: колёсико, оси геймпадов, движения мыши, касания. */
export type WatchGroup = "wheel" | "axes" | "moves" | "touch";

export interface WatchEntry {
  time: string;
  device_name: string;
  device: string;
  kind: "key" | "axis" | "wheel" | "move" | "touch";
  /** Группа для галочек «Показывать»: клавиши и кнопки (keys) видны всегда. */
  group: WatchGroup | "keys";
  /** Имя для макросов: "A", "UnKey001", "Sega.Start"; "#код" — имени нет. */
  name: string;
  kernel: string;
  code: number;
  /** Имя — авто-ID или имя, данное человеком (кнопку можно переименовать). */
  labeled?: boolean;
  action?: "down" | "up";
  value?: number;
  dy?: number;
}

/** Кнопка, ось или индикатор устройства: код, имя ядра, имя для макросов (если есть). */
export interface DeviceControl {
  code: number;
  kernel: string;
  name?: string;
  /** Имя кнопки или оси без стандартного имени для макросов ("UnKey001", "UnKey2.001", "Sega.Start"). */
  label?: string;
  /** Номер в devices.yaml ("001", "Axis01") — для переименования; имя, данное человеком. */
  number?: string;
  custom_name?: string;
}

/** Абсолютная ось с диапазоном. */
export interface DeviceAxis extends DeviceControl {
  min: number;
  max: number;
  fuzz: number;
  flat: number;
  resolution: number;
}

/** Подробности устройства (GET /devices/inspect, FR-DEV-1). */
export interface DeviceDetails extends InputDevice {
  info: InputDevice["info"] & { uniq?: string };
  by_id?: string;
  by_path?: string;
  bus: string;
  /** Авто-ID устройства (UnKey, UnKey2…); нет — устройство его не получило. */
  auto_id?: string;
  /** Имя устройства, данное человеком (FR-DEV-3). */
  device_name?: string;
  keys?: DeviceControl[];
  rel?: DeviceControl[];
  axes?: DeviceAxis[];
  switches?: DeviceControl[];
  leds?: DeviceControl[];
  ff?: DeviceControl[];
  props?: string[];
}

/** Место на диске, где mKey хранит файлы пользователя (GET /places, «Где что лежит»). */
export interface PlaceInfo {
  /** Идентификатор: config, projects, recordings, lua_scripts, shell_scripts, log…;
   * ids — все места с этим путём (скрипты Lua и bash в одной папке — одна строка). */
  id: string;
  ids: string[];
  /** Название и описание на языке интерфейса. */
  name: string;
  description?: string;
  /** Полный путь и тот же путь с «~» вместо домашней папки. */
  path: string;
  display: string;
  /** Папка (иначе файл); есть ли она уже на диске. */
  is_dir: boolean;
  exists: boolean;
}

/** Запись ввода (GET /recordings). */
export interface RecordingInfo {
  name: string;
  path: string;
  created: string;
  duration_ms: number;
  events: number;
  devices?: string[];
  stop_hotkey?: string;
  /** Файл не читается (ошибка после ручной правки, запись первой версии); нет — всё в порядке. */
  problem?: RecordingProblem;
}

/** Ошибка в файле записи: строка, вид, понятное сообщение на языке интерфейса. */
export interface RecordingProblem {
  line?: number;
  text?: string;
  code: string;
  arg?: string;
  message?: string;
}

/** Результат «Нажмите клавишу…». */
export interface CapturedKey {
  name: string;
  code: number;
  kernel: string;
  device: string;
  device_name: string;
}

/** Timing — интервалы нажатий по умолчанию (GET/PUT /settings/timing, modules.engine в config.yaml). */
export interface Timing {
  /** key_hold_ms — сколько держать клавишу при обычном нажатии, мс (0–1000). */
  key_hold_ms: number;
  /** key_delay_ms — пауза после каждого нажатия и символа текста, мс (0–1000). */
  key_delay_ms: number;
  /** layout_switch_ms — пауза после переключения раскладки, мс (0–5000). */
  layout_switch_ms: number;
}

/** RecordSettings — настройки записи по умолчанию (GET/PUT /settings/recording, config.yaml). */
export interface RecordSettings {
  /** kinds — классы, которые записываются по умолчанию (для устройств без своей галочки). */
  kinds: string[];
  /** devices — свои галочки устройств: «VID:PID имя» → записывать ли (важнее kinds). */
  devices?: Record<string, boolean>;
  /** moves — записывать движения мыши. */
  moves: boolean;
  /** merge_moves_ms — склейка движений мыши при записи, мс (0 — каждое движение). */
  merge_moves_ms: number;
  /** center_pointer — курсор в центр экрана перед записью. */
  center_pointer: boolean;
  /** coalesce_ms — склейка движений мыши при воспроизведении, мс (0 — нет). */
  coalesce_ms: number;
}

/** InterfaceSettings — язык и тема окна (GET/PUT /settings/interface, config.yaml). */
export interface InterfaceSettings {
  /** language — "ru", "en" или "" (как в системе). */
  language: string;
  /** theme — "system", "light", "dark" или "" (как в системе). */
  theme: string;
}

/** PluginInfo — плагин (GET /plugins, ADR-0029). */
export interface PluginInfo {
  id: string;
  version?: string;
  /** kind — process, lua или data. */
  kind: string;
  /** name и description — на языках ("ru" → текст). */
  name?: Record<string, string>;
  description?: Record<string, string>;
  dir: string;
  system?: boolean;
  permissions?: string[];
  active: boolean;
  /** state — off, starting, running, restarting, failed, broken. */
  state: string;
  error?: string;
  actions?: string[];
  conditions?: string[];
  triggers?: string[];
  templates?: string[];
}

/** UpdateInfo — версии mKey (GET /update, ADR-0030). */
export interface UpdateInfo {
  current: string;
  latest?: string;
  available: boolean;
  url?: string;
  /** can_apply — mKey может обновиться сам; иначе reason — "package" или "dev". */
  can_apply: boolean;
  reason?: string;
  /** check — проверка раз в сутки включена. */
  check: boolean;
}

/** ProjectScript — скрипт в проекте (lua, shell): показывается до включения чужого проекта (SEC-7). */
export interface ProjectScript {
  event: string;
  type: string;
  code?: string;
  file?: string;
}

/**
 * Строка таймлайна сухого прогона (contracts.DryStep): шаг макроса (press, release, release_all,
 * tap, wait, text, move, wheel, axis, touch, swipe), заголовок группы (group), заметка (note)
 * или действие, которое выполнится только при настоящем запуске (action).
 */
export interface DryStep {
  /** at_ms — когда начнётся шаг, мс от начала события. */
  at_ms: number;
  /** depth — вложенность (0 — верхний уровень). */
  depth: number;
  kind: string;
  keys?: string[];
  count?: number;
  /** ms — удержание (tap, touch), пауза (wait; max_ms — верхняя граница), время свайпа. */
  ms?: number;
  max_ms?: number;
  text?: string;
  dx?: number;
  dy?: number;
  value?: number;
  /** points — точки касания: "50%, 80%". */
  points?: string[];
  device?: string;
  /** key и args — текст группы или заметки (i18n-ключ окна и параметры). */
  key?: string;
  args?: Record<string, string>;
  /** action — вид действия (action); conditions — условия группы или события (вид и параметры). */
  action?: string;
  conditions?: Condition[];
}

/** Результат сухого прогона события (POST /projects/{id}/events/{event}/dry_run). */
export interface DryRun {
  steps: DryStep[];
  /** total_ms — примерная длительность; open — заранее неизвестна (повтор «пока…»). */
  total_ms: number;
  open?: boolean;
  /** truncated — таймлайн слишком длинный и обрезан. */
  truncated?: boolean;
}

/** Язык файлов-скриптов (GET /scripts): lua или shell. */
export interface ScriptLang {
  id: string;
  /** name — название на языке окна; highlight — подсветка редактора. */
  name: string;
  highlight: "lua" | "shell";
  /** dir — папка файлов; ext — их расширение (".lua"). */
  dir: string;
  ext: string;
}

/** Файл скрипта в папке языка. */
export interface ScriptFile {
  lang: string;
  name: string;
  size: number;
  modified: string;
}

/** Ошибка синтаксиса скрипта: строка с 1 (нет — неизвестна) и текст интерпретатора. */
export interface ScriptProblem {
  line?: number;
  message: string;
}

/** Устройство в выборе «что записывать» (GET /recordings/devices). */
export interface RecordDevice {
  /** key — «2dc8:310a 8BitDo Ultimate 2C Wireless Controller»; id — VID:PID; name — имя в системе. */
  key: string;
  id: string;
  name: string;
  path: string;
  /** category — keyboards, gamepads, touch, other, virtual. */
  category: string;
  kinds: string[];
  /** virtual — создано программой; own — самим mKey (не записывается). */
  virtual?: boolean;
  own?: boolean;
  /** selected — будет ли записываться; explicit — у устройства своя галочка. */
  selected: boolean;
  explicit?: boolean;
}
