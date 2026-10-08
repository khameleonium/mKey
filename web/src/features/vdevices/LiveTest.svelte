<!--
  LiveTest — проверка виртуального устройства вживую (FR-VD-8): mKey ≈15 раз в секунду
  спрашивает, что нажато и куда наклонены оси у подключённого устройства, и показывает это —
  кнопки подсвечиваются, оси заполняются полосками. Это то же, что получает игра.
  Props: name — имя устройства в макросах; system — имя в системе («mKey wheel»); info — состав
  шаблона (кнопки и оси по порядку); onclose() — закрыть.
-->
<script lang="ts">
  import { api } from "../../lib/api";
  import Modal from "../../lib/components/Modal.svelte";
  import { t } from "../../lib/i18n/index.svelte";
  import type { VirtualState, VirtualTemplateInfo } from "../../lib/types";

  let {
    name,
    system,
    info,
    onclose,
  }: { name: string; system: string; info?: VirtualTemplateInfo; onclose: () => void } = $props();

  /** POLL_MS — как часто спрашивать состояние: 15 раз в секунду — плавно для глаза. */
  const POLL_MS = 66;
  /** ONE_SIDED — оси «от нуля» (0…1): курки, педали, РУД, ползунок; остальные — −1…1. */
  const ONE_SIDED = new Set(["LT", "RT", "Gas", "Brake", "Clutch", "Throttle", "Slider"]);

  /** snap — последнее состояние; off — устройство не подключено. */
  let snap = $state<VirtualState | null>(null);
  let off = $state(false);

  // Опрос состояния, пока окно открыто (ошибка — устройство выключено).
  $effect(() => {
    let alive = true;
    const tick = async (): Promise<void> => {
      try {
        snap = await api.virtualState(name);
        off = false;
      } catch {
        off = true;
      }
      if (alive) timer = setTimeout(() => void tick(), POLL_MS);
    };
    let timer = setTimeout(() => void tick(), 0);
    return () => {
      alive = false;
      clearTimeout(timer);
    };
  });

  /** axes — оси для показа: по составу шаблона, иначе — что прислало устройство. */
  let axes = $derived(info?.axes ?? Object.keys(snap?.axes ?? {}));

  /** label — подпись кнопки или оси: своя («газ»), иначе имя для макросов. */
  function label(control: string): string {
    const s = t(`vd.ctl.${control}`);
    return s === `vd.ctl.${control}` ? control : `${control} — ${s}`;
  }

  /** bar — заливка полоски оси: с центром — от середины, «от нуля» — от левого края (в процентах). */
  function bar(axis: string, v: number): { left: number; width: number } {
    if (ONE_SIDED.has(axis)) return { left: 0, width: Math.max(0, Math.min(1, v)) * 100 };
    const x = Math.max(-1, Math.min(1, v));
    return x < 0 ? { left: 50 + x * 50, width: -x * 50 } : { left: 50, width: x * 50 };
  }
</script>

<Modal title={t("vd.test_title", { system })} wide {onclose}>
  <p class="muted">{t("vd.test_hint")}</p>
  {#if off}
    <div class="note">{t("vd.test_off")}</div>
  {:else if snap}
    <!-- Оси -->
    {#if axes.length}
      <h3>{t("vd.test_axes")}</h3>
      <div class="axes">
        {#each axes as a (a)}
          {@const v = snap.axes[a] ?? 0}
          {@const b = bar(a, v)}
          <span class="axis-name">{label(a)}</span>
          <span class="track" class:centered={!ONE_SIDED.has(a)}>
            <span class="fill" style="left: {b.left}%; width: {b.width}%"></span>
          </span>
          <span class="num">{v.toFixed(2)}</span>
        {/each}
      </div>
    {/if}

    <!-- Кнопки -->
    <h3>{t("vd.test_buttons")}</h3>
    <div class="buttons">
      {#each info?.buttons ?? snap.buttons as btn (btn)}
        <span class="btn" class:on={snap.buttons.includes(btn)}>{btn}</span>
      {/each}
    </div>
    <p class="muted small">{t("vd.emergency")}</p>
  {/if}
</Modal>

<style>
  .axes {
    display: grid;
    grid-template-columns: minmax(0, max-content) minmax(80px, 1fr) 3.5em;
    gap: 6px 12px;
    align-items: center;
  }
  .track {
    position: relative;
    height: 12px;
    border-radius: 6px;
    background: var(--surface-2);
    overflow: hidden;
  }
  /* Черта посередине у осей с центром. */
  .track.centered::after {
    content: "";
    position: absolute;
    left: 50%;
    top: 0;
    bottom: 0;
    width: 1px;
    background: var(--border);
  }
  .fill {
    position: absolute;
    top: 0;
    bottom: 0;
    background: var(--accent);
  }
  .num {
    font-family: var(--mono);
    text-align: right;
  }
  .buttons {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }
  .btn {
    padding: 3px 8px;
    border: 1px solid var(--border);
    border-radius: var(--radius-s);
    font-family: var(--mono);
    font-size: 0.85rem;
  }
  /* Нажатая кнопка подсвечивается. */
  .btn.on {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  h3 {
    margin: 14px 0 6px;
  }
  .small {
    font-size: 0.85rem;
    margin-top: 12px;
  }
</style>
