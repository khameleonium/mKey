<!--
  DevicesPage — устройства ввода (FR-UI-1.5): список клавиатур, мышей, геймпадов и т.п.
  и монитор нажатий (FR-DEV-8): после «Следить за нажатиями» mKey непрерывно показывает журнал —
  когда, на каком устройстве и какая кнопка нажата (с именем для макросов), пока не нажать «Стоп»
  или не удержать Esc 2 секунды. Нажатия никуда не сохраняются.
  Список устройств обновляется при подключении и отключении.
  Props: нет.
-->
<script lang="ts">
  import { api } from "../../lib/api";
  import { t } from "../../lib/i18n/index.svelte";
  import { onTopic } from "../../lib/stream.svelte";
  import { errorText, toast } from "../../lib/toast.svelte";
  import type { InputDevice } from "../../lib/types";

  /** Данные страницы. */
  let devices = $state<InputDevice[]>([]);
  let denied = $state<string[]>([]);
  /** Запись журнала монитора (как её присылает GET /api/v1/input/watch). */
  interface WatchEntry {
    time: string;
    device_name: string;
    device: string;
    kind: "key" | "axis" | "wheel" | "move";
    name: string;
    kernel: string;
    action?: "down" | "up";
    value?: number;
    dy?: number;
  }

  /** watching — монитор включён; moves — показывать перемещения мыши; log — записи (новые сверху). */
  let watching = $state(false);
  let moves = $state(false);
  let log = $state<(WatchEntry & { id: number })[]>([]);
  /** source — поток событий; escTimer — таймер «Esc удерживается 2 секунды»; nextId — номера записей. */
  let source: EventSource | null = null;
  let escTimer: ReturnType<typeof setTimeout> | null = null;
  let nextId = 0;

  /** Наибольшее число записей в журнале (старые убираются). */
  const MAX_LOG = 300;
  /** ESC_HOLD_MS — сколько удерживать Esc, чтобы остановить монитор. */
  const ESC_HOLD_MS = 2000;

  /** startWatch включает монитор нажатий. */
  function startWatch(): void {
    stopWatch();
    log = [];
    watching = true;
    source = new EventSource("/api/v1/input/watch" + (moves ? "?moves=1" : ""));
    source.addEventListener("input", (e: MessageEvent<string>) => {
      const entry = JSON.parse(e.data) as WatchEntry;
      // Удержание Esc 2 секунды — остановка (отпускание раньше отменяет таймер).
      if (entry.kind === "key" && entry.kernel === "KEY_ESC") {
        if (entry.action === "down" && !escTimer) escTimer = setTimeout(stopWatch, ESC_HOLD_MS);
        if (entry.action === "up" && escTimer) {
          clearTimeout(escTimer);
          escTimer = null;
        }
      }
      nextId += 1;
      log = [{ ...entry, id: nextId }, ...log].slice(0, MAX_LOG);
    });
    source.onerror = () => {
      if (watching) toast(t("devices.watch_lost"), "error");
      stopWatch();
    };
  }

  /** stopWatch выключает монитор. */
  function stopWatch(): void {
    source?.close();
    source = null;
    watching = false;
    if (escTimer) clearTimeout(escTimer);
    escTimer = null;
  }

  // Уход со страницы выключает монитор.
  $effect(() => () => stopWatch());

  /** describe возвращает описание события для журнала. */
  function describe(e: WatchEntry): string {
    switch (e.kind) {
      case "key":
        return t(e.action === "up" ? "devices.ev_up" : "devices.ev_down", { key: `{${e.name}}` });
      case "axis":
        return t("devices.ev_axis", { axis: e.name, value: e.value ?? 0 });
      case "wheel":
        return t("devices.ev_wheel", {
          value: (e.value ?? 0) > 0 ? "+" + String(e.value) : String(e.value),
        });
      default:
        return t("devices.ev_move", { dx: e.value ?? 0, dy: e.dy ?? 0 });
    }
  }

  /** clock возвращает время события с миллисекундами. */
  function clock(iso: string): string {
    const d = new Date(iso);
    return d.toLocaleTimeString() + "." + String(d.getMilliseconds()).padStart(3, "0");
  }

  /** load перечитывает устройства. */
  async function load(): Promise<void> {
    try {
      const r = (await api.devices()) as { devices: InputDevice[]; status?: { denied?: string[] } };
      devices = r.devices;
      denied = r.status?.denied ?? [];
    } catch (e) {
      toast(errorText(e), "error");
    }
  }

  // Загрузка при открытии и при подключении/отключении устройств.
  $effect(() => {
    void load();
    const offs = ["input.device_added", "input.device_removed", "input.access_changed"].map(
      (topic) => onTopic(topic, () => void load()),
    );
    return () => offs.forEach((off) => off());
  });

  /** hex4 записывает число как 4 шестнадцатеричные цифры (VID:PID). */
  function hex4(n: number): string {
    return n.toString(16).padStart(4, "0");
  }
