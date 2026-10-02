// Клиент HTTP API демона. Все запросы идут на тот же адрес, с которого открыт интерфейс;
// вход выполнен по cookie (ссылка /?t=…). Язык ответов (тексты ошибок, названия блоков)
// задаётся заголовком Accept-Language по языку интерфейса.
import { lang } from "./i18n/index.svelte";
import type {
  Action,
  ApiErrorBody,
  CapturedKey,
  DeviceDetails,
  PlaceInfo,
  PluginInfo,
  RecordingInfo,
  RecordSettings,
  UpdateInfo,
  VirtualDeviceInfo,
  VirtualTemplateInfo,
  DoctorCheck,
  EventStatus,
  InputDevice,
  InterfaceSettings,
  Project,
  ProjectFile,
  ProjectInfo,
  ProjectScript,
  Registry,
  Status,
  Template,
} from "./types";

/** ApiError — ошибка ответа API с кодом и понятным сообщением. */
export class ApiError extends Error {
  /** HTTP-статус (0 — демон недоступен). */
  readonly status: number;
  /** Код ошибки (i18n-ключ), например "api.project_invalid". */
  readonly code: string;
  /** Подробности (например, позиция ошибки в макросе). */
  readonly details: ApiErrorBody["details"];

  constructor(status: number, body: ApiErrorBody) {
    super(body.message);
    this.status = status;
    this.code = body.code;
    this.details = body.details;
  }
}

/**
 * request выполняет запрос и возвращает разобранный JSON; ошибки превращает в ApiError.
 * signal прерывает запрос; для долгих операций (воспроизведение) это останавливает их в демоне.
 */
