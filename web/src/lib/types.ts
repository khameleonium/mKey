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
  /** Применённый профиль устройства ("builtin/…", "user/…"). */
  profile?: string;
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
