<!--
  RowsEditor — раскладка виртуального устройства таблицей: слева — что нажимается («газ», «левый
  стик вверх», «кнопка 7»), справа — чем: клавиша с кнопкой «Нажмите клавишу…» или, для мыши и
  стиков, подпись источника. Любую строку можно очистить.
  Props: rows — строки раскладки; device — устройство-источник (его имя убирается из пойманной
  клавиши: «Геймпад.South» → «South»); onchange(rows) — новые строки.
-->
<script lang="ts">
  import KeyCapture from "../../lib/components/KeyCapture.svelte";
  import { t } from "../../lib/i18n/index.svelte";
  import { rowLabel, sourceLabel } from "./labels";
  import type { Row } from "./presets";

  let {
    rows,
    device = "",
    onchange,
  }: { rows: Row[]; device?: string; onchange: (rows: Row[]) => void } = $props();

  /** setFrom назначает строке id источник from ("" — убрать); имя устройства-источника отрезается. */
  function setFrom(id: string, from: string): void {
    const prefix = device ? device + "." : "";
    const clean = prefix && from.startsWith(prefix) ? from.slice(prefix.length) : from;
    onchange(
      rows.map((r) => (r.id === id ? { ...r, from: clean, analog: clean ? r.analog : false } : r)),
    );
  }
</script>

<div class="rows">
  {#each rows as r (r.id)}
    <span class="what">{rowLabel(r, t)}</span>
    <span class="src">
      {#if r.analog && r.from}
        <!-- Мышь или ось: подпись источника -->
        <span class="analog">{sourceLabel(r, t)}</span>
      {:else}
        <KeyCapture value={r.from} placeholder={t("vd.none")} onchange={(v) => setFrom(r.id, v)} />
      {/if}
      {#if r.from}
        <button class="small ghost" onclick={() => setFrom(r.id, "")}>{t("vd.unset")}</button>
      {/if}
    </span>
  {/each}
</div>

<style>
  .rows {
    display: grid;
    grid-template-columns: minmax(0, max-content) minmax(0, 1fr);
    gap: 4px 14px;
    align-items: center;
  }
  .src {
    display: flex;
    gap: 6px;
    align-items: center;
    flex-wrap: wrap;
  }
  .analog {
    padding: 4px 8px;
    border-radius: var(--radius-s);
    background: var(--surface-2);
  }
  /* На узком экране — строка под строкой. */
  @media (max-width: 560px) {
    .rows {
      grid-template-columns: minmax(0, 1fr);
    }
    .what {
      margin-top: 6px;
      font-weight: 600;
    }
  }
</style>