async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  signal?: AbortSignal,
): Promise<T> {
  // Отправляем запрос; сетевой сбой означает, что демон не запущен.
  let resp: Response;
  try {
    resp = await fetch("/api/v1" + path, {
      method,
      headers: {
        "Accept-Language": lang(),
        ...(body === undefined ? {} : { "Content-Type": "application/json" }),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
      credentials: "same-origin",
      signal,
    });
  } catch {
    // Прерванный запрос — остановка по просьбе пользователя.
    if (signal?.aborted) throw new ApiError(0, { code: "api.stopped", message: "stopped" });
    throw new ApiError(0, { code: "net", message: "mKey is not running" });
  }

  // Разбираем ответ; ошибка API приходит в поле error.
  const data: unknown = await resp.json().catch(() => ({}));
  if (!resp.ok) {
    const err = (data as { error?: ApiErrorBody }).error;
    throw new ApiError(resp.status, err ?? { code: "http", message: resp.statusText });
  }
  return data as T;
}

/** upload отправляет файл (архив .zip) телом запроса и возвращает разобранный JSON. */
async function upload<T>(path: string, file: Blob): Promise<T> {
  let resp: Response;
  try {
    resp = await fetch("/api/v1" + path, {
      method: "POST",
      headers: { "Accept-Language": lang(), "Content-Type": "application/zip" },
      body: file,
      credentials: "same-origin",
    });
  } catch {
    throw new ApiError(0, { code: "net", message: "mKey is not running" });
  }
  const data: unknown = await resp.json().catch(() => ({}));
  if (!resp.ok) {
    const err = (data as { error?: ApiErrorBody }).error;
    throw new ApiError(resp.status, err ?? { code: "http", message: resp.statusText });
  }
  return data as T;
}

/** api — методы API, которыми пользуется интерфейс. */
export const api = {
  status: () => request<Status>("GET", "/status"),
  doctor: () => request<{ checks: DoctorCheck[] }>("GET", "/doctor"),
  doctorFix: () =>
    request<{ ok?: boolean; method?: string; manual_command?: string }>("POST", "/doctor/fix", {}),
  devices: () =>
    request<{
      devices: InputDevice[];
      status?: { denied?: string[] };
      auto_ids?: Record<string, string>;
    }>("GET", "/devices"),
  renameDevice: (device: string, control: string, name: string) =>
    request<{ ok: boolean }>("POST", "/devices/rename", { device, control, name }),
  virtualDevices: () =>
    request<{
      devices: VirtualDeviceInfo[];
      templates: string[];
      template_info?: VirtualTemplateInfo[];
    }>("GET", "/devices/virtual"),
  deviceSettings: () => request<{ auto_ids: string }>("GET", "/settings/devices"),
  setDeviceSettings: (autoIds: string) =>
    request<{ auto_ids: string }>("PUT", "/settings/devices", { auto_ids: autoIds }),
  inspectDevice: (ref: string) =>
    request<{ device: DeviceDetails }>("GET", `/devices/inspect?ref=${enc(ref)}`),
  registry: () => request<Registry>("GET", "/registry"),
  places: () => request<{ places: PlaceInfo[] }>("GET", "/places"),
  logs: (lines: number) => request<{ lines: string[] }>("GET", `/logs?lines=${lines}`),

  // Обновления.
  updateInfo: () => request<UpdateInfo>("GET", "/update"),
  checkUpdate: () => request<UpdateInfo>("POST", "/update/check", {}),
  applyUpdate: () => request<UpdateInfo>("POST", "/update/apply", {}),
  setUpdateCheck: (check: boolean) => request<UpdateInfo>("PUT", "/settings/update", { check }),

  // Плагины.
  plugins: () => request<{ plugins: PluginInfo[]; dir: string }>("GET", "/plugins"),
  installPlugin: (path: string) => request<PluginInfo>("POST", "/plugins/install", { path }),
  uploadPlugin: (file: Blob) => upload<PluginInfo>("/plugins/install", file),
  setPluginActive: (id: string, on: boolean) =>
    request<{ ok: boolean }>("POST", `/plugins/${enc(id)}/${on ? "enable" : "disable"}`, {}),
  removePlugin: (id: string) => request<{ ok: boolean }>("DELETE", `/plugins/${enc(id)}`),
  pluginLog: (id: string) =>
    request<{ lines: string[] }>("GET", `/plugins/${enc(id)}/log?lines=300`),

  // Проекты.
  projects: () => request<{ dir: string; projects: ProjectInfo[] }>("GET", "/projects"),
  project: (id: string) => request<ProjectFile>("GET", `/projects/${enc(id)}`),
  saveProject: (id: string, project: Project) =>
    request<{ ok: boolean }>("PUT", `/projects/${enc(id)}`, { project }),
  saveProjectRaw: (id: string, raw: string) =>
    request<{ ok: boolean }>("PUT", `/projects/${enc(id)}`, { raw }),
  validateProject: (project: Project) =>
    request<{ ok: boolean }>("POST", "/projects/validate", { project }),
  createProject: (id: string, template = "") =>
    request<{ id: string; scripts?: ProjectScript[] }>("POST", "/projects", { id, template }),
  importProject: (name: string, content: string) =>
    request<{ id: string; scripts?: ProjectScript[] }>("POST", "/projects/import", {
      name,
      content,
    }),
  deleteProject: (id: string) => request<{ ok: boolean }>("DELETE", `/projects/${enc(id)}`),
  setProjectEnabled: (id: string, on: boolean) =>
    request<{ ok: boolean }>("POST", `/projects/${enc(id)}/${on ? "enable" : "disable"}`, {}),
  templates: () => request<{ templates: Template[] | null }>("GET", "/templates"),

  // События.
  events: () => request<{ events: EventStatus[] | null }>("GET", "/events"),
  runEvent: (project: string, event: string) =>
    request<{ ok: boolean }>("POST", `/events/${enc(project)}/${enc(event)}/run`, {}),

  // Макросы и блоки.
  send: (sequence: string) => request<{ ok: boolean }>("POST", "/send", { sequence }),
  dslToActions: (text: string) =>
    request<{ actions: Action[] }>("POST", "/dsl/to_actions", { text }),
  actionsToDsl: (actions: Action[]) =>
    request<{ text: string }>("POST", "/actions/to_dsl", { actions }),
  captureKey: (combo: boolean, timeoutMs = 15000) =>
    request<CapturedKey>("POST", "/capture/key", { combo, timeout_ms: timeoutMs }),

  // Запись и воспроизведение.
  recordings: () =>
    request<{ recordings: RecordingInfo[]; current?: RecordingInfo }>("GET", "/recordings"),
  startRecording: (name: string) => request<RecordingInfo>("POST", "/recordings/start", { name }),
  stopRecording: () => request<RecordingInfo>("POST", "/recordings/stop", {}),
  convertRecording: (name: string, simplify: boolean) =>
    request<{ project: string }>("POST", `/recordings/${enc(name)}/convert`, { simplify }),
  deleteRecording: (name: string) => request<{ ok: boolean }>("DELETE", `/recordings/${enc(name)}`),
  play: (name: string, speed: number, repeat: number, fixedPauseMs: number, signal?: AbortSignal) =>
    request<{ ok: boolean }>(
      "POST",
      "/play",
      { name, speed, repeat, fixed_pause_ms: fixedPauseMs },
      signal,
    ),

  // Системные сочетания mKey.
  hotkeys: () => request<{ record?: string; emergency?: string }>("GET", "/settings/hotkeys"),
  setHotkeys: (h: { record?: string; emergency?: string }) =>
    request<{ record?: string; emergency?: string }>("PUT", "/settings/hotkeys", h),
  recordSettings: () => request<RecordSettings>("GET", "/settings/recording"),
  setRecordSettings: (s: RecordSettings) =>
    request<RecordSettings>("PUT", "/settings/recording", s),
  interfaceSettings: () => request<InterfaceSettings>("GET", "/settings/interface"),
  setInterfaceSettings: (s: InterfaceSettings) =>
    request<InterfaceSettings>("PUT", "/settings/interface", s),

  // Управление программой.
  stopAll: () => request<{ ok: boolean }>("POST", "/stop", {}),
  emergency: () => request<{ ok: boolean }>("POST", "/emergency", {}),
  resume: () => request<{ ok: boolean }>("POST", "/resume", {}),
  uninstall: (keepConfig: boolean) =>
    request<{ ok: boolean }>("POST", "/uninstall", { keep_config: keepConfig }),
};

/** enc кодирует часть пути. */
function enc(s: string): string {
  return encodeURIComponent(s);
}
