<!--
  Block — один блок действия: заголовок с названием (за него блок перетаскивают), кнопки
  «Копия» и «Удалить» и поля параметров, построенные по схеме вида (SchemaForm).
  Props: block — блок; list — список, в котором он лежит; bad — подсветить как блок с ошибкой.
-->
<script lang="ts">
  import { t } from "../../lib/i18n/index.svelte";
  import type { ActionBlock } from "../../lib/blocks";
  import SchemaForm from "./SchemaForm.svelte";
  import { useEditor } from "./editor.svelte";

  let {
    block,
    list,
    bad = false,
  }: { block: ActionBlock; list: ActionBlock[]; bad?: boolean } = $props();
  const ed = useEditor();

  /** Вид блока из реестра (нет — блок от отключённого модуля или плагина). */
  let entry = $derived(ed.entry("action", block.type));
</script>

<div class="block cat-{entry?.category ?? 'unknown'}" class:bad role="listitem">
  <!-- Заголовок: перетаскивание, название, действия с блоком -->
  <div
    class="head"
    draggable="true"
    role="button"
    tabindex="-1"
    title={entry?.description ?? ""}
    ondragstart={(e) => {
      e.stopPropagation();
      e.dataTransfer?.setData("text/plain", block.uid);
      ed.drag = { uid: block.uid };
    }}
    ondragend={() => (ed.drag = null)}
  >
    <span class="grip" aria-hidden="true">⋮⋮</span>
    <span class="name">{entry?.name ?? block.type}</span>
    <span class="spacer"></span>
    <button
      class="ghost small"
      title={t("editor.duplicate")}
      onclick={() => ed.duplicate(list, block)}>⧉</button
    >
    <button class="ghost small" title={t("common.delete")} onclick={() => ed.remove(list, block)}
      >✕</button
    >
  </div>

  <!-- Параметры -->
  <div class="body">
    {#if entry}
      {#if entry.params_schema}
        <SchemaForm
          schema={entry.params_schema}
          value={block.value}
          onchange={(v) => ed.set(block, "value", v)}
        />
      {/if}
    {:else}
      <span class="unknown">{t("editor.unknown_block", { type: block.type })}</span>
      <code>{JSON.stringify(block.value)}</code>
    {/if}
  </div>
</div>

<style>
  .block {
    --cat: var(--accent);
    background: var(--surface);
    border: 1px solid var(--border);
    border-left: 4px solid var(--cat);
    border-radius: var(--radius-s);
  }
  .cat-keyboard {
    --cat: #3d6cf0;
  }
  .cat-mouse {
    --cat: #0f9d8a;
  }
  .cat-time {
    --cat: #d98a00;
  }
  .cat-logic {
    --cat: #8c4de0;
  }
  .cat-system {
    --cat: #6b7280;
  }
  .cat-script {
    --cat: #c2410c;
  }
  .cat-unknown {
    --cat: var(--danger);
  }
  .block.bad {
    outline: 2px solid var(--danger);
    outline-offset: 2px;
  }
  .head {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 3px 4px 3px 8px;
    cursor: grab;
    border-bottom: 1px solid var(--surface-2);
  }
  .grip {
    color: var(--muted);
    letter-spacing: -2px;
  }
  .name {
    font-weight: 600;
    font-size: 0.92rem;
  }
  .body {
    padding: 8px 10px;
  }
  .body:empty {
    display: none;
  }
  .unknown {
    color: var(--danger);
    margin-right: 8px;
  }
</style>
