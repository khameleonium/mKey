// Клиент HTTP API демона. Все запросы идут на тот же адрес, с которого открыт интерфейс;
// вход выполнен по cookie (ссылка /?t=…). Язык ответов (тексты ошибок, названия блоков)
// задаётся заголовком Accept-Language по языку интерфейса.
import { lang } from "./i18n/index.svelte";
import type {
  Action,
  ApiErrorBody,
  CapturedKey,
  RecordingInfo,
  DoctorCheck,
  EventStatus,
  InputDevice,
  Project,
  ProjectFile,
  ProjectInfo,
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

/** api — методы API, которыми пользуется интерфейс. */
export const api = {
  status: () => request<Status>("GET", "/status"),
  doctor: () => request<{ checks: DoctorCheck[] }>("GET", "/doctor"),
  doctorFix: () =>
    request<{ ok?: boolean; method?: string; manual_command?: string }>("POST", "/doctor/fix", {}),
  devices: () => request<{ devices: InputDevice[] }>("GET", "/devices"),
  registry: () => request<Registry>("GET", "/registry"),
  logs: (lines: number) => request<{ lines: string[] }>("GET", `/logs?lines=${lines}`),

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
    request<{ id: string }>("POST", "/projects", { id, template }),
  importProject: (name: string, content: string) =>
    request<{ id: string }>("POST", "/projects/import", { name, content }),
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
  deleteRecording: (name: string) => request<{ ok: boolean }>("DELETE", `/recordings/${enc(name)}`),
  play: (name: string, speed: number, repeat: number, signal?: AbortSignal) =>
    request<{ ok: boolean }>("POST", "/play", { name, speed, repeat }, signal),

  // Системные сочетания mKey.
  hotkeys: () => request<{ record?: string; emergency?: string }>("GET", "/settings/hotkeys"),
  setHotkeys: (h: { record?: string; emergency?: string }) =>
    request<{ record?: string; emergency?: string }>("PUT", "/settings/hotkeys", h),

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
