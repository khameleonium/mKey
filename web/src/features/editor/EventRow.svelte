<!--
  EventRow — одно событие в листе событий (FR-UI-2): слева «Когда» (триггеры и условия),
  справа «Делать» (блоки действий). В заголовке — название, переключатель вкл/выкл,
  индикатор «выполняется», «Запустить сейчас», «Сухой прогон», копия, сдвиг, удаление, свёртывание.
  Props: ev — событие; index — его номер; total — всего событий; onrun(ev) — запустить;
  ondry(ev) — показать сухой прогон.
-->
<script lang="ts">
  import Toggle from "../../lib/components/Toggle.svelte";
  import { t } from "../../lib/i18n/index.svelte";
  import { live } from "../../lib/stream.svelte";
  import type { EditorEvent } from "../../lib/blocks";
  import type { Trigger } from "../../lib/types";
  import BlockList from "./BlockList.svelte";
  import CondList from "./CondList.svelte";
  import TriggerWizard from "./TriggerWizard.svelte";
  import { useEditor } from "./editor.svelte";
  import { summarize } from "./summary";

  let {
    ev,
    index,
    total,
    onrun,
    ondry,
  }: {
    ev: EditorEvent;
    index: number;
    total: number;
    onrun: (ev: EditorEvent) => void;
    ondry: (ev: EditorEvent) => void;
  } = $props();
  const ed = useEditor();

  /** collapsed — событие свёрнуто; editing — номер изменяемого триггера (-1 — новый, null — нет). */
  let collapsed = $state(false);
  let editing = $state<number | null>(null);
  let showConds = $state(false);

  /** Сколько выполнений этого события идёт сейчас (по потоку новостей). */
  let running = $derived(live.running[`${ed.id}/${ev.data.id}`] ?? 0);

  /** Место ошибки последней проверки, если она в этом событии (-1 — часть целиком). */
  let bad = $derived(ed.problem?.event === ev.data.id ? ed.problem : null);
  let badTrigger = $derived(bad?.part === "trigger" ? bad.index : -2);
  let badCond = $derived(bad?.part === "condition" ? bad.index : -2);
  let badAction = $derived(bad?.part === "action" ? bad.index : -2);

  /** triggerText — название и параметры триггера одной строкой. */
  function triggerText(tr: Trigger): { name: string; details: string } {
    const entry = ed.entry("trigger", tr.type);
    return {
      name: entry?.name ?? tr.type,
      details: summarize(entry?.params_schema, tr.params ?? {}, t("editor.ms")),
    };
  }
</script>

<article
  class="event"
  class:off={ev.data.enabled === false}
  class:running={running > 0}
  class:has-problem={bad !== null}
