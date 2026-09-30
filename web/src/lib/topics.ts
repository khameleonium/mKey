// Подписки страниц на темы потока новостей демона (lib/stream.svelte.ts рассылает события).
// Обычный модуль, а не .svelte.ts: подписчики — не реактивное состояние.

/** Подписчики по темам. */
const listeners = new Map<string, Set<(data: unknown) => void>>();

/** onTopic подписывает на тему потока ("store.projects_changed"…); возвращает отписку. */
export function onTopic(topic: string, fn: (data: unknown) => void): () => void {
  let set = listeners.get(topic);
  if (!set) {
    set = new Set();
    listeners.set(topic, set);
  }
  set.add(fn);
  return () => set.delete(fn);
}

/** emit передаёт событие потока подписчикам темы. */
export function emit(topic: string, data: unknown): void {
  listeners.get(topic)?.forEach((fn) => fn(data));
}
