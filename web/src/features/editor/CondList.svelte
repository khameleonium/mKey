<!--
  CondList — список условий события или вложенных условий («Хотя бы одно из»…): у каждого
  условия — название, поля параметров и кнопка «Удалить»; внизу — выбор нового условия.
  Props: list — массив блоков условий (часть проекта в редакторе);
  bad — номер условия с ошибкой последней проверки (подсвечивается; -2 — нет).
-->
<script lang="ts">
  import { t } from "../../lib/i18n/index.svelte";
  import type { CondBlock } from "../../lib/blocks";
  import SchemaForm from "./SchemaForm.svelte";
  import { useEditor } from "./editor.svelte";

  let { list, bad = -2 }: { list: CondBlock[]; bad?: number } = $props();
  const ed = useEditor();
</script>

<div class="conds">
  {#each list as c, i (c.uid)}
    {@const entry = ed.entry("condition", c.type)}
    <div class="cond" class:bad={bad === i}>
      <div class="row head">
        <span class="name" title={entry?.description ?? ""}>{entry?.name ?? c.type}</span>
        <span class="spacer"></span>
        <button class="ghost small" title={t("common.delete")} onclick={() => ed.remove(list, c)}
          >✕</button
        >
      </div>
      {#if entry?.params_schema}
        <SchemaForm
          schema={entry.params_schema}
          value={c.params}
          onchange={(v) => ed.set(c, "params", v as Record<string, unknown>)}
        />
      {/if}
    </div>
  {/each}

  <!-- Новое условие: выбор вида из реестра -->
  <select
    class="add"
    value=""
    onchange={(e) => {
      const type = e.currentTarget.value;
      e.currentTarget.value = "";
      if (type) ed.insert(list, ed.newCondition(type));
    }}
  >
    <option value="">+ {t("editor.add_condition")}</option>
    {#each ed.reg.condition ?? [] as entry (entry.id)}
      <option value={entry.id}>{entry.name}</option>
    {/each}
  </select>
</div>

<style>
  .conds {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .cond {
    border: 1px solid var(--border);
    border-left: 4px solid var(--when);
    border-radius: var(--radius-s);
    padding: 4px 8px 8px;
    background: var(--surface);
  }
  .head {
    margin-bottom: 4px;
  }
  .name {
    font-weight: 600;
    font-size: 0.92rem;
  }
  .bad {
    outline: 2px solid var(--danger);
    outline-offset: 2px;
  }
  .add {
    align-self: flex-start;
    color: var(--when);
    font-size: 0.88rem;
  }
</style>