</script>

<h1>{t("devices.title")}</h1>

<!-- Монитор нажатий -->
<div class="card identify">
  <div class="grow">
    <h2>{t("devices.watch")}</h2>
    <p class="muted">{t("devices.watch_hint")}</p>
  </div>
  {#if watching}
    <button class="danger" onclick={stopWatch}>■ {t("devices.watch_stop")}</button>
  {:else}
    <label class="row"
      ><input type="checkbox" bind:checked={moves} /> {t("devices.watch_moves")}</label
    >
    <button class="primary" onclick={startWatch}>● {t("devices.watch_start")}</button>
  {/if}
  {#if watching || log.length}
    <div class="log">
      {#if watching && log.length === 0}<div class="muted">{t("devices.watch_waiting")}</div>{/if}
      {#each log as e (e.id)}
        <div class="line" class:up={e.action === "up"}>
          <span class="time">{clock(e.time)}</span>
          <span class="what">{describe(e)}</span>
          <span class="dev">{e.device_name || e.device}</span>
          <span class="kernel muted">{e.kernel}</span>
        </div>
      {/each}
    </div>
  {/if}
</div>

{#if denied.length}
  <div class="note warn">{t("devices.denied", { n: denied.length })}</div>
{/if}

<!-- Список устройств -->
<div class="list">
  {#each devices as d (d.info.path)}
    <div class="card dev">
      <b>{d.info.name}</b>
      <span class="kinds">
        {#each d.kinds as k (k)}<span class="tag">{t("devices.kind." + k)}</span>{/each}
      </span>
      <details>
        <summary class="muted">{t("common.more")}</summary>
        <div class="muted small">
          {d.info.path} · VID:PID {hex4(d.info.id.vendor)}:{hex4(d.info.id.product)}
          {#if d.info.phys}· {d.info.phys}{/if}
        </div>
      </details>
    </div>
  {/each}
  {#if devices.length === 0}
    <div class="note">{t("devices.none")}</div>
  {/if}
</div>

<style>
  .identify {
    display: flex;
    flex-wrap: wrap;
    gap: 12px 20px;
    align-items: center;
    margin-bottom: 16px;
  }
  .identify h2,
  .identify p {
    margin: 0;
  }
  .grow {
    flex: 1;
  }
  .log {
    flex-basis: 100%;
    max-height: 420px;
    overflow: auto;
    font-family: var(--mono);
    font-size: 0.85rem;
    background: var(--surface-2);
    border-radius: var(--radius-s);
    padding: 6px 10px;
  }
  .line {
    display: grid;
    grid-template-columns: 9em minmax(12em, 1fr) minmax(10em, 1.5fr) 12em;
    gap: 10px;
    padding: 2px 0;
    border-bottom: 1px solid var(--border);
  }
  .line.up .what {
    color: var(--muted);
  }
  .what {
    font-weight: 600;
  }
  .dev,
  .kernel {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .list {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
    gap: 10px;
    margin-top: 12px;
  }
  .dev {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 12px;
  }
  .kinds {
    display: flex;
    gap: 4px;
    flex-wrap: wrap;
  }
  .tag {
    font-size: 0.8rem;
    padding: 1px 8px;
    border-radius: 10px;
    background: var(--accent-soft);
  }
  .small {
    font-size: 0.82rem;
    word-break: break-all;
  }
  summary {
    cursor: pointer;
    font-size: 0.85rem;
  }
</style>
