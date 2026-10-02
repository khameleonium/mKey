<!--
  DryRunDialog — окно «Сухой прогон» (FR-UI-6): что сделает событие и когда, по шагам, без
  нажатий. Слева — время начала шага в секундах, справа — шаг с отступом по вложенности
  (внутри «Повторять» и «Если»). Внизу — примерная длительность.
  Props: title — название события; result — таймлайн от демона; onclose() — закрыть.
-->
<script lang="ts">
  import Modal from "../../lib/components/Modal.svelte";
  import { lang, t } from "../../lib/i18n/index.svelte";
  import type { Condition, DryRun } from "../../lib/types";
  import { describe, seconds, type Names } from "./dryrun";
  import { useEditor } from "./editor.svelte";
  import { summarize } from "./summary";

  let { title, result, onclose }: { title: string; result: DryRun; onclose: () => void } = $props();
  const ed = useEditor();

  /** names — тексты из реестра: название действия; условие — название и краткое содержание
   *  параметров («Переменная (clicks, >=, 3)»). Вида нет в реестре — его ID. */
  const names: Names = {
    action: (type) => ed.entry("action", type)?.name ?? type,
    condition: (c: Condition) => {
      const entry = ed.entry("condition", c.type);
      const details = summarize(entry?.params_schema, c.params ?? {}, t("editor.ms"));
      return (entry?.name ?? c.type) + (details ? ` (${details})` : "");
    },
  };
</script>

<Modal title={t("dry.title", { name: title })} wide {onclose}>
  <p class="muted">{t("dry.hint")}</p>

  <!-- Таймлайн: время и шаг -->
  {#if result.steps.length === 0}
    <p>{t("dry.empty")}</p>
  {:else}
    <ol class="timeline">
      {#each result.steps as s, i (i)}
        <li class:group={s.kind === "group"} class:other={s.kind === "action" || s.kind === "note"}>
          <span class="at">{seconds(s.at_ms, lang())} {t("dry.sec")}</span>
          <span class="what" style:padding-left="{s.depth * 1.5}em"
            >{describe(s, t, names, lang())}</span
          >
        </li>
      {/each}
    </ol>
  {/if}

  <!-- Итог: длительность и пометки -->
  {#snippet footer()}
    <span class="total">
      {result.open
        ? t("dry.total_open", { sec: seconds(result.total_ms, lang()) })
        : t("dry.total", { sec: seconds(result.total_ms, lang()) })}
      {#if result.truncated}<br />{t("dry.truncated")}{/if}
    </span>
    <span class="spacer"></span>
    <button onclick={onclose}>{t("common.close")}</button>
  {/snippet}
</Modal>

<style>
  .timeline {
    list-style: none;
    margin: 0;
    padding: 0;
    max-height: 60vh;
    overflow: auto;
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }
  li {
    display: flex;
    gap: 12px;
    padding: 4px 10px;
    border-bottom: 1px solid var(--border);
  }
  li:last-child {
    border-bottom: none;
  }
  li.group {
    font-weight: 600;
  }
  li.other .what {
    font-style: italic;
  }
  .at {
    flex: none;
    width: 6em;
    text-align: right;
    font-variant-numeric: tabular-nums;
    color: var(--muted);
  }
  .what {
    overflow-wrap: anywhere;
  }
  .total {
    color: var(--muted);
  }
</style>