>
  <!-- Заголовок события -->
  <header class="row">
    <button
      class="ghost small"
      title={t(collapsed ? "editor.expand" : "editor.collapse")}
      onclick={() => (collapsed = !collapsed)}
    >
      {collapsed ? "▸" : "▾"}
    </button>
    <Toggle
      checked={ev.data.enabled !== false}
      title={t("editor.event_enabled")}
      onchange={(on) =>
        on
          ? ed.unset(ev.data as Record<string, unknown>, "enabled")
          : ed.set(ev.data, "enabled", false)}
    />
    <input
      class="name"
      value={ev.data.name ?? ""}
      placeholder={t("editor.event_name_placeholder", { n: index + 1 })}
      onchange={(e) => ed.set(ev.data, "name", e.currentTarget.value)}
    />
    {#if running > 0}<span class="badge">● {t("editor.running")}</span>{/if}
    <span class="spacer"></span>
    <button class="small" title={t("editor.run_now_hint")} onclick={() => onrun(ev)}
      >▶ {t("editor.run_now")}</button
    >
    <button class="small" title={t("dry.button_hint")} onclick={() => ondry(ev)}
      >⧗ {t("dry.button")}</button
    >
    <button
      class="ghost small"
      title={t("editor.move_up")}
      disabled={index === 0}
      onclick={() => ed.moveEvent(ev, -1)}>↑</button
    >
    <button
      class="ghost small"
      title={t("editor.move_down")}
      disabled={index === total - 1}
      onclick={() => ed.moveEvent(ev, 1)}>↓</button
    >
    <button class="ghost small" title={t("editor.duplicate")} onclick={() => ed.duplicateEvent(ev)}
      >⧉</button
    >
    <button
      class="ghost small"
      title={t("common.delete")}
      onclick={() => {
        if (confirm(t("editor.delete_event_confirm"))) ed.removeEvent(ev);
      }}>✕</button
    >
  </header>

  {#if !collapsed}
    <div class="cols">
      <!-- «Когда»: триггеры и условия -->
      <section class="when" class:bad={badTrigger === -1}>
        <h3>{t("editor.when")}</h3>
        {#each ev.triggers as tr, i (i)}
          {@const text = triggerText(tr)}
          <div class="trigger" class:bad={badTrigger === i}>
            <button class="ghost tbtn" onclick={() => (editing = i)}>
              <b>{text.name}</b>
              {#if text.details}<span>{text.details}</span>{/if}
            </button>
            <button
              class="ghost small"
              title={t("common.delete")}
              onclick={() => ed.removeTrigger(ev, i)}>✕</button
            >
          </div>
          {#if i < ev.triggers.length - 1}<div class="or muted">{t("editor.or")}</div>{/if}
        {/each}
        <button class="small ghost addw" onclick={() => (editing = -1)}>
          + {t(ev.triggers.length ? "editor.add_trigger_or" : "editor.add_trigger")}
        </button>

        {#if ev.conditions.length || showConds}
          <div class="conds">
            <div class="muted small-title">{t("editor.conditions")}</div>
            <CondList list={ev.conditions} bad={badCond} />
          </div>
        {:else}
          <button class="small ghost addw" onclick={() => (showConds = true)}
            >+ {t("editor.add_condition")}</button
          >
        {/if}
      </section>

      <!-- «Делать»: блоки действий -->
      <section class="do">
        <h3>{t("editor.do")}</h3>
        <BlockList list={ev.actions} bad={badAction} />
      </section>
    </div>
  {/if}
</article>

{#if editing !== null}
  {@const idx = editing}
  <TriggerWizard
    trigger={idx >= 0 ? ev.triggers[idx] : undefined}
    onsave={(tr) => {
      ed.setTrigger(ev, idx >= 0 ? idx : ev.triggers.length, tr);
      editing = null;
    }}
    onclose={() => (editing = null)}
  />
{/if}

<style>
  .event {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    box-shadow: var(--shadow);
  }
  .event.off {
    opacity: 0.6;
  }
  .event.running {
    border-color: var(--ok);
  }
  /* Событие и его часть с ошибкой последней проверки. */
  .event.has-problem {
    border-color: var(--danger);
  }
  .bad {
    outline: 2px solid var(--danger);
    outline-offset: 2px;
  }
  header {
    padding: 6px 10px;
    border-bottom: 1px solid var(--surface-2);
    gap: 6px;
  }
  .name {
    font-weight: 600;
    border-color: transparent;
    background: transparent;
    min-width: 12em;
    flex: 0 1 22em;
  }
  .name:hover,
  .name:focus {
    border-color: var(--border);
  }
  .badge {
    color: var(--ok);
    font-size: 0.85rem;
    font-weight: 600;
  }
  .cols {
    display: grid;
    grid-template-columns: minmax(240px, 1fr) minmax(320px, 2fr);
  }
  @media (max-width: 800px) {
    .cols {
      grid-template-columns: 1fr;
    }
  }
  section {
    padding: 10px 12px;
    min-width: 0;
  }
  .when {
    background: var(--when-soft);
    border-right: 1px solid var(--surface-2);
    border-bottom-left-radius: var(--radius);
  }
  h3 {
    margin: 0 0 8px;
    font-size: 0.8rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--muted);
  }
  .trigger {
    display: flex;
    align-items: flex-start;
    background: var(--surface);
    border: 1px solid var(--border);
    border-left: 4px solid var(--when);
    border-radius: var(--radius-s);
  }
  .tbtn {
    flex: 1;
    flex-direction: column;
    align-items: flex-start;
    white-space: normal;
    text-align: left;
    gap: 2px;
  }
  .tbtn span {
    font-family: var(--mono);
    font-size: 0.85rem;
    color: var(--muted);
  }
  .or {
    font-size: 0.8rem;
    margin: 2px 0 2px 8px;
  }
  .addw {
    color: var(--when);
    margin-top: 4px;
  }
  .conds {
    margin-top: 10px;
  }
  .small-title {
    font-size: 0.8rem;
    margin-bottom: 4px;
  }
</style>
