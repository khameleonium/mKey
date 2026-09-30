<!--
  DevicesPage — устройства ввода (FR-UI-1.5): список клавиатур, мышей, геймпадов и т.п.
  и «Узнать кнопку» — нажмите любую кнопку на любом устройстве, и mKey покажет её имя
  для макросов. Список обновляется при подключении и отключении устройств.
  Props: нет.
-->
<script lang="ts">
  import { api, ApiError } from "../../lib/api";
  import { t } from "../../lib/i18n/index.svelte";
  import { onTopic } from "../../lib/stream.svelte";
  import { errorText, toast } from "../../lib/toast.svelte";
  import type { CapturedKey, InputDevice } from "../../lib/types";

  /** Данные страницы. */
  let devices = $state<InputDevice[]>([]);
  let denied = $state<string[]>([]);
  /** waiting — ждём нажатия; captured — последняя пойманная кнопка. */
  let waiting = $state(false);
  let captured = $state<CapturedKey | null>(null);

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

  /** identify ждёт нажатия любой кнопки и показывает её имя. */
  async function identify(): Promise<void> {
    waiting = true;
    captured = null;
    try {
      captured = await api.captureKey(false, 30000);
    } catch (e) {
      toast(e instanceof ApiError && e.status === 408 ? t("key.timeout") : errorText(e), "error");
    } finally {
      waiting = false;
    }
  }

  /** hex4 записывает число как 4 шестнадцатеричные цифры (VID:PID). */
  function hex4(n: number): string {
    return n.toString(16).padStart(4, "0");
  }
</script>

<h1>{t("devices.title")}</h1>

<!-- «Узнать кнопку» -->
<div class="card identify">
  <div>
    <h2>{t("devices.identify")}</h2>
    <p class="muted">{t("devices.identify_hint")}</p>
  </div>
  <button class="primary" disabled={waiting} onclick={identify}>
    {waiting ? t("key.waiting") : t("devices.identify_button")}
  </button>
  {#if captured}
    <div class="result note ok">
      <div class="big"><code>{`{${captured.name}}`}</code></div>
      <div>{t("devices.captured_on", { device: captured.device_name || captured.device })}</div>
      <details>
        <summary>{t("common.more")}</summary>
        <div class="muted">
          {t("devices.code", { code: captured.code, kernel: captured.kernel })}
        </div>
      </details>
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
  .result {
    flex-basis: 100%;
  }
  .big {
    font-size: 1.5rem;
    margin-bottom: 4px;
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
