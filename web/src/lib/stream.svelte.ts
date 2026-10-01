// Поток новостей от демона (Server-Sent Events, GET /api/v1/stream) и общее состояние программы:
// связь с демоном, приостановка после экстренной остановки, выполняющиеся события.
// Страницы подписываются на изменения через onTopic, чтобы перечитать свои данные.
import { api } from "./api";
import { emit } from "./topics";

export { onTopic } from "./topics";

/** Общее состояние программы (реактивное). */
export const live = $state({
  /** connected — связь с демоном есть. */
  connected: false,
  /** suspended — mKey приостановлен после экстренной остановки. */
  suspended: false,
  /** running — сколько выполнений идёт по каждому событию ("проект/событие" → число). */
  running: {} as Record<string, number>,
  /** version — версия демона. */
  version: "",
});

/** refreshStatus перечитывает состояние демона. */
export async function refreshStatus(): Promise<void> {
  try {
    const st = await api.status();
    live.connected = true;
    live.suspended = st.grab_suspended;
    live.version = st.version;
  } catch {
    live.connected = false;
  }
}

/** Темы потока, на которые реагирует само состояние. */
const TOPICS = [
  "store.projects_changed",
  "store.project_error",
  "engine.project_error",
  "engine.event_started",
  "engine.event_finished",
  "input.emergency",
  "input.resumed",
  "input.device_added",
  "input.device_removed",
  "input.access_changed",
  "recorder.started",
  "recorder.stopped",
  "inspector.auto_ids_changed",
];

/** connect открывает поток и переподключается при обрыве (демон перезапущен). */
export function connect(): void {
  const es = new EventSource("/api/v1/stream");

  // Поток открыт: связь есть, состояние перечитываем (могли пропустить события).
  es.onopen = () => {
    live.running = {};
    void refreshStatus();
  };

  // Обрыв: EventSource переподключается сам; пока — «нет связи».
  es.onerror = () => {
    live.connected = false;
  };

  // События потока.
  for (const topic of TOPICS) {
    es.addEventListener(topic, (e: MessageEvent<string>) => {
      let data: unknown = null;
      try {
        data = JSON.parse(e.data);
      } catch {
        // Пустые данные у событий без полезной нагрузки.
      }
      handle(topic, data);
      emit(topic, data);
    });
  }
}

/** handle обновляет общее состояние по событию потока. */
function handle(topic: string, data: unknown): void {
  const ref = data as { project?: string; event?: string } | null;
  const key = ref ? `${ref.project ?? ""}/${ref.event ?? ""}` : "";
  switch (topic) {
    case "engine.event_started":
      live.running[key] = (live.running[key] ?? 0) + 1;
      break;
    case "engine.event_finished":
      live.running[key] = Math.max(0, (live.running[key] ?? 0) - 1);
      break;
    case "input.emergency":
      live.suspended = true;
      live.running = {};
      break;
    case "input.resumed":
      live.suspended = false;
      break;
  }
}
